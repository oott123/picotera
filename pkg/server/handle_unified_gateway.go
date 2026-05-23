package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"picotera/pkg/annotations"
	"picotera/pkg/artifacts"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/jsx"
	"picotera/pkg/llmbridge"
	"picotera/pkg/logx"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// handleUnifiedGenerate returns the http.HandlerFunc that backs one of the
// five unified gateway routes. Source format is hard-wired into the closure;
// stream behavior is determined per request: for Anthropic / OpenAI sources
// we read body.stream, for Gemini sources the route variant fixes it.
//
// The handler mirrors handle_gateway.go's orchestration but swaps three
// things: model extraction (path-aware for Gemini), MPE resolution (uses the
// new endpoint-type-set query, not the path-based one), and a bridge step
// that rewrites the post-rewriteRequest body into the candidate's upstream
// format when the formats differ.
func (s *Server) handleUnifiedGenerate(srcFormat llmbridge.Format) http.HandlerFunc {
	h := &gatewayHandler{s}
	return func(w http.ResponseWriter, r *http.Request) {
		gatewayStart := time.Now()
		bgCtx := context.Background()

		// 1. Read body. We must have read it before authenticating so that
		// model extraction (which inspects the body for OpenAI/Anthropic
		// sources) can run.
		body, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			writeGatewayError(w, http.StatusInternalServerError, "failed to read request body", errorx.InternalError.Error())
			return
		}

		// 2. Synthesize a hook-visible endpoint for the route. The unified
		// routes are not rows in the endpoint table; this struct is what the
		// JS hooks see when they read ctx.endpoint.
		virtualEndpoint := db.Endpoint{
			Name:                "(unified)",
			Path:                r.URL.Path,
			ModelPath:           "", // model resolution is route-specific below
			CredentialsResolver: contract.CredentialsResolver_Unknown,
			EndpointType:        sourceEndpointType(srcFormat),
		}

		// 3. Insert the meta request row.
		metaID, metaIDCreatedAt := newRequestID()
		metaReqHeader := r.Header.Clone()
		parentSpanID := extractParentSpanID(metaReqHeader)
		parentSpanIDPg := pgtype.Text{String: parentSpanID, Valid: parentSpanID != ""}
		userMessagePreview := extractUserMessagePreview(body, virtualEndpoint.EndpointType)
		// See handle_gateway.go for the late-binding rationale. Candidate
		// extraction is pure; resolution waits for authentication so the
		// per-account project namespace can be consulted.
		projectCandidates := h.extractProjectCandidates(r.Context(), body)
		var projectIDPg pgtype.Int4
		metaCreatedAt := h.insertRequest(bgCtx, db.InsertRequestParams{
			ID:                 metaID,
			SpanID:             pgtype.Text{String: metaID, Valid: true},
			ParentSpanID:       parentSpanIDPg,
			Type:               db.RequestTypeMeta,
			Status:             db.RequestStatusPending,
			ProviderID:         pgtype.Int4{Valid: false},
			EndpointPath:       pgtype.Text{String: virtualEndpoint.Path, Valid: true},
			ApiKeyID:           pgtype.Int4{Valid: false},
			Model:              pgtype.Text{Valid: false},
			UpstreamModel:      pgtype.Text{Valid: false},
			StatusCode:         pgtype.Int4{Valid: false},
			ErrorMessage:       pgtype.Text{Valid: false},
			TimeSpentMs:        pgtype.Int4{Valid: false},
			UserMessagePreview: userMessagePreview,
			ProjectID:          projectIDPg,
			CreatedAt:          pgtype.Timestamp{Time: metaIDCreatedAt, Valid: true},
		})
		h.uploadRequestArtifact(bgCtx, metaID, metaCreatedAt, r.Method, r.URL.String(), metaReqHeader, body)

		// 4. Failure-path closures. Mirrors handle_gateway.go so that meta
		// rows always close out cleanly and meta-response artifacts capture
		// the error envelope plus any JS console output.
		var session *jsx.Session
		collectLogs := func() []artifacts.LogEntry {
			if session == nil {
				return nil
			}
			raw := session.Logs()
			if len(raw) == 0 {
				return nil
			}
			out := make([]artifacts.LogEntry, len(raw))
			for i, l := range raw {
				out[i] = artifacts.LogEntry{Level: l.Level, Message: l.Message, Ts: l.Ts}
			}
			return out
		}
		failMeta := func(statusCode int32, errMsg string) {
			h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
				ID:           metaID,
				StatusCode:   pgtype.Int4{Int32: statusCode, Valid: true},
				ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
				TimeSpentMs:  pgtype.Int4{Int32: int32(time.Since(gatewayStart).Milliseconds()), Valid: true},
				Status:       db.RequestStatusFailed,
				CreatedAt:    pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
			})
		}
		failMetaResponse := func(err error) {
			statusCode, respBody := handleGatewayErr(w, err)
			h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, statusCode, w.Header().Clone(), respBody, collectLogs())
		}
		failHook := func(err error) {
			status := http.StatusBadGateway
			if errors.Is(err, jsx.ErrHookTimeout) {
				status = http.StatusServiceUnavailable
			}
			errMsg := err.Error()
			failMeta(int32(status), errMsg)
			respBody := writeGatewayError(w, status, errMsg, errorx.UpstreamError.Error())
			h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, status, w.Header().Clone(), respBody, collectLogs())
		}

		// 5. Authenticate. Resolver=Unknown forces the full fallback chain
		// over Authorization/X-Api-Key/?key=/X-Goog-Api-Key, which matches
		// what the unified routes advertise.
		apiKey, err := h.authenticateClient(r.Context(), r, contract.CredentialsResolver_Unknown)
		if err != nil {
			var gwErr *gatewayError
			if errors.As(err, &gwErr) {
				failMeta(int32(gwErr.status), gwErr.message)
			} else {
				failMeta(http.StatusInternalServerError, "auth validation failed")
			}
			failMetaResponse(err)
			return
		}
		apiKeyID := pgtype.Int4{Int32: apiKey.ID, Valid: true}
		apiKeyJS := apiKeySummaryFromRow(apiKey)
		apiKeyAnno := apiKeyJS.Annotations
		h.updateRequestOnHeader(bgCtx, db.UpdateRequestOnHeaderParams{
			ID:           metaID,
			EndpointPath: pgtype.Text{String: virtualEndpoint.Path, Valid: true},
			ApiKeyID:     apiKeyID,
			Status:       db.RequestStatusPending,
			CreatedAt:    pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
		})

		// Resolve project_id within the api_key's account namespace (mirrors
		// handle_gateway.go). System keys skip both lookup and auto-create.
		apiKeyAccountID := accountIDForAPIKey(apiKey)
		projectIDPg = h.resolveProjectForAccount(r.Context(), apiKeyAccountID, projectCandidates)
		if projectIDPg.Valid {
			h.updateRequestProjectID(bgCtx, db.UpdateRequestProjectIDParams{
				ID:        metaID,
				ProjectID: projectIDPg,
				CreatedAt: pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
			})
			go h.upsertProjectSeen(projectIDPg.Int32, metaCreatedAt)
		}

		// 6. Resolve model name and stream flag. Format-specific.
		modelName, streaming, err := extractUnifiedModelAndStream(srcFormat, r, body)
		if err != nil {
			var gwErr *gatewayError
			if errors.As(err, &gwErr) {
				failMeta(int32(gwErr.status), gwErr.message)
			} else {
				failMeta(http.StatusBadRequest, "model extraction failed")
			}
			failMetaResponse(err)
			return
		}
		h.updateRequestModel(bgCtx, db.UpdateRequestModelParams{
			ID:        metaID,
			Model:     pgtype.Text{String: modelName, Valid: modelName != ""},
			CreatedAt: pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
		})

		// 7. Build the JS session and run rewriteModel once.
		session, err = h.jsxEngine.NewSession(r.Context(), metaID)
		if err != nil {
			errMsg := "failed to load js hooks: " + err.Error()
			failMeta(http.StatusBadGateway, errMsg)
			respBody := writeGatewayError(w, http.StatusBadGateway, errMsg, errorx.UpstreamError.Error())
			h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusBadGateway, w.Header().Clone(), respBody, nil)
			return
		}
		defer session.Close()

		originalModelName := modelName
		// pathVars carries the matched chi path variables so they can be
		// surfaced to JS hooks (matches the path-based gateway's contract).
		pathVars := chiURLParams(r)
		initialClientReq := serializeClientRequest(r, body, modelName, pathVars)
		modelAnno := h.fetchModelAnnotations(r.Context(), modelName)
		newModel, err := session.RunRewriteModelHook(jsx.RewriteModelInput{
			Request:     initialClientReq,
			Model:       originalModelName,
			ApiKey:      apiKeyJS,
			Annotations: annotations.Merge(modelAnno, apiKeyAnno),
		}, modelName)
		if err != nil {
			failHook(err)
			return
		}
		preRewriteBody := body
		if newModel != modelName {
			updated, serr := setUnifiedModel(srcFormat, body, newModel)
			if serr != nil {
				errMsg := "failed to set model in body: " + serr.Error()
				failMeta(http.StatusInternalServerError, errMsg)
				respBody := writeGatewayError(w, http.StatusInternalServerError, errMsg, errorx.InternalError.Error())
				h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusInternalServerError, w.Header().Clone(), respBody, collectLogs())
				return
			}
			body = updated
			modelName = newModel
			modelAnno = h.fetchModelAnnotations(r.Context(), modelName)
			// request.model tracks the routed (post-rewrite) model — the
			// name used to resolve MPE rows and match billing. Overwrite
			// the extracted value now that rewriteModel has settled.
			h.updateRequestModel(bgCtx, db.UpdateRequestModelParams{
				ID:        metaID,
				Model:     pgtype.Text{String: modelName, Valid: modelName != ""},
				CreatedAt: pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
			})
		}
		// routedModel is the post-rewrite model name: matches the `model`
		// table row that drives pricing and was used to resolve providers.
		// Subsequent request-row writes use this, not originalModelName.
		routedModel := modelName

		// 8. Resolve candidate providers across the endpoint-type set.
		typeSet := candidateEndpointTypes(srcFormat, streaming)
		providers, err := h.resolveProvidersByTypes(r.Context(), modelName, typeSet, sourceEndpointType(srcFormat))
		if err != nil {
			var gwErr *gatewayError
			if errors.As(err, &gwErr) {
				failMeta(int32(gwErr.status), gwErr.message)
			} else {
				failMeta(http.StatusInternalServerError, "failed to query providers")
			}
			failMetaResponse(err)
			return
		}

		// 9. Build candidate list for hooks plus a sidecar carrying upstream
		// URL, credentials, send resolver, chosen upstream format, and the
		// per-candidate merged annotations. The sidecar is keyed by
		// providerID+endpointPath because one provider can have rows for
		// multiple endpoints in the type set (e.g. both anthropicMessages and
		// openaiChatCompletions).
		if len(providers) > 0 {
			if m, derr := annotations.Decode(providers[0].ModelAnnotations); derr == nil {
				modelAnno = m
			}
		}
		annoBuilder, err := newCandidateAnnotationsBuilder(nil, apiKeyAnno)
		if err != nil {
			errMsg := "failed to build annotations: " + err.Error()
			failMeta(http.StatusInternalServerError, errMsg)
			respBody := writeGatewayError(w, http.StatusInternalServerError, errMsg, errorx.InternalError.Error())
			h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusInternalServerError, w.Header().Clone(), respBody, collectLogs())
			return
		}
		annoBuilder.modelAnno = modelAnno

		type providerSidecar struct {
			upstreamURL             string
			credentials             string
			sendResolver            int32
			proxyURL                string
			upFormat                llmbridge.Format
			endpointPath            string
			annotations             map[string]string
			supportsNativeWebSearch bool
		}
		sidecar := make(map[string]providerSidecar, len(providers))
		candidates := make([]jsx.Candidate, 0, len(providers))
		for _, row := range providers {
			entryAnno, _ := annotations.Decode(row.Annotations)
			merged, providerAnno := annoBuilder.merge(row.ProviderAnnotations, entryAnno)
			var proxyURL string
			if row.ProxyUrl.Valid {
				proxyURL = row.ProxyUrl.String
			}
			key := fmt.Sprintf("%d|%s", row.ProviderID, row.EndpointPath)
			sidecar[key] = providerSidecar{
				upstreamURL:             row.UpstreamUrl,
				credentials:             row.ProviderCredentials,
				sendResolver:            effectiveSendResolver(virtualEndpoint.CredentialsResolver, row.SendCredentialsResolver),
				proxyURL:                proxyURL,
				upFormat:                upstreamFormatFor(row.EndpointType),
				endpointPath:            row.EndpointPath,
				annotations:             merged,
				supportsNativeWebSearch: row.SupportsNativeWebSearch,
			}
			candidates = append(candidates, jsx.Candidate{
				Provider: jsx.ProviderSummary{
					ID:          row.ProviderID,
					Name:        row.ProviderName,
					Priority:    row.ProviderPriority,
					Annotations: providerAnno,
				},
				MPE: jsx.CandidateMPE{
					ModelName:         row.ModelName,
					ProviderID:        row.ProviderID,
					EndpointPath:      row.EndpointPath,
					UpstreamModelName: row.UpstreamModelName,
					Priority:          row.Priority,
					Annotations:       entryAnno,
				},
				Annotations: merged,
			})
		}

		modelJS := &jsx.ModelSummary{Name: modelName, Annotations: modelAnno}
		endpointJS := endpointSummaryFromRow(virtualEndpoint)

		jsClientRequest := serializeClientRequest(r, body, modelName, pathVars)
		sortedCandidates, err := session.RunSortHook(jsx.SortInput{
			Endpoint:    endpointJS,
			Model:       modelJS,
			Request:     jsClientRequest,
			Providers:   candidates,
			ApiKey:      apiKeyJS,
			Annotations: annotations.Merge(modelAnno, apiKeyAnno),
		})
		if err != nil {
			failHook(err)
			return
		}

		// 10. Retry loop. Mirrors handle_gateway.go's body, with the bridge
		// step inserted after rewriteRequest.
		var lastErr error
		var lastJSErr *jsx.LastError
		i := 0
		currentRetryCount := 0
		totalAttemptCount := 0
		// wsCtx is populated when the current attempt's chosen provider
		// requires emulating Anthropic web search via Exa. It survives a
		// successful attempt into unifiedStreamSuccess; on retry we reset it
		// per attempt because the next candidate may not need emulation.
		var wsCtx *webSearchContext

		for {
			if i >= len(sortedCandidates) {
				break
			}
			if totalAttemptCount >= h.config.JSMaxTotalAttempts {
				break
			}
			cand := sortedCandidates[i]
			providerID := candidateProviderID(cand)
			candPath := candidateEndpointPath(cand)
			side, hasSide := sidecar[fmt.Sprintf("%d|%s", providerID, candPath)]
			if !hasSide {
				// JS introduced an unknown provider+path pair — skip safely.
				i++
				currentRetryCount = 0
				continue
			}

			candAnno := cand.Annotations
			if candAnno == nil {
				candAnno = side.annotations
			}
			dec, herr := session.RunBeforeRequestHook(jsx.BeforeRequestInput{
				Endpoint:          endpointJS,
				Model:             modelJS,
				Request:           jsClientRequest,
				Provider:          cand.Provider,
				MPE:               cand.MPE,
				CurrentRetryCount: currentRetryCount,
				TotalAttemptCount: totalAttemptCount,
				LastError:         lastJSErr,
				ApiKey:            apiKeyJS,
				Annotations:       candAnno,
			})
			if herr != nil {
				failHook(herr)
				return
			}
			if dec.Delay > 0 {
				d := time.Duration(dec.Delay) * time.Millisecond
				if h.config.JSMaxDelay > 0 && d > h.config.JSMaxDelay {
					d = h.config.JSMaxDelay
				}
				time.Sleep(d)
			}
			if dec.Next {
				i++
				currentRetryCount = 0
				continue
			}

			attemptStart := time.Now()
			ctx, cancel := context.WithCancel(r.Context())

			// Pick the upstream model for this attempt (hook → MPE → original).
			upstreamModel := dec.UpstreamModel
			if upstreamModel == "" {
				upstreamModel = candidateUpstreamModel(cand)
			}
			if upstreamModel == "" {
				upstreamModel = modelName
			}

			upstreamID, upstreamIDCreatedAt := newRequestID()
			upstreamCreatedAt := h.insertRequest(bgCtx, db.InsertRequestParams{
				ID:                 upstreamID,
				SpanID:             pgtype.Text{String: metaID, Valid: true},
				ParentSpanID:       parentSpanIDPg,
				Type:               db.RequestTypeUpstream,
				Status:             db.RequestStatusPending,
				ProviderID:         pgtype.Int4{Int32: providerID, Valid: true},
				EndpointPath:       pgtype.Text{String: side.endpointPath, Valid: side.endpointPath != ""},
				ApiKeyID:           apiKeyID,
				Model:              pgtype.Text{String: routedModel, Valid: routedModel != ""},
				UpstreamModel:      pgtype.Text{String: upstreamModel, Valid: upstreamModel != ""},
				StatusCode:         pgtype.Int4{Valid: false},
				ErrorMessage:       pgtype.Text{Valid: false},
				TimeSpentMs:        pgtype.Int4{Valid: false},
				UserMessagePreview: pgtype.Text{Valid: false},
				ProjectID:          projectIDPg,
				CreatedAt:          pgtype.Timestamp{Time: upstreamIDCreatedAt, Valid: true},
			})

			// Build the upstream request in source format. The body has the
			// chosen upstream model name swapped in (when the source format
			// carries it); for Gemini sources, that's a no-op because the
			// body has no model field.
			srcBody := body
			if upstreamModel != "" {
				srcBody, err = setUnifiedModel(srcFormat, body, upstreamModel)
				if err != nil {
					h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, err.Error())
					lastErr = err
					lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: err.Error()}
					currentRetryCount++
					totalAttemptCount++
					cancel()
					continue
				}
			}
			req, reqBody, berr := buildUpstreamRequest(ctx, r, srcBody, side.upstreamURL, "", side.credentials, side.sendResolver, pathVars)
			if berr != nil {
				h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, berr.Error())
				lastErr = berr
				lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: berr.Error()}
				currentRetryCount++
				totalAttemptCount++
				cancel()
				continue
			}

			// rewriteRequest hook — JS sees the source-format body.
			newPending, rerr := session.RunRewriteHook(jsx.RewriteInput{
				Endpoint:          endpointJS,
				Model:             modelJS,
				Provider:          cand.Provider,
				MPE:               cand.MPE,
				CurrentRetryCount: currentRetryCount,
				TotalAttemptCount: totalAttemptCount,
				ClientRequest:     jsClientRequest,
				PendingRequest:    serializePendingRequest(req, reqBody),
				ApiKey:            apiKeyJS,
				Annotations:       candAnno,
			})
			if rerr != nil {
				failHook(rerr)
				cancel()
				return
			}
			req, reqBody, rerr = buildRequestFromPending(ctx, newPending, reqBody)
			if rerr != nil {
				failHook(rerr)
				cancel()
				return
			}

			// Web search emulation: when the source is Anthropic Messages and
			// the chosen upstream does not support native web search, rewrite
			// the outbound tools array and history to use a function-tool
			// equivalent. The response transformer wraps the reverse leg.
			wsCtx = nil
			if srcFormat == llmbridge.FormatAnthropicMessages && !side.supportsNativeWebSearch && hasWebSearchTool(reqBody) {
				rewrote, wsErr := rewriteWebSearchTools(reqBody)
				if wsErr != nil {
					h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, wsErr.Error())
					lastErr = wsErr
					lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: wsErr.Error()}
					currentRetryCount++
					totalAttemptCount++
					cancel()
					continue
				}
				rewrote, wsErr = rewriteWebSearchHistory(rewrote)
				if wsErr != nil {
					h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, wsErr.Error())
					lastErr = wsErr
					lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: wsErr.Error()}
					currentRetryCount++
					totalAttemptCount++
					cancel()
					continue
				}
				reqBody = rewrote
				req.Body = io.NopCloser(bytes.NewReader(reqBody))
				req.ContentLength = int64(len(reqBody))
				req.GetBody = func() (io.ReadCloser, error) {
					return io.NopCloser(bytes.NewReader(reqBody)), nil
				}
				wsCtx = &webSearchContext{
					active:              true,
					apiKeyToken:         apiKey.Key,
					metaID:              metaID,
					metaCreatedAt:       metaCreatedAt,
					parentSpanID:        parentSpanID,
					originalRequestBody: append([]byte(nil), preRewriteBody...),
				}
			}

			baseProfile, perr := llmbridge.DefaultOutboundProfileForFormat(side.upFormat)
			if perr != nil {
				h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, perr.Error())
				lastErr = perr
				lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: perr.Error()}
				currentRetryCount++
				totalAttemptCount++
				cancel()
				continue
			}
			initialProfile := jsx.OutboundProfile{Type: baseProfile.Type, Config: map[string]any{}}
			hookProfile, perr := session.RunBeforeTransformHook(jsx.BeforeTransformInput{
				Endpoint:          endpointJS,
				Model:             modelJS,
				Provider:          cand.Provider,
				MPE:               cand.MPE,
				CurrentRetryCount: currentRetryCount,
				TotalAttemptCount: totalAttemptCount,
				ClientRequest:     jsClientRequest,
				PendingRequest:    newPending,
				ApiKey:            apiKeyJS,
				Annotations:       candAnno,
				SourceFormat:      srcFormat.String(),
				UpstreamFormat:    side.upFormat.String(),
				Stream:            streaming,
			}, initialProfile)
			if perr != nil {
				failHook(perr)
				cancel()
				return
			}
			outboundProfile := llmbridge.OutboundProfile{
				Type:   hookProfile.Type,
				Config: hookProfile.Config,
			}
			if outboundProfile.Config == nil {
				outboundProfile.Config = map[string]any{}
			}

			// Bridge step. When formats match, BridgeRequest is identity.
			if side.upFormat != srcFormat {
				if !s.llmBridge.Enabled() {
					brerr := fmt.Errorf("llmbridge: wasm module is not configured")
					h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, brerr.Error())
					lastErr = brerr
					lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: brerr.Error()}
					currentRetryCount++
					totalAttemptCount++
					cancel()
					continue
				}
				bridgeURL := req.URL.String()
				if srcFormat == llmbridge.FormatGeminiGenerateContent || srcFormat == llmbridge.FormatGeminiStreamGenerateContent {
					// The Gemini Inbound parser reads model and stream from
					// httpReq.Path. Synthesize a path that matches the
					// route variant we're serving and the model the client
					// asked for, so the parsed *llm.Request carries them.
					bridgeURL = llmbridge.SyntheticGeminiPath(srcFormat, originalModelName)
				}
				upBody, upCT, brerr := s.llmBridge.BridgeRequest(ctx, srcFormat, side.upFormat, reqBody, req.Header, bridgeURL, outboundProfile)
				if brerr != nil {
					h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, brerr.Error())
					lastErr = brerr
					lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: brerr.Error()}
					currentRetryCount++
					totalAttemptCount++
					cancel()
					continue
				}
				// Rewrite the upstream-bound request body. We keep the URL
				// (picotera-configured) and headers (post-rewriteRequest),
				// only overriding Content-Type so the upstream parses the
				// converted bytes correctly.
				req.Body = io.NopCloser(bytes.NewReader(upBody))
				req.ContentLength = int64(len(upBody))
				req.GetBody = func() (io.ReadCloser, error) {
					return io.NopCloser(bytes.NewReader(upBody)), nil
				}
				req.Header.Set("Content-Type", upCT)
				reqBody = upBody
			}

			// Upload upstream-request artifact AFTER bridge so it reflects
			// what was actually written on the wire.
			h.uploadRequestArtifact(bgCtx, upstreamID, upstreamCreatedAt, req.Method, req.URL.String(), req.Header.Clone(), reqBody)

			upstreamStartTime := time.Now()
			resp, ferr := h.forwardRequest(req, side.proxyURL)
			if ferr != nil {
				h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, ferr.Error())
				lastErr = ferr
				lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: ferr.Error()}
				currentRetryCount++
				totalAttemptCount++
				cancel()
				continue
			}

			if resp.StatusCode == http.StatusOK {
				metaLogs := collectLogs()
				h.unifiedStreamSuccess(unifiedStreamArgs{
					w: w, r: r, ctx: ctx, cancel: cancel, resp: resp,
					srcFormat:         srcFormat,
					upFormat:          side.upFormat,
					outboundProfile:   outboundProfile,
					upstreamID:        upstreamID,
					upstreamCreatedAt: upstreamCreatedAt,
					attemptStart:      attemptStart,
					metaID:            metaID,
					metaCreatedAt:     metaCreatedAt,
					gatewayStart:      gatewayStart,
					providerID:        providerID,
					routedModel:       routedModel,
					upstreamModel:     upstreamModel,
					metaEndpointPath:  virtualEndpoint.Path,
					upstreamPath:      side.endpointPath,
					upstreamStartTime: upstreamStartTime,
					bgCtx:             bgCtx,
					metaLogs:          metaLogs,
					apiKeyID:          apiKeyID,
					wsCtx:             wsCtx,
				})
				return
			}

			// Non-200 — try the next candidate. The error body stays in the
			// upstream's native format because we never bridge it.
			decoded, derr := decodedBody(resp)
			if derr != nil {
				_ = resp.Body.Close()
				errMsg := "decode upstream response: " + derr.Error()
				h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, int32(resp.StatusCode), errMsg)
				lastErr = fmt.Errorf("%s", errMsg)
				lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: resp.StatusCode, Message: errMsg}
				currentRetryCount++
				totalAttemptCount++
				cancel()
				continue
			}
			respBody, rerr := io.ReadAll(decoded.Body)
			_ = decoded.Body.Close()
			if rerr != nil {
				errMsg := "decode upstream response: " + rerr.Error()
				h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, int32(resp.StatusCode), errMsg)
				lastErr = fmt.Errorf("%s", errMsg)
				lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: resp.StatusCode, Message: errMsg}
				currentRetryCount++
				totalAttemptCount++
				cancel()
				continue
			}
			h.uploadResponseArtifact(bgCtx, upstreamID, upstreamCreatedAt, resp.StatusCode, resp.Header.Clone(), respBody)
			errMsg := string(respBody)
			h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
				ID:           upstreamID,
				StatusCode:   pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
				ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
				TimeSpentMs:  pgtype.Int4{Int32: int32(time.Since(attemptStart).Milliseconds()), Valid: true},
				Status:       db.RequestStatusFailed,
				CreatedAt:    pgtype.Timestamp{Time: upstreamCreatedAt, Valid: true},
			})
			lastErr = fmt.Errorf("upstream returned %d: %s", resp.StatusCode, errMsg)
			lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: resp.StatusCode, Message: errMsg}
			currentRetryCount++
			totalAttemptCount++
			cancel()
		}

		errMsg := "all providers failed"
		if lastErr != nil {
			errMsg = lastErr.Error()
		}
		failMeta(http.StatusBadGateway, errMsg)
		respBody := writeGatewayError(w, http.StatusBadGateway, errMsg, errorx.UpstreamError.Error())
		h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusBadGateway, w.Header().Clone(), respBody, collectLogs())
	}
}

// sourceEndpointType maps an llmbridge format back to the EndpointType_*
// constant used by the contract package, so the synthetic endpoint shown to
// JS hooks reports a consistent endpoint_type.
func sourceEndpointType(f llmbridge.Format) int32 {
	switch f {
	case llmbridge.FormatAnthropicMessages:
		return contract.EndpointType_AnthropicMessages
	case llmbridge.FormatOpenAIChatCompletions:
		return contract.EndpointType_OpenAIChatCompletions
	case llmbridge.FormatOpenAIResponses:
		return contract.EndpointType_OpenAIResponses
	case llmbridge.FormatGeminiGenerateContent:
		return contract.EndpointType_GeminiGenerateContent
	case llmbridge.FormatGeminiStreamGenerateContent:
		return contract.EndpointType_GeminiStreamGenerateContent
	default:
		return contract.EndpointType_Unknown
	}
}

// upstreamFormatFor maps a candidate row's endpoint_type to the bridge
// format. Endpoint types not in the generation set never appear in the
// type-set query result, so they default to Unknown which fails the bridge
// loudly if it ever sneaks in.
func upstreamFormatFor(t int32) llmbridge.Format {
	switch t {
	case contract.EndpointType_AnthropicMessages:
		return llmbridge.FormatAnthropicMessages
	case contract.EndpointType_OpenAIChatCompletions:
		return llmbridge.FormatOpenAIChatCompletions
	case contract.EndpointType_OpenAIResponses:
		return llmbridge.FormatOpenAIResponses
	case contract.EndpointType_GeminiGenerateContent:
		return llmbridge.FormatGeminiGenerateContent
	case contract.EndpointType_GeminiStreamGenerateContent:
		return llmbridge.FormatGeminiStreamGenerateContent
	default:
		return llmbridge.FormatUnknown
	}
}

// candidateEndpointTypes returns the endpoint_type ids that should be
// considered for a given (source format, stream flag) tuple. Mirrors the
// table in api.md.
func candidateEndpointTypes(src llmbridge.Format, streaming bool) []int32 {
	// Anthropic and OpenAI sources share the same set; only the Gemini pair
	// is filtered by the stream flag.
	switch src {
	case llmbridge.FormatGeminiGenerateContent:
		return []int32{
			contract.EndpointType_AnthropicMessages,
			contract.EndpointType_OpenAIChatCompletions,
			contract.EndpointType_OpenAIResponses,
			contract.EndpointType_GeminiGenerateContent,
		}
	case llmbridge.FormatGeminiStreamGenerateContent:
		return []int32{
			contract.EndpointType_AnthropicMessages,
			contract.EndpointType_OpenAIChatCompletions,
			contract.EndpointType_OpenAIResponses,
			contract.EndpointType_GeminiStreamGenerateContent,
		}
	}
	geminiVariant := contract.EndpointType_GeminiGenerateContent
	if streaming {
		geminiVariant = contract.EndpointType_GeminiStreamGenerateContent
	}
	return []int32{
		contract.EndpointType_AnthropicMessages,
		contract.EndpointType_OpenAIChatCompletions,
		contract.EndpointType_OpenAIResponses,
		geminiVariant,
	}
}

// extractUnifiedModelAndStream picks the model name and stream flag for the
// inbound request. For Anthropic / OpenAI the body carries both; for Gemini
// the model lives in the chi {model} path variable and the stream flag is
// fixed by the route variant.
func extractUnifiedModelAndStream(src llmbridge.Format, r *http.Request, body []byte) (string, bool, error) {
	switch src {
	case llmbridge.FormatGeminiGenerateContent:
		m := chi.URLParam(r, "model")
		if m == "" {
			return "", false, &gatewayError{status: http.StatusBadRequest, message: "missing {model} path variable", code: errorx.ModelNotFound.Error()}
		}
		return m, false, nil
	case llmbridge.FormatGeminiStreamGenerateContent:
		m := chi.URLParam(r, "model")
		if m == "" {
			return "", false, &gatewayError{status: http.StatusBadRequest, message: "missing {model} path variable", code: errorx.ModelNotFound.Error()}
		}
		return m, true, nil
	case llmbridge.FormatAnthropicMessages, llmbridge.FormatOpenAIChatCompletions, llmbridge.FormatOpenAIResponses:
		model := gjson.GetBytes(body, "model").Str
		if model == "" {
			return "", false, &gatewayError{status: http.StatusBadRequest, message: "model is required", code: errorx.ModelNotFound.Error()}
		}
		stream := gjson.GetBytes(body, "stream").Bool()
		return model, stream, nil
	}
	return "", false, &gatewayError{status: http.StatusBadRequest, message: "unsupported source format", code: errorx.InvalidRequest.Error()}
}

// setUnifiedModel rewrites the model name carried by the source body. Gemini
// requests carry no model field — the unified handler swaps the URL path
// variable instead, but at this layer we just leave the body alone.
func setUnifiedModel(src llmbridge.Format, body []byte, newModel string) ([]byte, error) {
	switch src {
	case llmbridge.FormatGeminiGenerateContent, llmbridge.FormatGeminiStreamGenerateContent:
		return body, nil
	}
	return sjson.SetBytes(body, "model", newModel)
}

// chiURLParams collects path variables that the chi router matched onto r,
// so they can be surfaced to JS hooks via RequestShape.PathVars. Currently
// only the Gemini routes carry one ("model"); pulling it generically keeps
// the surface symmetrical with the path-based gateway.
func chiURLParams(r *http.Request) map[string]string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return nil
	}
	keys := rctx.URLParams.Keys
	values := rctx.URLParams.Values
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]string, len(keys))
	for i, k := range keys {
		if i < len(values) && k != "" {
			out[k] = values[i]
		}
	}
	return out
}

// candidateEndpointPath returns mpe.endpointPath for a candidate. Used
// together with provider id to look up the sidecar entry, since the unified
// handler can have multiple rows per provider when one provider serves
// multiple endpoint types.
func candidateEndpointPath(c jsx.Candidate) string {
	return c.MPE.EndpointPath
}

// resolveProvidersByTypes is the unified handler's analogue of resolveProviders.
// It runs the new sqlc query and applies the same priority sort and minimum
// validity filter (upstream URL + credentials non-empty). srcType is the
// inbound request's endpoint_type (from sourceEndpointType(srcFormat)) and
// drives the per-(provider, model) dedupe — see dedupeUnifiedRows.
func (s *Server) resolveProvidersByTypes(ctx context.Context, model string, types []int32, srcType int32) ([]db.GetProvidersByEndpointTypesAndModelRow, error) {
	rows, err := s.queries.GetProvidersByEndpointTypesAndModel(ctx, db.GetProvidersByEndpointTypesAndModelParams{
		ModelName:     model,
		EndpointTypes: types,
	})
	if err != nil {
		logx.WithContext(ctx).WithError(err).Error("unified provider lookup failed")
		return nil, &gatewayError{status: http.StatusInternalServerError, message: "failed to query providers", code: errorx.InternalError.Error()}
	}
	if len(rows) == 0 {
		return nil, &gatewayError{status: http.StatusNotFound, message: "no provider available for model", code: errorx.NoProviderAvailable.Error()}
	}
	valid := make([]db.GetProvidersByEndpointTypesAndModelRow, 0, len(rows))
	for _, row := range rows {
		if row.UpstreamUrl != "" && row.ProviderCredentials != "" {
			valid = append(valid, row)
		}
	}
	if len(valid) == 0 {
		return nil, &gatewayError{status: http.StatusNotFound, message: "no provider available for model", code: errorx.NoProviderAvailable.Error()}
	}
	valid = dedupeUnifiedRows(valid, srcType)
	// Sort by combined priority (provider + per-model-entry) descending.
	for i := 1; i < len(valid); i++ {
		for j := i; j > 0; j-- {
			pi := int(valid[j].Priority) + int(valid[j].ProviderPriority)
			pj := int(valid[j-1].Priority) + int(valid[j-1].ProviderPriority)
			if pi <= pj {
				break
			}
			valid[j], valid[j-1] = valid[j-1], valid[j]
		}
	}
	return valid, nil
}

// dedupeUnifiedRows collapses the type-set query result so that each
// (ProviderID, ModelName) pair contributes at most one row. ModelName is
// constant across the result set (the query is parameterized on it), so
// bucketing by ProviderID alone is sufficient.
//
// Within each bucket, betterRow picks the survivor by: srcType match >
// AnthropicMessages > OpenAIChatCompletions > endpoint.path lex order. The
// returned slice preserves the order of first appearance per provider, so the
// downstream priority sort sees a stable input.
func dedupeUnifiedRows(rows []db.GetProvidersByEndpointTypesAndModelRow, srcType int32) []db.GetProvidersByEndpointTypesAndModelRow {
	idx := make(map[int32]int, len(rows))
	out := make([]db.GetProvidersByEndpointTypesAndModelRow, 0, len(rows))
	for _, row := range rows {
		if pos, ok := idx[row.ProviderID]; ok {
			if betterRow(row, out[pos], srcType) {
				out[pos] = row
			}
			continue
		}
		idx[row.ProviderID] = len(out)
		out = append(out, row)
	}
	return out
}

// betterRow reports whether a should beat b within a (provider, model) bucket.
// Tie-breakers, in order:
//  1. srcType exact match (the row whose endpoint_type equals the inbound
//     request format wins).
//  2. AnthropicMessages format.
//  3. OpenAIChatCompletions format.
//  4. EndpointPath lexicographic ascending.
func betterRow(a, b db.GetProvidersByEndpointTypesAndModelRow, srcType int32) bool {
	rank := func(t int32) int {
		switch {
		case t == srcType:
			return 0
		case t == contract.EndpointType_AnthropicMessages:
			return 1
		case t == contract.EndpointType_OpenAIChatCompletions:
			return 2
		default:
			return 3
		}
	}
	ra, rb := rank(a.EndpointType), rank(b.EndpointType)
	if ra != rb {
		return ra < rb
	}
	return a.EndpointPath < b.EndpointPath
}

// unifiedStreamArgs bundles the (many) inputs the unified streaming success
// path needs. Wrapped so the call site stays readable.
//
// metaEndpointPath is the unified route path (`/api/picotera/v1/messages` …)
// — what the meta row should record. upstreamPath is the chosen upstream's
// configured endpoint.path — what the upstream row should record.
type unifiedStreamArgs struct {
	w                 http.ResponseWriter
	r                 *http.Request
	ctx               context.Context
	cancel            context.CancelFunc
	resp              *http.Response
	srcFormat         llmbridge.Format
	upFormat          llmbridge.Format
	outboundProfile   llmbridge.OutboundProfile
	upstreamID        string
	upstreamCreatedAt time.Time
	attemptStart      time.Time
	metaID            string
	metaCreatedAt     time.Time
	gatewayStart      time.Time
	providerID        int32
	routedModel       string
	upstreamModel     string
	metaEndpointPath  string
	upstreamPath      string
	upstreamStartTime time.Time
	bgCtx             context.Context
	metaLogs          []artifacts.LogEntry
	apiKeyID          pgtype.Int4
	wsCtx             *webSearchContext
}

// unifiedStreamSuccess is the streamSuccess analogue for unified routes. It
// runs the upstream bytes through the response extractor (so token/TTFT
// metrics still reflect the upstream's native format), captures them into
// the upstream-artifact buffer, then bridges to source format and writes the
// converted bytes to the client and the meta-artifact buffer.
//
// When src == upFormat the bridge is an identity wrapper, so this code path
// behaves exactly like streamSuccess for 1:1 cases.
func (h *gatewayHandler) unifiedStreamSuccess(a unifiedStreamArgs) {
	w, r, ctx, cancel, resp := a.w, a.r, a.ctx, a.cancel, a.resp

	h.updateRequestOnHeader(a.bgCtx, db.UpdateRequestOnHeaderParams{
		ID:            a.metaID,
		ProviderID:    pgtype.Int4{Int32: a.providerID, Valid: true},
		Model:         pgtype.Text{String: a.routedModel, Valid: a.routedModel != ""},
		UpstreamModel: pgtype.Text{String: a.upstreamModel, Valid: a.upstreamModel != ""},
		EndpointPath:  pgtype.Text{String: a.metaEndpointPath, Valid: a.metaEndpointPath != ""},
		ApiKeyID:      a.apiKeyID,
		Status:        db.RequestStatusHeaderReceived,
		CreatedAt:     pgtype.Timestamp{Time: a.metaCreatedAt, Valid: true},
	})
	h.updateRequestOnHeader(a.bgCtx, db.UpdateRequestOnHeaderParams{
		ID:            a.upstreamID,
		ProviderID:    pgtype.Int4{Int32: a.providerID, Valid: true},
		Model:         pgtype.Text{String: a.routedModel, Valid: a.routedModel != ""},
		UpstreamModel: pgtype.Text{String: a.upstreamModel, Valid: a.upstreamModel != ""},
		EndpointPath:  pgtype.Text{String: a.upstreamPath, Valid: a.upstreamPath != ""},
		ApiKeyID:      a.apiKeyID,
		Status:        db.RequestStatusHeaderReceived,
		CreatedAt:     pgtype.Timestamp{Time: a.upstreamCreatedAt, Valid: true},
	})

	// Forward upstream headers as-is when there's no bridge. When bridging,
	// strip Content-Type and Content-Length because the body shape changes;
	// we restore Content-Type below from the bridged side. Web-search
	// emulation also rewrites the body, so the same content-encoding stripping
	// applies — otherwise the client receives gzip headers but plaintext
	// bytes.
	bridging := a.srcFormat != a.upFormat
	wsActive := a.wsCtx != nil && a.wsCtx.active
	transforming := bridging || wsActive
	for key, values := range resp.Header {
		lower := strings.ToLower(key)
		if lower == "content-length" {
			continue
		}
		if transforming && (lower == "content-encoding" || lower == "transfer-encoding") {
			continue
		}
		if bridging && lower == "content-type" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	upstreamCT := resp.Header.Get("Content-Type")
	streamMode := strings.Contains(strings.ToLower(upstreamCT), "text/event-stream")

	clientCT := upstreamCT
	if bridging {
		if streamMode {
			clientCT = clientStreamContentType(a.srcFormat, upstreamCT)
		} else {
			clientCT = "application/json"
		}
	}

	if clientCT != "" {
		w.Header().Set("Content-Type", clientCT)
	}

	responseWriter := newLockedResponseWriter(w)
	clientWriter := io.Discard
	if !transforming {
		clientWriter = responseWriter
	}
	internalReader, derr := decodedInternalResponseReader(resp, clientWriter)
	if derr != nil {
		cancel()
		h.failUnifiedSuccess(a, "decode upstream response: "+derr.Error())
		_ = resp.Body.Close()
		return
	}
	internalBody := internalReader.Body
	w.WriteHeader(http.StatusOK)
	if err := internalReader.StartClientWrite(); err != nil {
		cancel()
		closeDecodedInternalResponseReader(internalBody, resp)
		return
	}
	metaRespHeader := w.Header().Clone()

	// Extractor reads decoded upstream bytes and forwards them; metrics come
	// from the upstream's native response format regardless of bridging.
	extractor := NewResponseExtractor(internalBody, upstreamCT, a.upstreamStartTime)

	var upstreamCapture bytes.Buffer
	teedUpstream := llmbridge.NewUpstreamTee(asReadCloser(extractor, internalBody), &upstreamCapture)

	// clientReader produces the bytes we will actually write to the client
	// (and into the meta-artifact buffer). When bridging it's the bridge
	// output; otherwise it's the upstream tee directly.
	var clientReader io.ReadCloser
	if bridging {
		if streamMode {
			br, err := h.llmBridge.BridgeStream(ctx, a.srcFormat, a.upFormat, teedUpstream, upstreamCT, a.outboundProfile)
			if err != nil {
				cancel()
				h.failUnifiedSuccess(a, err.Error())
				return
			}
			clientReader = br
		} else {
			// Non-stream: drain the whole upstream JSON body, bridge once,
			// then expose the bridged bytes as a reader.
			upstreamBody, err := io.ReadAll(teedUpstream)
			if err != nil {
				cancel()
				h.failUnifiedSuccess(a, err.Error())
				return
			}
			_ = teedUpstream.Close()
			bridged, _, berr := h.llmBridge.BridgeNonStream(ctx, a.srcFormat, a.upFormat, upstreamBody, resp.Header, a.outboundProfile)
			if berr != nil {
				cancel()
				h.failUnifiedSuccess(a, berr.Error())
				return
			}
			clientReader = io.NopCloser(bytes.NewReader(bridged))
		}
	} else {
		clientReader = teedUpstream
	}

	// Web search emulation transformer wraps the client-facing reader so that
	// the source-format bytes the browser sees include synthesized
	// server_tool_use + web_search_tool_result blocks. Applied AFTER bridging
	// because the bridge operates on the upstream's native format.
	if a.wsCtx != nil && a.wsCtx.active {
		if streamMode {
			transformer := newWebSearchSSETransformer(ctx, clientReader, a.wsCtx, h, true)
			clientReader = newWebSearchSSELoopDriver(ctx, transformer, a.wsCtx, h, buildForwardedHeaders(r))
		} else {
			allBytes, rerr := io.ReadAll(clientReader)
			_ = clientReader.Close()
			if rerr != nil {
				cancel()
				h.failUnifiedSuccess(a, "read bridge output: "+rerr.Error())
				return
			}
			transformed, terr := h.transformWebSearchResponse(ctx, allBytes, a.wsCtx)
			if terr != nil {
				cancel()
				h.failUnifiedSuccess(a, "web search transform: "+terr.Error())
				return
			}
			transformed = h.loopWebSearchNonStream(ctx, transformed, a.wsCtx, buildForwardedHeaders(r))
			clientReader = io.NopCloser(bytes.NewReader(transformed))
		}
	}

	idleReader := newIdleTimeoutReader(clientReader, h.config.GatewayReadTimeout, cancel)
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var clientCapture bytes.Buffer
	for {
		n, readErr := idleReader.Read(buf)
		if n > 0 {
			if transforming || internalBody == resp.Body {
				w.Write(buf[:n])
			}
			clientCapture.Write(buf[:n])
			if canFlush {
				if !transforming && internalBody != resp.Body {
					responseWriter.Flush()
				} else {
					flusher.Flush()
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	cancel()
	_ = clientReader.Close()
	closeDecodedInternalResponseReader(internalBody, resp)

	upstreamBytes := upstreamCapture.Bytes()
	clientBytes := clientCapture.Bytes()
	if !transforming {
		// 1:1 path — the upstream tee may have a few bytes the client write
		// loop hasn't accumulated by the time it hits EOF; in that case
		// upstreamCapture is already authoritative for both views, so we
		// align them.
		clientBytes = upstreamBytes
	}

	upstreamAggregated := buildAggregatedArtifact(a.bgCtx, h.llmBridge, a.upFormat, upstreamCT, upstreamBytes, a.outboundProfile)
	var metaAggregated *artifacts.AggregatedResponse
	if profile, ok := defaultAggregationProfile(a.srcFormat); ok {
		metaAggregated = buildAggregatedArtifact(a.bgCtx, h.llmBridge, a.srcFormat, metaRespHeader.Get("Content-Type"), clientBytes, profile)
	}
	h.uploadResponseArtifactWithAggregation(a.bgCtx, a.upstreamID, a.upstreamCreatedAt, resp.StatusCode, resp.Header.Clone(), upstreamBytes, upstreamAggregated)
	h.uploadMetaResponseArtifactWithAggregation(a.bgCtx, a.metaID, a.metaCreatedAt, http.StatusOK, metaRespHeader, clientBytes, a.metaLogs, metaAggregated)

	m := extractor.Metrics()
	ttftMs, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens := metricsToPG(m)
	modelCost, modelCcy := h.costsFor(a.bgCtx, a.routedModel, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens)

	upstreamTimeSpent := int32(time.Since(a.attemptStart).Milliseconds())
	h.updateRequestOnComplete(a.bgCtx, db.UpdateRequestOnCompleteParams{
		ID:                 a.upstreamID,
		StatusCode:         pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
		ErrorMessage:       pgtype.Text{Valid: false},
		TimeSpentMs:        pgtype.Int4{Int32: upstreamTimeSpent, Valid: true},
		Status:             db.RequestStatusCompleted,
		TtftMs:             ttftMs,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		CacheReadTokens:    cacheReadTokens,
		CacheWriteTokens:   cacheWriteTokens,
		CacheWrite1hTokens: cacheWrite1hTokens,
		ModelCost:          modelCost,
		ModelCostCurrency:  modelCcy,
		CreatedAt:          pgtype.Timestamp{Time: a.upstreamCreatedAt, Valid: true},
	})
	metaTimeSpent := int32(time.Since(a.gatewayStart).Milliseconds())
	h.updateRequestOnComplete(a.bgCtx, db.UpdateRequestOnCompleteParams{
		ID:                 a.metaID,
		StatusCode:         pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true},
		ErrorMessage:       pgtype.Text{Valid: false},
		TimeSpentMs:        pgtype.Int4{Int32: metaTimeSpent, Valid: true},
		Status:             db.RequestStatusCompleted,
		TtftMs:             ttftMs,
		InputTokens:        inputTokens,
		OutputTokens:       outputTokens,
		CacheReadTokens:    cacheReadTokens,
		CacheWriteTokens:   cacheWriteTokens,
		CacheWrite1hTokens: cacheWrite1hTokens,
		ModelCost:          modelCost,
		ModelCostCurrency:  modelCcy,
		CreatedAt:          pgtype.Timestamp{Time: a.metaCreatedAt, Valid: true},
	})
	_ = r
}

// failUnifiedSuccess closes out a streaming/non-stream success path that
// errored after the gateway already started writing or committed to a
// candidate. We can't recover by retrying because part of the upstream may
// have been read; surface the bridge failure as 502 and complete the rows.
func (h *gatewayHandler) failUnifiedSuccess(a unifiedStreamArgs, errMsg string) {
	h.updateRequestOnComplete(a.bgCtx, db.UpdateRequestOnCompleteParams{
		ID:           a.upstreamID,
		StatusCode:   pgtype.Int4{Int32: int32(a.resp.StatusCode), Valid: true},
		ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
		TimeSpentMs:  pgtype.Int4{Int32: int32(time.Since(a.attemptStart).Milliseconds()), Valid: true},
		Status:       db.RequestStatusFailed,
		CreatedAt:    pgtype.Timestamp{Time: a.upstreamCreatedAt, Valid: true},
	})
	respBody := writeGatewayError(a.w, http.StatusBadGateway, "bridge failed: "+errMsg, errorx.UpstreamError.Error())
	h.updateRequestOnComplete(a.bgCtx, db.UpdateRequestOnCompleteParams{
		ID:           a.metaID,
		StatusCode:   pgtype.Int4{Int32: http.StatusBadGateway, Valid: true},
		ErrorMessage: pgtype.Text{String: errMsg, Valid: true},
		TimeSpentMs:  pgtype.Int4{Int32: int32(time.Since(a.gatewayStart).Milliseconds()), Valid: true},
		Status:       db.RequestStatusFailed,
		CreatedAt:    pgtype.Timestamp{Time: a.metaCreatedAt, Valid: true},
	})
	h.uploadMetaResponseArtifact(a.bgCtx, a.metaID, a.metaCreatedAt, http.StatusBadGateway, a.w.Header().Clone(), respBody, a.metaLogs)
	_ = a.resp.Body.Close()
}

// asReadCloser pairs an io.Reader (the response extractor) with the original
// ReadCloser so the chain can be Close()d cleanly when the client write loop
// finishes. Without this we'd have to hand the Close responsibility around
// across teeing/bridging layers.
func asReadCloser(r io.Reader, c io.Closer) io.ReadCloser {
	return &readerWithCloser{r: r, c: c}
}

type readerWithCloser struct {
	r io.Reader
	c io.Closer
}

func (rc *readerWithCloser) Read(p []byte) (int, error) { return rc.r.Read(p) }
func (rc *readerWithCloser) Close() error               { return rc.c.Close() }

// clientStreamContentType picks the Content-Type to send to the client when
// bridging a streaming response. SSE outputs keep the upstream's
// "text/event-stream" because we re-emit SSE in the source format. JSON
// outputs are handled in the non-stream branch.
func clientStreamContentType(src llmbridge.Format, upstreamCT string) string {
	if strings.Contains(strings.ToLower(upstreamCT), "text/event-stream") {
		return "text/event-stream"
	}
	return upstreamCT
}

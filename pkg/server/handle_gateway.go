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
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/jsx"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tidwall/sjson"
)

type gatewayHandler struct {
	*Server
}

var _ http.Handler = (*gatewayHandler)(nil)

func (h *gatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	gatewayStart := time.Now()
	bgCtx := context.Background()

	// 1. Match endpoint by path. If no endpoint matches, we don't log the
	// request at all — the request table tracks LLM gateway traffic, not
	// every miss. Browser navigations get the dashboard SPA; everything else
	// gets the structured JSON 404.
	endpoint, pathVars, err := h.resolveEndpoint(r.Context(), r.URL.Path)
	if err != nil {
		if isRouteNotFound(err) && looksLikeBrowserNav(r) {
			h.staticHandler.ServeHTTP(w, r)
			return
		}
		handleGatewayErr(w, err)
		return
	}

	// 2. Read request body
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		writeGatewayError(w, http.StatusInternalServerError, "failed to read request body", errorx.InternalError.Error())
		return
	}

	// 3. Insert meta request now that we know the endpoint matched.
	metaID, metaIDCreatedAt := newRequestID()
	metaReqHeader := r.Header.Clone()
	parentSpanID := extractParentSpanID(metaReqHeader)
	parentSpanIDPg := pgtype.Text{String: parentSpanID, Valid: parentSpanID != ""}
	metaReqMethod := r.Method
	metaReqURL := r.URL.String()
	userMessagePreview := extractUserMessagePreview(body, endpoint.EndpointType)
	// Project candidate extraction is pure (regex over body) and runs early so
	// failures in the auth phase still get logged. Resolution to a project_id
	// is deferred until after authentication, since projects are user-bound
	// and the api_key.account_id is the lookup key. projectIDPg stays invalid
	// here; we backfill it on the meta row via updateRequestProjectID once
	// resolveProjectForAccount completes.
	projectCandidates := h.extractProjectCandidates(r.Context(), body)
	var projectIDPg pgtype.Int4
	metaCreatedAt := h.insertRequest(bgCtx, db.InsertRequestParams{
		ID:                 metaID,
		SpanID:             pgtype.Text{String: metaID, Valid: true},
		ParentSpanID:       parentSpanIDPg,
		Type:               db.RequestTypeMeta,
		Status:             db.RequestStatusPending,
		ProviderID:         pgtype.Int4{Valid: false},
		EndpointPath:       pgtype.Text{String: endpoint.Path, Valid: true},
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

	h.uploadRequestArtifact(bgCtx, metaID, metaCreatedAt, metaReqMethod, metaReqURL, metaReqHeader, body)

	// session is created at step 6 but the failure-path closures below need
	// to read its log buffer. Declare here so the nil check is the same
	// before and after creation.
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

	// 4. Authenticate client. Returns the matched api_key row when the
	// supplied credentials are valid and the key is not disabled.
	apiKey, err := h.authenticateClient(r.Context(), r, endpoint.CredentialsResolver)
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
	// Backfill the meta row's api_key_id now that we have it. Other fields
	// (provider, model) get filled later in streamSuccess; here we only set
	// what we know and keep status pending.
	h.updateRequestOnHeader(bgCtx, db.UpdateRequestOnHeaderParams{
		ID:           metaID,
		EndpointPath: pgtype.Text{String: endpoint.Path, Valid: true},
		ApiKeyID:     apiKeyID,
		Status:       db.RequestStatusPending,
		CreatedAt:    pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
	})

	// Resolve project_id within the api_key's account namespace. System keys
	// (api_key.account_id IS NULL) never tag a project; the auto-create path
	// is also skipped because accountID == 0. The resolved id flows into
	// every subsequent upstream row's ProjectID and onto the meta row via
	// the backfill below.
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

	// 5. Extract model name. When endpoint.ModelPath is empty the endpoint is
	// a "no-model" endpoint: all providers bound to the path are candidates,
	// and we skip body / path-var extraction entirely.
	var modelName string
	if endpoint.ModelPath != "" {
		modelName, err = extractModel(body, endpoint.ModelPath, pathVars)
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
	}

	h.updateRequestModel(bgCtx, db.UpdateRequestModelParams{
		ID:        metaID,
		Model:     pgtype.Text{String: modelName, Valid: modelName != ""},
		CreatedAt: pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
	})

	// 6. Build jsx session up front so the rewriteModel hook can run before
	// MPE resolution. The session loads enabled scripts from the DB; if no
	// scripts are enabled this is essentially a no-op pass-through.
	session, err = h.jsxEngine.NewSession(r.Context(), metaID)
	if err != nil {
		errMsg := "failed to load js hooks: " + err.Error()
		failMeta(http.StatusBadGateway, errMsg)
		respBody := writeGatewayError(w, http.StatusBadGateway, errMsg, errorx.UpstreamError.Error())
		h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusBadGateway, w.Header().Clone(), respBody, nil)
		return
	}
	defer session.Close()

	// 6a. rewriteModel hook — once before MPE lookup. ctx is a snapshot of the
	// raw client request (modelName as extracted). If the hook returns a new
	// modelName, body.model is rewritten in lockstep so downstream hooks see
	// a consistent client-request shape.
	originalModelName := modelName
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
	if endpoint.ModelPath == "" && newModel != "" {
		failHook(errors.New("rewriteModel returned non-empty model on no-model endpoint"))
		return
	}
	if newModel != modelName {
		updated, serr := sjson.SetBytes(body, "model", newModel)
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
		// request.model tracks the routed (post-rewrite) model — the name
		// used to resolve MPE rows and match billing. Overwrite the
		// extracted value now that rewriteModel has settled.
		h.updateRequestModel(bgCtx, db.UpdateRequestModelParams{
			ID:        metaID,
			Model:     pgtype.Text{String: modelName, Valid: modelName != ""},
			CreatedAt: pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
		})
	}
	// routedModel is the post-rewrite model name: matches the `model` table
	// row that drives pricing and was used to resolve providers. All
	// subsequent request-row writes use this, not originalModelName.
	routedModel := modelName

	// 7. Resolve providers using the (possibly rewritten) modelName.
	providers, err := h.resolveProviders(r.Context(), endpoint.Path, modelName)
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

	// 8a. Build candidate list and a sidecar map for fields not exposed to JS
	// (upstream URL, credentials, merged annotations). The hooks see
	// {provider, mpe, annotations}; we look up the rest by providerID after
	// the hook returns. Refresh modelAnno from the route SQL so a single
	// snapshot drives the request — avoids drift between an earlier
	// GetModelByName read and a later route lookup.
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
		upstreamURL  string
		credentials  string
		sendResolver int32
		proxyURL     string
		annotations  map[string]string
	}
	sidecar := make(map[int32]providerSidecar, len(providers))
	candidates := make([]jsx.Candidate, 0, len(providers))
	for _, row := range providers {
		entryAnno, _ := annotations.Decode(row.EntryAnnotations)
		merged, providerAnno := annoBuilder.merge(row.ProviderAnnotations, entryAnno)
		var proxyURL string
		if row.ProxyURL.Valid {
			proxyURL = row.ProxyURL.String
		}
		sidecar[row.ProviderID] = providerSidecar{
			upstreamURL:  row.UpstreamURL,
			credentials:  row.ProviderCredentials,
			sendResolver: effectiveSendResolver(endpoint.CredentialsResolver, row.SendCredentialsResolver),
			proxyURL:     proxyURL,
			annotations:  merged,
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
				Priority:          row.EntryPriority,
				Annotations:       entryAnno,
			},
			Annotations: merged,
		})
	}

	modelJS := &jsx.ModelSummary{Name: originalModelName, Annotations: modelAnno}
	endpointJS := endpointSummaryFromRow(endpoint)

	// 8b. The JS-visible client request shape (read-only).
	jsClientRequest := serializeClientRequest(r, body, modelName, pathVars)

	// 8c. sortProviders — once before the loop.
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

	// 8d. Retry loop
	var lastErr error
	var lastJSErr *jsx.LastError
	i := 0
	currentRetryCount := 0
	totalAttemptCount := 0

	for {
		if i >= len(sortedCandidates) {
			break
		}
		if totalAttemptCount >= h.config.JSMaxTotalAttempts {
			break
		}
		cand := sortedCandidates[i]

		// Pull providerID back from the JSON-roundtripped Provider field.
		providerID := candidateProviderID(cand)
		side, hasSide := sidecar[providerID]
		if !hasSide {
			// JS introduced a provider we never saw — fail safely by skipping it.
			i++
			currentRetryCount = 0
			continue
		}

		candAnno := cand.Annotations
		if candAnno == nil {
			candAnno = side.annotations
		}
		dec, err := session.RunBeforeRequestHook(jsx.BeforeRequestInput{
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
		if err != nil {
			failHook(err)
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

		// Build upstream request.
		attemptStart := time.Now()
		ctx, cancel := context.WithCancel(r.Context())

		// Compute the model name to write into the upstream body.
		// Preference: hook-supplied upstreamModel → MPE.upstreamModelName → modelName.
		// buildUpstreamRequest still gates on non-empty, but with this fallback
		// chain we always pass it a real value.
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
			EndpointPath:       pgtype.Text{String: endpoint.Path, Valid: true},
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

		req, reqBody, berr := buildUpstreamRequest(ctx, r, body, side.upstreamURL, upstreamModel, side.credentials, side.sendResolver, pathVars)
		if berr != nil {
			h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, berr.Error())
			lastErr = berr
			lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: berr.Error()}
			currentRetryCount++
			totalAttemptCount++
			cancel()
			continue
		}

		// rewriteRequest hook. Serialize the upstream request, hand it to JS,
		// then rebuild a fresh *http.Request from whatever the hook returns —
		// no mutate-in-place, so the outgoing request is exactly the JSON
		// shape JS produced.
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

		// Upload upstream request artifact AFTER rewrite so it reflects what was sent.
		h.uploadRequestArtifact(bgCtx, upstreamID, upstreamCreatedAt, req.Method, req.URL.String(), req.Header.Clone(), reqBody)

		upstreamStartTime := time.Now()
		resp, err := h.forwardRequest(req, side.proxyURL)
		if err != nil {
			h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, 0, err.Error())
			lastErr = err
			lastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: 0, Message: err.Error()}
			currentRetryCount++
			totalAttemptCount++
			cancel()
			continue
		}

		if resp.StatusCode == http.StatusOK {
			metaLogs := collectLogs()
			h.streamSuccess(w, r, ctx, cancel, resp, upstreamID, upstreamCreatedAt, attemptStart, metaID, metaCreatedAt, gatewayStart, providerID, routedModel, upstreamModel, endpoint.EndpointType, endpoint.Path, upstreamStartTime, bgCtx, metaLogs, apiKeyID)
			return
		}

		// Non-200: record + try again.
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

	// 9. All providers failed (or attempts cap reached) — fail meta with 502.
	errMsg := "all providers failed"
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	failMeta(http.StatusBadGateway, errMsg)
	respBody := writeGatewayError(w, http.StatusBadGateway, errMsg, errorx.UpstreamError.Error())
	h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusBadGateway, w.Header().Clone(), respBody, collectLogs())
}

func mapLowerKeys(header http.Header) http.Header {
	lower := make(http.Header, len(header))
	for k, v := range header {
		lower[strings.ToLower(k)] = v
	}
	return lower
}

// uploadRequestArtifact builds and asynchronously uploads a request artifact for the given id+ts.
func (h *gatewayHandler) uploadRequestArtifact(ctx context.Context, id string, ts time.Time, method, url string, header http.Header, body []byte) {
	if !h.artifacts.Enabled() {
		return
	}
	payload, err := artifacts.BuildRequest(method, url, header, body)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", id).Warn("artifact: build request failed")
		return
	}
	h.artifacts.Put(ctx, artifacts.RequestKey(id, ts), payload)
}

// uploadResponseArtifact builds and asynchronously uploads a response artifact for the given id+ts.
func (h *gatewayHandler) uploadResponseArtifact(ctx context.Context, id string, ts time.Time, statusCode int, header http.Header, body []byte) {
	if !h.artifacts.Enabled() {
		return
	}
	payload, err := artifacts.BuildResponse(statusCode, header, body)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", id).Warn("artifact: build response failed")
		return
	}
	h.artifacts.Put(ctx, artifacts.ResponseKey(id, ts), payload)
}

func (h *gatewayHandler) uploadResponseArtifactWithAggregation(ctx context.Context, id string, ts time.Time, statusCode int, header http.Header, body []byte, aggregated *artifacts.AggregatedResponse) {
	if !h.artifacts.Enabled() {
		return
	}
	payload, err := artifacts.BuildResponseWithAggregated(statusCode, header, body, aggregated)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", id).Warn("artifact: build response failed")
		return
	}
	h.artifacts.Put(ctx, artifacts.ResponseKey(id, ts), payload)
}

// uploadMetaResponseArtifact is uploadResponseArtifact for the meta request,
// embedding any captured JSX console output. Only meta artifacts carry logs.
func (h *gatewayHandler) uploadMetaResponseArtifact(ctx context.Context, id string, ts time.Time, statusCode int, header http.Header, body []byte, logs []artifacts.LogEntry) {
	if !h.artifacts.Enabled() {
		return
	}
	payload, err := artifacts.BuildResponseWithLogs(statusCode, header, body, logs)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", id).Warn("artifact: build meta response failed")
		return
	}
	h.artifacts.Put(ctx, artifacts.ResponseKey(id, ts), payload)
}

func (h *gatewayHandler) uploadMetaResponseArtifactWithAggregation(ctx context.Context, id string, ts time.Time, statusCode int, header http.Header, body []byte, logs []artifacts.LogEntry, aggregated *artifacts.AggregatedResponse) {
	if !h.artifacts.Enabled() {
		return
	}
	payload, err := artifacts.BuildResponseWithLogsAndAggregated(statusCode, header, body, logs, aggregated)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", id).Warn("artifact: build meta response failed")
		return
	}
	h.artifacts.Put(ctx, artifacts.ResponseKey(id, ts), payload)
}

// streamSuccess writes the upstream 200 response back to the client and
// completes both the upstream and meta request rows. Pulled out of the main
// handler so the retry loop body stays scannable.
func (h *gatewayHandler) streamSuccess(
	w http.ResponseWriter, r *http.Request,
	ctx context.Context, cancel context.CancelFunc, resp *http.Response,
	upstreamID string, upstreamCreatedAt time.Time, attemptStart time.Time,
	metaID string, metaCreatedAt time.Time, gatewayStart time.Time,
	providerID int32, routedModel, upstreamModel string, endpointType int32, endpointPath string,
	upstreamStartTime time.Time,
	bgCtx context.Context,
	metaLogs []artifacts.LogEntry,
	apiKeyID pgtype.Int4,
) {
	h.updateRequestOnHeader(bgCtx, db.UpdateRequestOnHeaderParams{
		ID:            metaID,
		ProviderID:    pgtype.Int4{Int32: providerID, Valid: true},
		Model:         pgtype.Text{String: routedModel, Valid: routedModel != ""},
		UpstreamModel: pgtype.Text{String: upstreamModel, Valid: upstreamModel != ""},
		EndpointPath:  pgtype.Text{String: endpointPath, Valid: true},
		ApiKeyID:      apiKeyID,
		Status:        db.RequestStatusHeaderReceived,
		CreatedAt:     pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
	})
	h.updateRequestOnHeader(bgCtx, db.UpdateRequestOnHeaderParams{
		ID:            upstreamID,
		ProviderID:    pgtype.Int4{Int32: providerID, Valid: true},
		Model:         pgtype.Text{String: routedModel, Valid: routedModel != ""},
		UpstreamModel: pgtype.Text{String: upstreamModel, Valid: upstreamModel != ""},
		EndpointPath:  pgtype.Text{String: endpointPath, Valid: true},
		ApiKeyID:      apiKeyID,
		Status:        db.RequestStatusHeaderReceived,
		CreatedAt:     pgtype.Timestamp{Time: upstreamCreatedAt, Valid: true},
	})

	for key, values := range resp.Header {
		if strings.ToLower(key) == "content-length" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	metaRespHeader := w.Header().Clone()

	responseWriter := newLockedResponseWriter(w)
	internalReader, derr := decodedInternalResponseReader(resp, responseWriter)
	if derr != nil {
		cancel()
		h.completeFailedAttempt(bgCtx, upstreamID, upstreamCreatedAt, attemptStart, int32(resp.StatusCode), "decode upstream response: "+derr.Error())
		respBody := writeGatewayError(w, http.StatusBadGateway, "decode upstream response: "+derr.Error(), errorx.UpstreamError.Error())
		h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
			ID:           metaID,
			StatusCode:   pgtype.Int4{Int32: http.StatusBadGateway, Valid: true},
			ErrorMessage: pgtype.Text{String: "decode upstream response: " + derr.Error(), Valid: true},
			TimeSpentMs:  pgtype.Int4{Int32: int32(time.Since(gatewayStart).Milliseconds()), Valid: true},
			Status:       db.RequestStatusFailed,
			CreatedAt:    pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
		})
		h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusBadGateway, w.Header().Clone(), respBody, metaLogs)
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

	extractor := NewResponseExtractor(internalBody, resp.Header.Get("Content-Type"), upstreamStartTime)
	reader := newIdleTimeoutReader(extractor, h.config.GatewayReadTimeout, cancel)
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var captureBuf bytes.Buffer
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if internalBody == resp.Body {
				w.Write(buf[:n])
			}
			captureBuf.Write(buf[:n])
			if canFlush {
				if internalBody != resp.Body {
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
	closeDecodedInternalResponseReader(internalBody, resp)

	respBytes := captureBuf.Bytes()
	var aggregated *artifacts.AggregatedResponse
	if format, ok := responseAggregationFormat(endpointType); ok {
		if profile, ok := defaultAggregationProfile(format); ok {
			aggregated = buildAggregatedArtifact(bgCtx, h.llmBridge, format, resp.Header.Get("Content-Type"), respBytes, profile)
		}
	}
	h.uploadResponseArtifactWithAggregation(bgCtx, upstreamID, upstreamCreatedAt, resp.StatusCode, resp.Header.Clone(), respBytes, aggregated)
	h.uploadMetaResponseArtifactWithAggregation(bgCtx, metaID, metaCreatedAt, http.StatusOK, metaRespHeader, respBytes, metaLogs, aggregated)

	m := extractor.Metrics()
	ttftMs, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens := metricsToPG(m)

	modelCost, modelCcy := h.costsFor(bgCtx, routedModel, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens)

	upstreamTimeSpent := int32(time.Since(attemptStart).Milliseconds())
	h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
		ID:                 upstreamID,
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
		CreatedAt:          pgtype.Timestamp{Time: upstreamCreatedAt, Valid: true},
	})

	metaTimeSpent := int32(time.Since(gatewayStart).Milliseconds())
	h.updateRequestOnComplete(bgCtx, db.UpdateRequestOnCompleteParams{
		ID:                 metaID,
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
		CreatedAt:          pgtype.Timestamp{Time: metaCreatedAt, Valid: true},
	})
	_ = r // kept for interface symmetry; r.Context() may be useful for future hooks
}

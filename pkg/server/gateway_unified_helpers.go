package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"picotera/pkg/artifacts"
	"picotera/pkg/contract"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/llmbridge"
	"picotera/pkg/logx"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

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

// extractUnifiedModel picks the model name for the inbound request. For
// Anthropic / OpenAI the body carries it; for Gemini it lives in the chi
// {model} path variable. The streaming flag is no longer derived here — it
// comes solely from detectStreaming (five rules) in resolveAndRewriteModel.
func extractUnifiedModel(src llmbridge.Format, r *http.Request, body []byte) (string, error) {
	switch src {
	case llmbridge.FormatGeminiGenerateContent, llmbridge.FormatGeminiStreamGenerateContent:
		m := chi.URLParam(r, "model")
		if m == "" {
			return "", &gatewayError{status: http.StatusBadRequest, message: "missing {model} path variable", code: errorx.ModelNotFound.Error()}
		}
		return m, nil
	case llmbridge.FormatAnthropicMessages, llmbridge.FormatOpenAIChatCompletions, llmbridge.FormatOpenAIResponses:
		model := gjson.GetBytes(body, "model").Str
		if model == "" {
			return "", &gatewayError{status: http.StatusBadRequest, message: "model is required", code: errorx.ModelNotFound.Error()}
		}
		return model, nil
	}
	return "", &gatewayError{status: http.StatusBadRequest, message: "unsupported source format", code: errorx.InvalidRequest.Error()}
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

// unifiedUpstreamPathVars returns the path variables used to fill tokens in a
// unified upstream URL. The only token a unified upstream URL can carry is the
// {model} of a Gemini generate/streamGenerate endpoint; it must resolve to the
// upstream model name — not the inbound requested model — so that
// format-converted requests whose inbound route carries no {model} (Anthropic /
// OpenAI sources) still produce a valid Gemini URL.
func unifiedUpstreamPathVars(upstreamModel string) map[string]string {
	if upstreamModel == "" {
		return nil
	}
	return map[string]string{"model": upstreamModel}
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
	// Sort by combined priority (provider + per-model-entry) descending,
	// with higher provider IDs first when priorities tie.
	slices.SortFunc(valid, func(a, b db.GetProvidersByEndpointTypesAndModelRow) int {
		return compareCandidateOrder(
			a.ProviderID,
			a.Priority,
			a.ProviderPriority,
			b.ProviderID,
			b.Priority,
			b.ProviderPriority,
		)
	})
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
// metaEndpointPath is the unified route pattern (`/api/unified/v1/messages`,
// `/api/unified/v1beta/models/{model}:generateContent` …) — what the meta row
// should record (with the {model} placeholder, not a concrete model name).
// upstreamPath is the chosen upstream's configured endpoint.path — what the
// upstream row should record.
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
	metaLogs          []artifacts.LogEntry
	apiKeyID          pgtype.Int4
	userID            pgtype.Int8
	wsCtx             *webSearchContext
	recordBody        bool
}

func unifiedStreamArgsFromSuccess(input successInput) unifiedStreamArgs {
	return unifiedStreamArgs{
		w: input.Flow.w, r: input.Flow.r, ctx: input.AttemptCtx, cancel: input.Cancel, resp: input.Response,
		srcFormat: input.Flow.config.SourceFormat, upFormat: input.Sidecar.UpstreamFormat,
		outboundProfile: input.Prepared.OutboundProfile, upstreamID: input.UpstreamID,
		upstreamCreatedAt: input.UpstreamCreatedAt, attemptStart: input.AttemptStart,
		metaID: input.Flow.meta.ID, metaCreatedAt: input.Flow.meta.CreatedAt,
		gatewayStart: input.Flow.startedAt, providerID: input.ProviderID,
		routedModel: input.RoutedModel, upstreamModel: input.UpstreamModel,
		metaEndpointPath: input.Flow.config.Endpoint.Path, upstreamPath: input.Sidecar.EndpointPath,
		upstreamStartTime: input.UpstreamStartTime,
		metaLogs:          input.Flow.collectLogs(), apiKeyID: input.Flow.auth.APIKeyID,
		userID:     input.Flow.auth.UserID,
		wsCtx:      input.Prepared.WebSearch,
		recordBody: input.Flow.otr.recordBody(),
	}
}

// unifiedStreamSuccess is the streamSuccess analogue for unified routes. It
// runs the upstream bytes through the response extractor (so token/TTFT
// metrics still reflect the upstream's native format), captures them into
// the upstream-artifact buffer, then bridges to source format and writes the
// converted bytes to the client and the meta-artifact buffer.
//
// When src == upFormat the bridge is an identity wrapper, so this code path
// behaves exactly like streamSuccess for 1:1 cases.
func (h *gatewayHandler) unifiedStreamSuccess(input successInput) {
	a := unifiedStreamArgsFromSuccess(input)
	w, r, ctx, cancel, resp := a.w, a.r, a.ctx, a.cancel, a.resp

	// bridging => upstream format differs from source format; wsActive => web
	// search emulation rewrites the body. Either way the client sees a
	// different byte stream than the upstream, so the meta and upstream rows
	// record separately below.
	bridging := a.srcFormat != a.upFormat
	wsActive := a.wsCtx != nil && a.wsCtx.active
	transforming := bridging || wsActive

	hdrCtx, hdrCancel := input.Flow.ctxs.Persist()
	defer hdrCancel()

	// user_id / project_id are intentionally NOT touched here: they were
	// backfilled post-auth on the meta row and must survive the header update.
	h.updateRequest(hdrCtx, newRequestUpdate(a.metaID, a.metaCreatedAt).
		ProviderID(pgtype.Int4{Int32: a.providerID, Valid: true}).
		Model(pgtype.Text{String: a.routedModel, Valid: a.routedModel != ""}).
		UpstreamModel(pgtype.Text{String: a.upstreamModel, Valid: a.upstreamModel != ""}).
		EndpointPath(pgtype.Text{String: a.metaEndpointPath, Valid: a.metaEndpointPath != ""}).
		ApiKeyID(a.apiKeyID))
	h.updateRequest(hdrCtx, newRequestUpdate(a.upstreamID, a.upstreamCreatedAt).
		ProviderID(pgtype.Int4{Int32: a.providerID, Valid: true}).
		Model(pgtype.Text{String: a.routedModel, Valid: a.routedModel != ""}).
		UpstreamModel(pgtype.Text{String: a.upstreamModel, Valid: a.upstreamModel != ""}).
		EndpointPath(pgtype.Text{String: a.upstreamPath, Valid: a.upstreamPath != ""}).
		ApiKeyID(a.apiKeyID))

	// Live records, one per row and each the single source for that row's live
	// view and persisted artifact. The upstream row records the upstream-format
	// stream (fed by the tee below). On transforming routes the meta row gets
	// its own source-format record; otherwise it mirrors the upstream record.
	var upstreamProgress *liveProgress
	if input.Entry != nil {
		upstreamProgress = input.Entry.progress
	}
	if upstreamProgress == nil {
		upstreamProgress = newLiveProgressWithOrigin(a.upstreamStartTime, a.recordBody)
	}
	upstreamProgress.markHeaders(resp.StatusCode, a.upstreamStartTime)

	var metaProgress *liveProgress
	if transforming {
		metaProgress = newLiveProgressWithOrigin(a.upstreamStartTime, a.recordBody)
		metaProgress.markHeaders(resp.StatusCode, a.upstreamStartTime)
	}
	metaLive := upstreamProgress
	if metaProgress != nil {
		metaLive = metaProgress
	}
	if metaEntry, ok := h.liveRequests.get(a.metaID); ok {
		metaEntry.active.Store(metaLive)
	}

	// Forward upstream headers as-is when there's no bridge. When bridging,
	// strip Content-Type and Content-Length because the body shape changes;
	// we restore Content-Type below from the bridged side. Web-search
	// emulation also rewrites the body, so the same content-encoding stripping
	// applies — otherwise the client receives gzip headers but plaintext
	// bytes.
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
		h.failUnifiedSuccess(hdrCtx, a, "decode upstream response: "+derr.Error())
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
	// from the upstream's native response format regardless of bridging. The
	// tee mirrors those bytes into the upstream row's progress, which records
	// the body and per-line timings for both the live view and the artifact.
	extractor := NewResponseExtractor(internalBody, upstreamCT, a.upstreamStartTime)
	teedUpstream := llmbridge.NewUpstreamTee(asReadCloser(extractor, internalBody), liveProgressWriter{upstreamProgress})

	// clientReader produces the bytes we will actually write to the client
	// (and into the meta-artifact buffer). When bridging it's the bridge
	// output; otherwise it's the upstream tee directly.
	var clientReader io.ReadCloser
	if bridging {
		if streamMode {
			br, err := h.llmBridge.BridgeStream(ctx, a.srcFormat, a.upFormat, teedUpstream, upstreamCT, a.outboundProfile)
			if err != nil {
				cancel()
				h.failUnifiedSuccess(hdrCtx, a, err.Error())
				return
			}
			clientReader = br
		} else {
			// Non-stream: drain the whole upstream JSON body, bridge once,
			// then expose the bridged bytes as a reader.
			upstreamBody, err := io.ReadAll(teedUpstream)
			if err != nil {
				_ = teedUpstream.Close()
				cancel()
				h.failUnifiedSuccess(hdrCtx, a, err.Error())
				return
			}
			_ = teedUpstream.Close()
			bridged, _, berr := h.llmBridge.BridgeNonStream(ctx, a.srcFormat, a.upFormat, upstreamBody, resp.Header, a.outboundProfile)
			if berr != nil {
				cancel()
				h.failUnifiedSuccess(hdrCtx, a, berr.Error())
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
				h.failUnifiedSuccess(hdrCtx, a, "read bridge output: "+rerr.Error())
				return
			}
			transformed, terr := h.transformWebSearchResponse(ctx, allBytes, a.wsCtx)
			if terr != nil {
				cancel()
				h.failUnifiedSuccess(hdrCtx, a, "web search transform: "+terr.Error())
				return
			}
			transformed = h.loopWebSearchNonStream(ctx, transformed, a.wsCtx, buildForwardedHeaders(r))
			clientReader = io.NopCloser(bytes.NewReader(transformed))
		}
	}

	idleReader := newIdleTimeoutReader(clientReader, h.config.GatewayReadTimeout, cancel)
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	var finalReadErr error
	for {
		n, readErr := idleReader.Read(buf)
		if n > 0 {
			if transforming || internalBody == resp.Body {
				w.Write(buf[:n])
			}
			// On transforming routes the client stream (source format) is the
			// meta row's record; on identity routes the upstream tee already
			// fed both rows, so there is nothing extra to record here.
			if metaProgress != nil {
				metaProgress.recordChunk(buf[:n])
			}
			if canFlush {
				if !transforming && internalBody != resp.Body {
					responseWriter.Flush()
				} else {
					flusher.Flush()
				}
			}
		}
		if readErr != nil {
			finalReadErr = readErr
			break
		}
	}
	cancel()
	_ = clientReader.Close()
	closeDecodedInternalResponseReader(internalBody, resp)

	// Each row's artifact comes from its own live record. Identity routes share
	// the upstream record (byte-identical); transforming routes use the meta
	// record for the source-format meta artifact.
	upstreamBytes, upstreamTimings := upstreamProgress.artifactRecord()
	metaSource := upstreamProgress
	if metaProgress != nil {
		metaSource = metaProgress
	}
	clientBytes, metaTimings := metaSource.artifactRecord()

	pctx, pcancel := input.Flow.ctxs.Persist()
	defer pcancel()

	// OTR body modes move bodies + aggregation + timings out of the record. The
	// live records above already returned empty bytes/timings for this case; here
	// we additionally skip the aggregation builds.
	var upstreamAggregated *artifacts.AggregatedResponse
	var metaAggregated *artifacts.AggregatedResponse
	if a.recordBody {
		upstreamAggregated = buildAggregatedArtifact(pctx, h.llmBridge, a.upFormat, upstreamCT, upstreamBytes, a.outboundProfile)
		if profile, ok := defaultAggregationProfile(a.srcFormat); ok {
			metaAggregated = buildAggregatedArtifact(pctx, h.llmBridge, a.srcFormat, metaRespHeader.Get("Content-Type"), clientBytes, profile)
		}
	}
	h.uploadResponseArtifactWithAggregation(pctx, a.upstreamID, a.upstreamCreatedAt, resp.StatusCode, resp.Header.Clone(), upstreamBytes, upstreamAggregated, upstreamTimings)
	h.uploadMetaResponseArtifactWithAggregation(pctx, a.metaID, a.metaCreatedAt, http.StatusOK, metaRespHeader, clientBytes, a.metaLogs, metaAggregated, metaTimings)
	finishReason := classifyStreamFinishReason(finalReadErr, r.Context())

	m := extractor.Metrics()
	ttftMs, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens := metricsToPG(m)
	modelCost, modelCcy := h.costsFor(pctx, a.routedModel, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens)

	// An in-stream error event (HTTP 200 with an error.message payload) marks
	// both rows failed while keeping the real upstream status code and metrics.
	// The extractor wraps the upstream's native bytes, so the error is detected
	// in the upstream format (the true source of the failure).
	streamErr := extractor.StreamError()
	errMsg := pgtype.Text{Valid: false}
	fr := finishReason
	if streamErr != "" {
		errMsg = pgtype.Text{String: streamErr, Valid: true}
		fr = int32(db.FinishReasonStreamError)
		input.Flow.runStreamErrorHook(a.providerID, input.CurrentRetryCount, input.TotalAttemptCount, resp.StatusCode, streamErr)
	}

	upstreamFr := input.Flow.finishReasonFor(a.upstreamID, fr)
	metaFr := input.Flow.finishReasonFor(a.metaID, fr)
	upstreamTimeSpent := int32(time.Since(a.attemptStart).Milliseconds())
	h.updateRequest(pctx, newRequestUpdate(a.upstreamID, a.upstreamCreatedAt).
		StatusCode(pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true}).
		ErrorMessage(errMsg).
		TimeSpentMs(pgtype.Int4{Int32: upstreamTimeSpent, Valid: true}).
		TtftMs(ttftMs).
		InputTokens(inputTokens).
		OutputTokens(outputTokens).
		CacheReadTokens(cacheReadTokens).
		CacheWriteTokens(cacheWriteTokens).
		CacheWrite1hTokens(cacheWrite1hTokens).
		ModelCost(modelCost).
		ModelCostCurrency(modelCcy).
		FinishReason(pgtype.Int4{Int32: upstreamFr, Valid: true}).
		InferredProvider(pgtype.Text{String: m.InferredProvider, Valid: m.InferredProvider != ""}).
		InferredModel(pgtype.Text{String: m.InferredModel, Valid: m.InferredModel != ""}).
		InferredModelSource(int16(m.InferredModelSource)).
		ExternalResponseID(matchExternalIDHeader(resp.Header, h.externalResponseIDHeaders)))
	metaTimeSpent := int32(time.Since(a.gatewayStart).Milliseconds())
	h.updateRequest(pctx, newRequestUpdate(a.metaID, a.metaCreatedAt).
		StatusCode(pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true}).
		ErrorMessage(errMsg).
		TimeSpentMs(pgtype.Int4{Int32: metaTimeSpent, Valid: true}).
		TtftMs(ttftMs).
		InputTokens(inputTokens).
		OutputTokens(outputTokens).
		CacheReadTokens(cacheReadTokens).
		CacheWriteTokens(cacheWriteTokens).
		CacheWrite1hTokens(cacheWrite1hTokens).
		ModelCost(modelCost).
		ModelCostCurrency(modelCcy).
		FinishReason(pgtype.Int4{Int32: metaFr, Valid: true}).
		InferredProvider(pgtype.Text{String: m.InferredProvider, Valid: m.InferredProvider != ""}).
		InferredModel(pgtype.Text{String: m.InferredModel, Valid: m.InferredModel != ""}).
		InferredModelSource(int16(m.InferredModelSource)).
		ExternalResponseID(matchExternalIDHeader(resp.Header, h.externalResponseIDHeaders)))
	_ = r
}

// failUnifiedSuccess closes out a streaming/non-stream success path that
// errored after the gateway already started writing or committed to a
// candidate. We can't recover by retrying because part of the upstream may
// have been read; surface the bridge failure as 502 and complete the rows.
func (h *gatewayHandler) failUnifiedSuccess(ctx context.Context, a unifiedStreamArgs, errMsg string) {
	h.updateRequest(ctx, newRequestUpdate(a.upstreamID, a.upstreamCreatedAt).
		StatusCode(pgtype.Int4{Int32: int32(a.resp.StatusCode), Valid: true}).
		ErrorMessage(pgtype.Text{String: errMsg, Valid: true}).
		TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(a.attemptStart).Milliseconds()), Valid: true}).
		FinishReason(pgtype.Int4{Int32: db.FinishReasonInternal, Valid: true}).
		ExternalResponseID(matchExternalIDHeader(a.resp.Header, h.externalResponseIDHeaders)))
	respBody := writeGatewayError(a.w, http.StatusBadGateway, "bridge failed: "+errMsg, errorx.UpstreamError.Error())
	h.updateRequest(ctx, newRequestUpdate(a.metaID, a.metaCreatedAt).
		StatusCode(pgtype.Int4{Int32: http.StatusBadGateway, Valid: true}).
		ErrorMessage(pgtype.Text{String: errMsg, Valid: true}).
		TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(a.gatewayStart).Milliseconds()), Valid: true}).
		FinishReason(pgtype.Int4{Int32: db.FinishReasonInternal, Valid: true}).
		ExternalResponseID(matchExternalIDHeader(a.resp.Header, h.externalResponseIDHeaders)))
	artifactBody := respBody
	if !a.recordBody {
		artifactBody = nil
	}
	h.uploadMetaResponseArtifact(ctx, a.metaID, a.metaCreatedAt, http.StatusBadGateway, a.w.Header().Clone(), artifactBody, a.metaLogs, nil)
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

package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"picotera/pkg/artifacts"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5/pgtype"
)

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
func (h *gatewayHandler) uploadResponseArtifact(ctx context.Context, id string, ts time.Time, statusCode int, header http.Header, body []byte, timings []float64) {
	if !h.artifacts.Enabled() {
		return
	}
	header = redactResponseHeaders(header)
	payload, err := artifacts.BuildResponse(statusCode, header, body, timings)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", id).Warn("artifact: build response failed")
		return
	}
	h.artifacts.Put(ctx, artifacts.ResponseKey(id, ts), payload)
}

func (h *gatewayHandler) uploadResponseArtifactWithAggregation(ctx context.Context, id string, ts time.Time, statusCode int, header http.Header, body []byte, aggregated *artifacts.AggregatedResponse, timings []float64) {
	if !h.artifacts.Enabled() {
		return
	}
	header = redactResponseHeaders(header)
	payload, err := artifacts.BuildResponseWithAggregated(statusCode, header, body, aggregated, timings)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", id).Warn("artifact: build response failed")
		return
	}
	h.artifacts.Put(ctx, artifacts.ResponseKey(id, ts), payload)
}

// uploadMetaResponseArtifact is uploadResponseArtifact for the meta request,
// embedding any captured JSX console output. Only meta artifacts carry logs.
func (h *gatewayHandler) uploadMetaResponseArtifact(ctx context.Context, id string, ts time.Time, statusCode int, header http.Header, body []byte, logs []artifacts.LogEntry, timings []float64) {
	if !h.artifacts.Enabled() {
		return
	}
	header = redactResponseHeaders(header)
	payload, err := artifacts.BuildResponseWithLogs(statusCode, header, body, logs, timings)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", id).Warn("artifact: build meta response failed")
		return
	}
	h.artifacts.Put(ctx, artifacts.ResponseKey(id, ts), payload)
}

func (h *gatewayHandler) uploadMetaResponseArtifactWithAggregation(ctx context.Context, id string, ts time.Time, statusCode int, header http.Header, body []byte, logs []artifacts.LogEntry, aggregated *artifacts.AggregatedResponse, timings []float64) {
	if !h.artifacts.Enabled() {
		return
	}
	header = redactResponseHeaders(header)
	payload, err := artifacts.BuildResponseWithLogsAndAggregated(statusCode, header, body, logs, aggregated, timings)
	if err != nil {
		logx.WithContext(ctx).WithError(err).WithField("id", id).Warn("artifact: build meta response failed")
		return
	}
	h.artifacts.Put(ctx, artifacts.ResponseKey(id, ts), payload)
}

// streamSuccess writes the upstream 200 response back to the client and
// completes both the upstream and meta request rows. Pulled out of the main
// handler so the retry loop body stays scannable.
func (h *gatewayHandler) streamSuccess(input successInput) {
	h.markPathHeadersReceived(input)
	copyPathSuccessHeaders(input.Flow.w, input.Response)
	metaRespHeader := input.Flow.w.Header().Clone()
	responseWriter, internalReader, ok := h.openPathInternalReader(input)
	if !ok {
		return
	}
	extractor, progress, finishReason := h.pipePathResponse(input, responseWriter, internalReader)
	respBytes, timings := progress.artifactRecord()
	h.aggregatePathResponse(input, metaRespHeader, respBytes, timings)
	h.completeGatewaySuccess(input, extractor.Metrics(), input.Response.StatusCode, finishReason, extractor.StreamError())
	_ = input.Flow.r
}

func (h *gatewayHandler) markPathHeadersReceived(input successInput) {
	metaID, metaCreatedAt := input.Flow.meta.ID, input.Flow.meta.CreatedAt
	endpointPath := input.Flow.config.Endpoint.Path
	if input.Entry != nil && input.Entry.progress != nil {
		input.Entry.progress.markHeaders(input.Response.StatusCode, input.UpstreamStartTime)
		if metaEntry, ok := h.liveRequests.get(metaID); ok {
			metaEntry.active.Store(input.Entry.progress)
		}
	}
	bgCtx, cancel := input.Flow.ctxs.Persist()
	defer cancel()
	apiKeyID := input.Flow.auth.APIKeyID
	// user_id / project_id are intentionally NOT touched here: they were
	// backfilled post-auth on the meta row and must survive the header update.
	// The header update only sets provider/model/endpoint/status.
	h.updateRequest(bgCtx, newRequestUpdate(metaID, metaCreatedAt).
		ProviderID(pgtype.Int4{Int32: input.ProviderID, Valid: true}).
		Model(pgtype.Text{String: input.RoutedModel, Valid: input.RoutedModel != ""}).
		UpstreamModel(pgtype.Text{String: input.UpstreamModel, Valid: input.UpstreamModel != ""}).
		EndpointPath(pgtype.Text{String: endpointPath, Valid: true}).
		ApiKeyID(apiKeyID))
	h.updateRequest(bgCtx, newRequestUpdate(input.UpstreamID, input.UpstreamCreatedAt).
		ProviderID(pgtype.Int4{Int32: input.ProviderID, Valid: true}).
		Model(pgtype.Text{String: input.RoutedModel, Valid: input.RoutedModel != ""}).
		UpstreamModel(pgtype.Text{String: input.UpstreamModel, Valid: input.UpstreamModel != ""}).
		EndpointPath(pgtype.Text{String: endpointPath, Valid: true}).
		ApiKeyID(apiKeyID))
}

func copyPathSuccessHeaders(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		lower := strings.ToLower(key)
		if lower == "content-length" || shouldStripUpstreamHeader(lower) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

func (h *gatewayHandler) openPathInternalReader(input successInput) (*lockedResponseWriter, *internalResponseReader, bool) {
	w, resp := input.Flow.w, input.Response
	responseWriter := newLockedResponseWriter(w)
	internalReader, derr := decodedInternalResponseReader(resp, responseWriter)
	if derr != nil {
		input.Cancel()
		bgCtx, cancel := input.Flow.ctxs.Persist()
		defer cancel()
		metaID, metaCreatedAt := input.Flow.meta.ID, input.Flow.meta.CreatedAt
		h.completeFailedAttemptWithReason(bgCtx, input.UpstreamID, input.UpstreamCreatedAt, input.AttemptStart, int32(resp.StatusCode), "decode upstream response: "+derr.Error(), db.FinishReasonInternal, resp.Header)
		respBody := writeGatewayError(w, http.StatusBadGateway, "decode upstream response: "+derr.Error(), errorx.UpstreamError.Error())
		h.updateRequest(bgCtx, newRequestUpdate(metaID, metaCreatedAt).
			StatusCode(pgtype.Int4{Int32: http.StatusBadGateway, Valid: true}).
			ErrorMessage(pgtype.Text{String: "decode upstream response: " + derr.Error(), Valid: true}).
			TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(input.Flow.startedAt).Milliseconds()), Valid: true}).
			FinishReason(pgtype.Int4{Int32: db.FinishReasonInternal, Valid: true}).
			ExternalResponseID(matchExternalIDHeader(resp.Header, h.externalResponseIDHeaders)))
		h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusBadGateway, w.Header().Clone(), input.Flow.artifactBody(respBody), input.Flow.collectLogs(), nil)
		_ = resp.Body.Close()
		return nil, nil, false
	}
	internalBody := internalReader.Body
	w.WriteHeader(http.StatusOK)
	if err := internalReader.StartClientWrite(); err != nil {
		input.Cancel()
		bgCtx, cancel := input.Flow.ctxs.Persist()
		defer cancel()
		metaID, metaCreatedAt := input.Flow.meta.ID, input.Flow.meta.CreatedAt
		errMsg := "start client write: " + err.Error()
		h.completeFailedAttemptWithReason(bgCtx, input.UpstreamID, input.UpstreamCreatedAt, input.AttemptStart, http.StatusOK, errMsg, db.FinishReasonCancelled, resp.Header)
		h.updateRequest(bgCtx, newRequestUpdate(metaID, metaCreatedAt).
			StatusCode(pgtype.Int4{Int32: http.StatusOK, Valid: true}).
			ErrorMessage(pgtype.Text{String: errMsg, Valid: true}).
			TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(input.Flow.startedAt).Milliseconds()), Valid: true}).
			FinishReason(pgtype.Int4{Int32: db.FinishReasonCancelled, Valid: true}).
			ExternalResponseID(matchExternalIDHeader(resp.Header, h.externalResponseIDHeaders)))
		h.uploadMetaResponseArtifact(bgCtx, metaID, metaCreatedAt, http.StatusOK, w.Header().Clone(), input.Flow.artifactBody(nil), input.Flow.collectLogs(), nil)
		closeDecodedInternalResponseReader(internalBody, resp)
		return nil, nil, false
	}
	return responseWriter, internalReader, true
}

// pipePathResponse streams the upstream body to the client while recording it
// into the upstream row's liveProgress, which is the single source for both the
// live view and the persisted artifact (path routes are always identity, so the
// same bytes/timings feed the meta artifact too). Returns the progress so the
// caller can take the final artifact snapshot.
func (h *gatewayHandler) pipePathResponse(input successInput, responseWriter *lockedResponseWriter, internalReader *internalResponseReader) (*ResponseExtractor, *liveProgress, int32) {
	w, resp := input.Flow.w, input.Response
	internalBody := internalReader.Body
	extractor := NewResponseExtractor(internalBody, resp.Header.Get("Content-Type"), input.UpstreamStartTime)
	reader := newIdleTimeoutReader(extractor, h.config.GatewayReadTimeout, input.Cancel)
	buf := make([]byte, 32*1024)
	var finalReadErr error
	// progress is guaranteed by RegisterUpstream on the success path; the
	// fallback keeps artifact capture working if it is ever absent.
	var progress *liveProgress
	if input.Entry != nil {
		progress = input.Entry.progress
	}
	if progress == nil {
		progress = newLiveProgressWithOrigin(input.UpstreamStartTime, input.Flow.otr.recordBody())
	}
	flusher, canFlush := w.(http.Flusher)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if internalBody == resp.Body {
				w.Write(buf[:n])
			}
			progress.recordChunk(buf[:n])
			if canFlush {
				if internalBody != resp.Body {
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
	input.Cancel()
	closeDecodedInternalResponseReader(internalBody, resp)
	return extractor, progress, classifyStreamFinishReason(finalReadErr, input.Flow.ctxs.Request, extractor.StreamCompleted())
}

func (h *gatewayHandler) aggregatePathResponse(input successInput, metaRespHeader http.Header, respBytes []byte, timings []float64) {
	pctx, pcancel := input.Flow.ctxs.Persist()
	defer pcancel()
	var aggregated *artifacts.AggregatedResponse
	// OTR body modes move bodies + aggregation + timings out of the record;
	// respBytes/timings are already empty (gated in liveProgress), so we just
	// skip the aggregation build here.
	if input.Flow.otr.recordBody() {
		if format, ok := responseAggregationFormat(input.Flow.config.Endpoint.EndpointType); ok {
			if profile, ok := defaultAggregationProfile(format); ok {
				aggregated = buildAggregatedArtifact(pctx, h.llmBridge, format, input.Response.Header.Get("Content-Type"), respBytes, profile)
			}
		}
	}
	h.uploadResponseArtifactWithAggregation(pctx, input.UpstreamID, input.UpstreamCreatedAt, input.Response.StatusCode, input.Response.Header.Clone(), respBytes, aggregated, timings)
	h.uploadMetaResponseArtifactWithAggregation(pctx, input.Flow.meta.ID, input.Flow.meta.CreatedAt, http.StatusOK, metaRespHeader, respBytes, input.Flow.collectLogs(), aggregated, timings)
}

func (h *gatewayHandler) completeGatewaySuccess(input successInput, m ResponseMetrics, statusCode int, finishReason int32, streamErr string) {
	bgCtx, cancel := input.Flow.ctxs.Persist()
	defer cancel()
	ttftMs, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens := metricsToPG(m)
	modelCost, modelCcy := h.costsFor(bgCtx, input.RoutedModel, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens)

	// An in-stream error event (HTTP 200 with an error.message payload) marks
	// both rows failed while keeping the real upstream status code and metrics.
	errMsg := pgtype.Text{Valid: false}
	fr := finishReason
	if streamErr != "" {
		errMsg = pgtype.Text{String: streamErr, Valid: true}
		fr = int32(db.FinishReasonStreamError)
		input.Flow.runStreamErrorHook(input.ProviderID, input.CurrentRetryCount, input.TotalAttemptCount, statusCode, streamErr)
	}

	upstreamFr := input.Flow.finishReasonFor(input.UpstreamID, fr)
	metaFr := input.Flow.finishReasonFor(input.Flow.meta.ID, fr)
	upstreamTimeSpent := int32(time.Since(input.AttemptStart).Milliseconds())
	h.updateRequest(bgCtx, newRequestUpdate(input.UpstreamID, input.UpstreamCreatedAt).
		StatusCode(pgtype.Int4{Int32: int32(statusCode), Valid: true}).
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
		ExternalResponseID(matchExternalIDHeader(input.Response.Header, h.externalResponseIDHeaders)))
	metaTimeSpent := int32(time.Since(input.Flow.startedAt).Milliseconds())
	h.updateRequest(bgCtx, newRequestUpdate(input.Flow.meta.ID, input.Flow.meta.CreatedAt).
		StatusCode(pgtype.Int4{Int32: int32(statusCode), Valid: true}).
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
		ExternalResponseID(matchExternalIDHeader(input.Response.Header, h.externalResponseIDHeaders)))
}

// classifyStreamFinishReason derives the finish reason from how the upstream
// read loop ended. streamCompleted (ResponseExtractor.StreamCompleted) reports
// whether the upstream already emitted the stream's terminating event: many
// clients close the connection the moment they see it, which cancels the
// request context — that is a normal EOF, not a cancellation. The idle-timeout
// check stays ahead of the cancel check, so an upstream that emits the
// terminating event but never closes the connection is still a read timeout.
func classifyStreamFinishReason(readErr error, reqCtx context.Context, streamCompleted bool) int32 {
	if errors.Is(readErr, io.EOF) {
		return db.FinishReasonEOF
	}
	if errors.Is(readErr, errReadIdleTimeout) {
		return db.FinishReasonReadTimeout
	}
	if reqCtx.Err() != nil && !streamCompleted {
		return db.FinishReasonCancelled
	}
	return db.FinishReasonEOF
}

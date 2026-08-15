package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"picotera/pkg/db"
	"picotera/pkg/jsx"
	"picotera/pkg/llmbridge"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5/pgtype"
)

type attemptInput struct {
	Candidate         jsx.CandidateView
	Sidecar           gatewayCandidateSidecar
	Annotations       map[string]string
	Decision          jsx.BeforeRequestDecision
	CurrentRetryCount int
	TotalAttemptCount int
	AttemptCtx        context.Context
	UpstreamID        string
	UpstreamCreatedAt time.Time
	AttemptStart      time.Time
	UpstreamModel     string
	Request           *http.Request
	RequestBody       []byte
	Entry             *liveEntry
}

type attemptPrepared struct {
	Request         *http.Request
	RequestBody     []byte
	OutboundProfile llmbridge.OutboundProfile
	WebSearch       *webSearchContext
}

type successInput struct {
	Flow              *gatewayFlow
	Candidate         jsx.CandidateView
	Sidecar           gatewayCandidateSidecar
	Response          *http.Response
	AttemptCtx        context.Context
	Cancel            context.CancelFunc
	UpstreamID        string
	UpstreamCreatedAt time.Time
	AttemptStart      time.Time
	UpstreamStartTime time.Time
	ProviderID        int32
	RoutedModel       string
	UpstreamModel     string
	Prepared          attemptPrepared
	Entry             *liveEntry
	CurrentRetryCount int
	TotalAttemptCount int
}

type attemptResult struct {
	Handled bool
	LastErr error
}

func (f *gatewayFlow) runAttempts(sorted []jsx.CandidateView, sidecars map[string]gatewayCandidateSidecar) attemptResult {
	state := attemptState{}
	for state.Index < len(sorted) && state.TotalAttemptCount < f.h.config.JSMaxTotalAttempts {
		// The flow context is cancelled when the meta row is interrupted from
		// the dashboard; stop trying further providers.
		if f.ctxs.Request.Err() != nil {
			break
		}
		cand := sorted[state.Index]
		side, ok := lookupCandidateSidecar(f.config.Kind, sidecars, cand)
		if !ok {
			state.Index++
			state.CurrentRetryCount = 0
			continue
		}
		handled, stop := f.runSingleAttempt(cand, side, &state)
		if handled || stop {
			return attemptResult{Handled: handled, LastErr: state.LastErr}
		}
	}
	return attemptResult{LastErr: state.LastErr}
}

type attemptState struct {
	Index             int
	CurrentRetryCount int
	TotalAttemptCount int
	LastErr           error
	LastJSErr         *jsx.LastError
}

func (f *gatewayFlow) runSingleAttempt(cand jsx.CandidateView, side gatewayCandidateSidecar, state *attemptState) (handled bool, stop bool) {
	candAnno := cand.Annotations
	if candAnno == nil {
		candAnno = side.Annotations
	}
	// Reset ctx.upstreamRequest to null: beforeRequest runs before this attempt's
	// upstream row exists, so a hook must not see the previous attempt's identity.
	// It is filled in right after the row is inserted below.
	if err := f.session.SetUpstreamRequest(nil); err != nil {
		f.failHook(err)
		return false, true
	}
	dec, err := f.runBeforeRequest(cand, candAnno, state)
	if err != nil {
		f.failHook(err)
		return false, true
	}
	if !f.waitHookDelay(dec.Delay) {
		state.LastErr = f.ctxs.Request.Err()
		return false, true
	}
	if dec.Next {
		logx.WithContext(f.ctxs.Request).WithField("provider_id", side.ProviderID).Debug("hook not continuing, trying next upstream")
		state.Index++
		state.CurrentRetryCount = 0
		return false, false
	}
	logx.WithContext(f.ctxs.Request).WithField("provider_id", side.ProviderID).Debug("starting upstream attempt")
	input, cancel, err := f.insertUpstreamAttempt(cand, side, candAnno, dec, state)
	if err != nil {
		f.recordAttemptFailure(state, input, side.ProviderID, 0, err, db.FinishReasonInternal)
		cancel()
		if hookDec, brk := f.runAfterUpstreamError(state, false); brk {
			f.respondUpstreamErrorBreak(hookDec, 0, []byte(err.Error()), nil)
			return true, true
		}
		return false, false
	}
	defer f.h.liveRequests.Remove(input.UpstreamID)
	// The upstream row now exists, so expose its identity: rewriteRequest,
	// afterUpstreamError and the in-stream error hook can annotate this attempt
	// via picotera.request.setAnnotation(ctx.upstreamRequest.id, ...).
	if err := f.session.SetUpstreamRequest(f.requestRef(input.UpstreamID)); err != nil {
		f.recordAttemptFailure(state, input, side.ProviderID, int32(gatewayHookStatus(err)), err, db.FinishReasonInternal)
		f.failHook(err)
		cancel()
		return true, true
	}
	prepared, err := f.buildRewrittenUpstreamRequest(input)
	if err != nil {
		var hookErr gatewayHookError
		if errors.As(err, &hookErr) {
			f.recordAttemptFailure(state, input, side.ProviderID, int32(gatewayHookStatus(hookErr.err)), hookErr.err, db.FinishReasonInternal)
			f.failHook(hookErr.err)
			cancel()
			return true, true
		}
		f.recordAttemptFailure(state, input, side.ProviderID, 0, err, db.FinishReasonInternal)
		cancel()
		if hookDec, brk := f.runAfterUpstreamError(state, false); brk {
			f.respondUpstreamErrorBreak(hookDec, 0, []byte(err.Error()), nil)
			return true, true
		}
		return false, false
	}
	reqArtifactCtx, reqArtifactCancel := f.ctxs.Persist()
	redactedHeader, redactedURL := redactRequestCredentials(prepared.Request.Header.Clone(), prepared.Request.URL.String())
	f.h.uploadRequestArtifact(reqArtifactCtx, input.UpstreamID, input.UpstreamCreatedAt, prepared.Request.Method, redactedURL, redactedHeader, f.artifactBody(prepared.RequestBody))
	reqArtifactCancel()
	upstreamStart := time.Now()
	resp, err := f.h.forwardRequest(prepared.Request, side.transport(), f.model.Mode.Streaming)
	if err != nil {
		f.recordAttemptFailure(state, input, side.ProviderID, 0, err, f.finishReasonFor(input.UpstreamID, classifyForwardError(err, f.ctxs.Request)))
		cancel()
		if hookDec, brk := f.runAfterUpstreamError(state, false); brk {
			f.respondUpstreamErrorBreak(hookDec, 0, []byte(err.Error()), nil)
			return true, true
		}
		return false, false
	}
	if resp.StatusCode == http.StatusOK {
		f.config.HandleSuccess(successInput{Flow: f, Candidate: cand, Sidecar: side, Response: resp, AttemptCtx: input.AttemptCtx, Cancel: cancel, UpstreamID: input.UpstreamID, UpstreamCreatedAt: input.UpstreamCreatedAt, AttemptStart: input.AttemptStart, UpstreamStartTime: upstreamStart, ProviderID: side.ProviderID, RoutedModel: f.model.Routed, UpstreamModel: input.UpstreamModel, Prepared: prepared, Entry: input.Entry, CurrentRetryCount: input.CurrentRetryCount, TotalAttemptCount: input.TotalAttemptCount})
		return true, false
	}
	if f.handleUpstreamNonOK(state, input, resp, side.ProviderID) {
		cancel()
		return true, true
	}
	cancel()
	return false, false
}

// runBeforeRequest patches the per-attempt ctx fields (provider, providerModel,
// annotations, attempt) and runs the beforeRequest waterfall. The initial
// decision pre-seeds next=true for retries (currentRetryCount > 0).
func (f *gatewayFlow) runBeforeRequest(cand jsx.CandidateView, candAnno map[string]string, state *attemptState) (jsx.BeforeRequestDecision, error) {
	attempt := jsx.AttemptState{
		CurrentRetryCount: state.CurrentRetryCount,
		TotalAttemptCount: state.TotalAttemptCount,
		LastError:         state.LastJSErr,
	}
	if err := f.session.PatchContext(jsx.ContextPatch{
		Provider:      &cand.Provider,
		ProviderModel: &cand.ProviderModel,
		Annotations:   &candAnno,
		Attempt:       &attempt,
	}); err != nil {
		return jsx.BeforeRequestDecision{}, err
	}
	return f.session.RunBeforeRequest(jsx.BeforeRequestDecision{Next: state.CurrentRetryCount > 0})
}

func (f *gatewayFlow) waitHookDelay(delayMs int) bool {
	if delayMs <= 0 {
		return true
	}
	delay := time.Duration(delayMs) * time.Millisecond
	if f.h.config.JSMaxDelay > 0 && delay > f.h.config.JSMaxDelay {
		delay = f.h.config.JSMaxDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-f.ctxs.Request.Done():
		return false
	}
}

func (f *gatewayFlow) insertUpstreamAttempt(cand jsx.CandidateView, side gatewayCandidateSidecar, candAnno map[string]string, dec jsx.BeforeRequestDecision, state *attemptState) (attemptInput, context.CancelFunc, error) {
	attemptCtx, cancel := context.WithCancel(f.ctxs.Request)
	upstreamModel := dec.UpstreamModel
	if upstreamModel == "" {
		upstreamModel = candidateUpstreamModel(cand)
	}
	if upstreamModel == "" {
		upstreamModel = f.model.Routed
	}
	upstreamID, upstreamIDCreatedAt := newRequestID()
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	upstreamCreatedAt := f.h.insertRequest(pctx, db.InsertRequestParams{
		ID:                 upstreamID,
		SpanID:             pgtype.Text{String: f.meta.ID, Valid: true},
		ParentSpanID:       f.meta.ParentSpanIDPg,
		Type:               db.RequestTypeUpstream,
		ProviderID:         pgtype.Int4{Int32: side.ProviderID, Valid: true},
		EndpointPath:       pgtype.Text{String: side.EndpointPath, Valid: side.EndpointPath != ""},
		ApiKeyID:           f.auth.APIKeyID,
		Model:              pgtype.Text{String: f.model.Routed, Valid: f.model.Routed != ""},
		UpstreamModel:      pgtype.Text{String: upstreamModel, Valid: upstreamModel != ""},
		StatusCode:         pgtype.Int4{Valid: false},
		ErrorMessage:       pgtype.Text{Valid: false},
		TimeSpentMs:        pgtype.Int4{Valid: false},
		UserMessagePreview: pgtype.Text{Valid: false},
		ProjectID:          f.meta.ProjectID,
		CreatedAt:          pgtype.Timestamp{Time: upstreamIDCreatedAt, Valid: true},
		UserID:             f.auth.UserID,
		ExternalRequestID:  pgtype.Text{Valid: false},
		ExternalResponseID: pgtype.Text{Valid: false},
	})
	entry := f.h.liveRequests.RegisterUpstream(upstreamID, cancel, f.otr.recordBody())
	return attemptInput{Candidate: cand, Sidecar: side, Annotations: candAnno, Decision: dec, CurrentRetryCount: state.CurrentRetryCount, TotalAttemptCount: state.TotalAttemptCount, AttemptCtx: attemptCtx, UpstreamID: upstreamID, UpstreamCreatedAt: upstreamCreatedAt, AttemptStart: time.Now(), UpstreamModel: upstreamModel, Entry: entry}, cancel, nil
}

func (f *gatewayFlow) buildRewrittenUpstreamRequest(input attemptInput) (attemptPrepared, error) {
	body := f.body
	upstreamModel := input.UpstreamModel
	pathVars := f.config.PathVars
	if f.config.Kind == gatewayRouteUnified {
		upstreamModel = ""
		// The upstream URL's {model} token (Gemini endpoints) must carry the
		// resolved upstream model name, not the inbound chi params — which are
		// empty for non-Gemini source routes that get bridged to Gemini.
		pathVars = unifiedUpstreamPathVars(input.UpstreamModel)
		var err error
		body, err = setUnifiedModel(f.config.SourceFormat, f.body, input.UpstreamModel)
		if err != nil {
			return attemptPrepared{}, err
		}
	}
	authHeaderName := ""
	if f.h.config.Auth.HeaderEnabled {
		authHeaderName = f.h.config.Auth.HeaderName
	}
	req, reqBody, err := buildUpstreamRequest(input.AttemptCtx, f.r, body, input.Sidecar.UpstreamURL, upstreamModel, input.Sidecar.Credentials, input.Sidecar.SendResolver, pathVars, authHeaderName)
	if err != nil {
		return attemptPrepared{}, err
	}
	// Web-search rewriting / beforeTransform / llmbridge conversion run BEFORE
	// the rewriteRequest hook, so the hook sees (and mutates) the body in the
	// upstream's format — a hook keyed on the upstream endpoint would otherwise
	// write fields that the cross-format bridge silently drops.
	input.Request = req
	input.RequestBody = reqBody
	prepared, err := f.config.PrepareAttempt(input.AttemptCtx, f, input)
	if err != nil {
		return attemptPrepared{}, err
	}
	req, reqBody = prepared.Request, prepared.RequestBody
	// The body is now in the upstream's format (after any llmbridge conversion),
	// so ctx.format reflects the format of the request about to be sent.
	outFormat := input.Sidecar.UpstreamFormat.String()
	if err := f.session.PatchContext(jsx.ContextPatch{Format: &outFormat}); err != nil {
		return attemptPrepared{}, gatewayHookError{err: err}
	}
	pending := serializePendingRequest(req)
	newPending, err := f.session.RunRewriteRequest(pending, []byte(jsonBodyOrNil(req.Header, reqBody)))
	if err != nil {
		return attemptPrepared{}, gatewayHookError{err: err}
	}
	req, reqBody, err = buildRequestFromPending(input.AttemptCtx, newPending, reqBody)
	if err != nil {
		return attemptPrepared{}, gatewayHookError{err: err}
	}
	prepared.Request = req
	prepared.RequestBody = reqBody
	return prepared, nil
}

// handleUpstreamNonOK records a non-200 upstream response as a failed attempt
// and runs the afterUpstreamError hook. It returns true when the hook decided to
// break — in which case the downstream response has already been written and the
// caller must stop trying further providers.
func (f *gatewayFlow) handleUpstreamNonOK(state *attemptState, input attemptInput, resp *http.Response, providerID int32) bool {
	decoded, err := decodedBody(resp)
	if err != nil {
		_ = resp.Body.Close()
		f.recordAttemptFailure(state, input, providerID, int32(resp.StatusCode), fmt.Errorf("decode upstream response: %w", err), db.FinishReasonInternal)
		if hookDec, brk := f.runAfterUpstreamError(state, false); brk {
			f.respondUpstreamErrorBreak(hookDec, resp.StatusCode, []byte(err.Error()), nil)
			return true
		}
		return false
	}
	respBody, err := io.ReadAll(decoded.Body)
	_ = decoded.Body.Close()
	if err != nil {
		f.recordAttemptFailure(state, input, providerID, int32(resp.StatusCode), fmt.Errorf("decode upstream response: %w", err), db.FinishReasonInternal)
		if hookDec, brk := f.runAfterUpstreamError(state, false); brk {
			f.respondUpstreamErrorBreak(hookDec, resp.StatusCode, []byte(err.Error()), nil)
			return true
		}
		return false
	}
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.uploadResponseArtifact(pctx, input.UpstreamID, input.UpstreamCreatedAt, resp.StatusCode, resp.Header.Clone(), f.artifactBody(respBody), nil)
	errMsg := string(respBody)
	f.h.updateRequest(pctx, newRequestUpdate(input.UpstreamID, input.UpstreamCreatedAt).
		StatusCode(pgtype.Int4{Int32: int32(resp.StatusCode), Valid: true}).
		ErrorMessage(pgtype.Text{String: errMsg, Valid: true}).
		TimeSpentMs(pgtype.Int4{Int32: int32(time.Since(input.AttemptStart).Milliseconds()), Valid: true}).
		FinishReason(pgtype.Int4{Int32: db.FinishReasonInternal, Valid: true}).
		ExternalResponseID(matchExternalIDHeader(resp.Header, f.h.externalResponseIDHeaders)))
	updateAttemptState(state, providerID, resp.StatusCode, errMsg, fmt.Errorf("upstream returned %d: %s", resp.StatusCode, errMsg))
	if hookDec, brk := f.runAfterUpstreamError(state, false); brk {
		f.respondUpstreamErrorBreak(hookDec, resp.StatusCode, respBody, resp.Header)
		return true
	}
	return false
}

// finishReasonFor resolves the finish reason for a request row, preferring a
// dashboard-interrupt stop reason recorded for the row itself, then falling
// back to the meta row's stop reason (so an upstream cascaded-cancelled by a
// meta interrupt reports the same reason), then to the supplied fallback.
func (f *gatewayFlow) finishReasonFor(rowID string, fallback int32) int32 {
	if r := f.h.liveRequests.StopReason(rowID); r != 0 {
		return r
	}
	if r := f.h.liveRequests.StopReason(f.meta.ID); r != 0 {
		return r
	}
	return fallback
}

func (f *gatewayFlow) recordAttemptFailure(state *attemptState, input attemptInput, providerID int32, statusCode int32, err error, finishReason int32) {
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.completeFailedAttemptWithReason(pctx, input.UpstreamID, input.UpstreamCreatedAt, input.AttemptStart, statusCode, err.Error(), finishReason, nil)
	updateAttemptState(state, providerID, int(statusCode), err.Error(), err)
}

// runAfterUpstreamError patches the per-attempt ctx (counts + lastError, which
// updateAttemptState must have already written into state.LastJSErr) and runs
// the afterUpstreamError waterfall. It returns the decision plus whether the
// caller should break now (decision.Break && !streamed). A hook failure is
// advisory: it is logged and treated as break=false.
func (f *gatewayFlow) runAfterUpstreamError(state *attemptState, streamed bool) (jsx.AfterUpstreamErrorDecision, bool) {
	var statusCode int
	var message string
	if state.LastJSErr != nil {
		statusCode = state.LastJSErr.StatusCode
		message = state.LastJSErr.Message
	}
	if err := f.session.PatchContext(jsx.ContextPatch{Attempt: &jsx.AttemptState{
		CurrentRetryCount: state.CurrentRetryCount,
		TotalAttemptCount: state.TotalAttemptCount,
		LastError:         state.LastJSErr,
	}}); err != nil {
		logx.WithContext(f.ctxs.Request).WithError(err).Warn("afterUpstreamError patch context failed")
		return jsx.AfterUpstreamErrorDecision{}, false
	}
	// Seed break=true for a bare 400 upstream error that hasn't streamed yet, so
	// the default is to pass that response through; the hook can read this default
	// in input.break and return { break: false } to keep trying other providers.
	defaultBreak := statusCode == http.StatusBadRequest && !streamed
	dec, err := f.session.RunAfterUpstreamError(jsx.UpstreamErrorView{Break: defaultBreak, StatusCode: statusCode, Message: message, Streamed: streamed})
	if err != nil {
		logx.WithContext(f.ctxs.Request).WithError(err).Warn("afterUpstreamError hook failed")
		return jsx.AfterUpstreamErrorDecision{}, false
	}
	return dec, dec.Break && !streamed
}

// respondUpstreamErrorBreak writes the downstream response for an
// afterUpstreamError break and finalizes the meta row. dec.Message overrides the
// body (application/json); otherwise the upstream's original body + Content-Type
// are passed through. dec.StatusCode overrides the status; otherwise the
// upstream's original status, falling back to 502.
func (f *gatewayFlow) respondUpstreamErrorBreak(dec jsx.AfterUpstreamErrorDecision, origStatus int, origBody []byte, origHeader http.Header) {
	status := dec.StatusCode
	if status <= 0 {
		status = origStatus
	}
	if status <= 0 {
		status = http.StatusBadGateway
	}
	var body []byte
	contentType := "application/json"
	if dec.Message != "" {
		body = []byte(dec.Message)
	} else {
		body = origBody
		if origHeader != nil {
			if ct := origHeader.Get("Content-Type"); ct != "" {
				contentType = ct
			}
		}
	}
	f.w.Header().Set("Content-Type", contentType)
	f.w.WriteHeader(status)
	_, _ = f.w.Write(body)
	errMsg := dec.Message
	if errMsg == "" {
		errMsg = string(origBody)
	}
	f.failMeta(int32(status), errMsg, db.FinishReasonInternal, origHeader)
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.uploadMetaResponseArtifact(pctx, f.meta.ID, f.meta.CreatedAt, status, f.w.Header().Clone(), f.artifactBody(body), f.collectLogs(), nil)
}

// runStreamErrorHook runs the afterUpstreamError waterfall for an in-stream
// error (HTTP 200 + an SSE error payload). The response already streamed to the
// client, so streamed=true and the hook's break is ignored — it runs purely for
// observation (logging, kv writes). Unlike runAfterUpstreamError it does not
// depend on an attemptState; the success path has no state, so the caller passes
// the counts and error explicitly.
func (f *gatewayFlow) runStreamErrorHook(providerID int32, currentRetry, totalAttempt, statusCode int, message string) {
	lastErr := &jsx.LastError{ProviderID: int(providerID), StatusCode: statusCode, Message: message}
	if err := f.session.PatchContext(jsx.ContextPatch{Attempt: &jsx.AttemptState{
		CurrentRetryCount: currentRetry,
		TotalAttemptCount: totalAttempt,
		LastError:         lastErr,
	}}); err != nil {
		logx.WithContext(f.ctxs.Request).WithError(err).Warn("afterUpstreamError patch context failed")
		return
	}
	if _, err := f.session.RunAfterUpstreamError(jsx.UpstreamErrorView{StatusCode: statusCode, Message: message, Streamed: true}); err != nil {
		logx.WithContext(f.ctxs.Request).WithError(err).Warn("afterUpstreamError hook failed")
	}
}

func updateAttemptState(state *attemptState, providerID int32, statusCode int, message string, err error) {
	state.LastErr = err
	state.LastJSErr = &jsx.LastError{ProviderID: int(providerID), StatusCode: statusCode, Message: message}
	state.CurrentRetryCount++
	state.TotalAttemptCount++
}

func identityPrepareAttempt(_ context.Context, _ *gatewayFlow, input attemptInput) (attemptPrepared, error) {
	return attemptPrepared{Request: input.Request, RequestBody: input.RequestBody}, nil
}

func prepareUnifiedAttempt(ctx context.Context, f *gatewayFlow, input attemptInput) (attemptPrepared, error) {
	req, reqBody := input.Request, input.RequestBody
	wsCtx, err := prepareUnifiedWebSearch(f, input, reqBody)
	if err != nil {
		return attemptPrepared{}, err
	}
	if wsCtx != nil && wsCtx.active {
		reqBody = wsCtx.rewrittenBody
		resetRequestBody(req, reqBody)
	}
	profile, err := prepareUnifiedOutboundProfile(f, input)
	if err != nil {
		return attemptPrepared{}, err
	}
	req, reqBody, err = bridgeUnifiedRequest(ctx, f, input, req, reqBody, profile)
	if err != nil {
		return attemptPrepared{}, err
	}
	return attemptPrepared{Request: req, RequestBody: reqBody, OutboundProfile: profile, WebSearch: wsCtx}, nil
}

func prepareUnifiedWebSearch(f *gatewayFlow, input attemptInput, reqBody []byte) (*webSearchContext, error) {
	if f.config.SourceFormat != llmbridge.FormatAnthropicMessages || input.Sidecar.SupportsNativeWebSearch || !hasWebSearchTool(reqBody) {
		return nil, nil
	}
	rewrote, err := rewriteWebSearchTools(reqBody)
	if err != nil {
		return nil, err
	}
	rewrote, err = rewriteWebSearchHistory(rewrote)
	if err != nil {
		return nil, err
	}
	return &webSearchContext{active: true, apiKeyToken: f.auth.APIKey.Key, metaID: f.meta.ID, metaCreatedAt: f.meta.CreatedAt, parentSpanID: f.meta.ParentSpanID, originalRequestBody: append([]byte(nil), f.preRewriteBody...), rewrittenBody: rewrote}, nil
}

func prepareUnifiedOutboundProfile(f *gatewayFlow, input attemptInput) (llmbridge.OutboundProfile, error) {
	base, err := llmbridge.DefaultOutboundProfileForFormat(input.Sidecar.UpstreamFormat)
	if err != nil {
		return llmbridge.OutboundProfile{}, gatewayHookError{err: err}
	}
	// ctx already carries stream / sourceFormat / providerModel.upstreamFormat
	// (patched before the attempt), so the waterfall value is just the profile.
	hookProfile, err := f.session.RunBeforeTransform(jsx.OutboundProfile{Type: base.Type, Config: map[string]any{}})
	if err != nil {
		return llmbridge.OutboundProfile{}, err
	}
	out := llmbridge.OutboundProfile{Type: hookProfile.Type, Config: hookProfile.Config}
	if out.Config == nil {
		out.Config = map[string]any{}
	}
	return out, nil
}

func bridgeUnifiedRequest(ctx context.Context, f *gatewayFlow, input attemptInput, req *http.Request, reqBody []byte, profile llmbridge.OutboundProfile) (*http.Request, []byte, error) {
	if input.Sidecar.UpstreamFormat == f.config.SourceFormat {
		return req, reqBody, nil
	}
	if !f.h.llmBridge.Enabled() {
		return nil, nil, fmt.Errorf("llmbridge: plugin is not configured")
	}
	bridgeURL := req.URL.String()
	if f.config.SourceFormat == llmbridge.FormatGeminiGenerateContent || f.config.SourceFormat == llmbridge.FormatGeminiStreamGenerateContent {
		bridgeURL = llmbridge.SyntheticGeminiPath(f.config.SourceFormat, f.model.Original)
	}
	upBody, upCT, err := f.h.llmBridge.BridgeRequest(ctx, f.config.SourceFormat, input.Sidecar.UpstreamFormat, reqBody, req.Header, bridgeURL, profile)
	if err != nil {
		return nil, nil, err
	}
	resetRequestBody(req, upBody)
	req.Header.Set("Content-Type", upCT)
	// 桥接到 Gemini streamGenerateContent 时,客户端(非 Gemini 源)不会携带
	// alt=sse,Gemini 默认返回 JSON 数组流而非 SSE。强制写入 alt=sse,使上游返回
	// text/event-stream,供 BridgeStream 正确解析。在 rewriteRequest 钩子前注入,
	// 脚本可改写或删除。
	if input.Sidecar.UpstreamFormat == llmbridge.FormatGeminiStreamGenerateContent {
		q := req.URL.Query()
		q.Set("alt", "sse")
		req.URL.RawQuery = q.Encode()
	}
	return req, upBody, nil
}

func resetRequestBody(req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
}

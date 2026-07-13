package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"picotera/pkg/annotations"
	"picotera/pkg/db"
	"picotera/pkg/errorx"
	"picotera/pkg/jsx"
	"picotera/pkg/llmbridge"
	"picotera/pkg/logx"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/tidwall/gjson"
)

type gatewayRouteKind int

const (
	gatewayRoutePath gatewayRouteKind = iota
	gatewayRouteUnified
)

type gatewayFlow struct {
	h              *gatewayHandler
	w              http.ResponseWriter
	r              *http.Request
	startedAt      time.Time
	ctxs           gatewayContexts
	config         gatewayFlowConfig
	body           []byte
	preRewriteBody []byte
	meta           gatewayMetaState
	auth           gatewayAuthState
	model          gatewayModelState
	session        jsx.Session
	// headerOTR carries the X-PicoTera-OTR override parsed pre-auth in run();
	// headerOTRSet reports whether the header was present and valid. otr is the
	// effective mode computed post-auth (header override, else user setting).
	headerOTR    otrMode
	headerOTRSet bool
	otr          otrMode
}

type gatewayFlowConfig struct {
	Kind              gatewayRouteKind
	Endpoint          db.Endpoint
	PathVars          map[string]string
	SourceFormat      llmbridge.Format
	ExtractModel      func(*http.Request, []byte, map[string]string) (gatewayModelMode, error)
	SetBodyModel      func([]byte, string) ([]byte, error)
	ResolveCandidates func(context.Context, gatewayModelMode, gatewayAuthState) (candidateSet, error)
	PrepareAttempt    func(context.Context, *gatewayFlow, attemptInput) (attemptPrepared, error)
	HandleSuccess     func(successInput)
}

type gatewayMetaState struct {
	ID             string
	CreatedAt      time.Time
	ParentSpanID   string
	ParentSpanIDPg pgtype.Text
	ProjectID      pgtype.Int4
	RequestHeader  http.Header
	RequestMethod  string
	RequestURL     string
}

type gatewayAuthState struct {
	APIKey     *db.ApiKey
	APIKeyID   pgtype.Int4
	UserID     pgtype.Int8
	APIKeyJS   *jsx.ApiKeySummary
	APIKeyAnno map[string]string
	UserJS     *jsx.UserSummary
	UserAnno   map[string]string
}

type gatewayModelState struct {
	Mode        gatewayModelMode
	Original    string
	Routed      string
	Annotations map[string]string
}

type gatewayModelMode struct {
	OriginalModel string
	RoutedModel   string
	// Streaming is the five-rule detection of whether the client expects a
	// streaming response (see detectStreaming), filled in resolveAndRewriteModel.
	// It drives candidateEndpointTypes' upstream-variant selection, the
	// upstream header-timeout decision, and the beforeTransform hook's Stream
	// input — all from this single source.
	Streaming bool
	HasModel  bool
}

func newGatewayFlow(h *gatewayHandler, w http.ResponseWriter, r *http.Request, startedAt time.Time, cfg gatewayFlowConfig) *gatewayFlow {
	return &gatewayFlow{
		h:         h,
		w:         w,
		r:         r,
		startedAt: startedAt,
		config:    cfg,
	}
}

// detectStreaming reports whether the client expects a streaming response,
// applying five rules in order (any match => true):
//  1. the source format is the Gemini stream endpoint;
//  2. the request body has `stream: true`;
//  3. the Accept header contains text/event-stream;
//  4. the Accept header contains application/x-ndjson;
//  5. otherwise false.
//
// Accept matching is case-insensitive across all Accept header values.
func detectStreaming(srcFormat llmbridge.Format, r *http.Request, body []byte) bool {
	if srcFormat == llmbridge.FormatGeminiStreamGenerateContent {
		return true
	}
	if gjson.GetBytes(body, "stream").Bool() {
		return true
	}
	for _, accept := range r.Header.Values("Accept") {
		lower := strings.ToLower(accept)
		if strings.Contains(lower, "text/event-stream") || strings.Contains(lower, "application/x-ndjson") {
			return true
		}
	}
	return false
}

func (f *gatewayFlow) run() {
	f.ctxs = newGatewayContexts(f.r)
	defer f.ctxs.cancelBase()
	defer f.ctxs.cancelRequest()
	if !f.readBody() {
		return
	}
	if !f.parseHeaderOTR() {
		return
	}
	if !f.insertMetaRequest() {
		return
	}
	defer f.h.liveRequests.Remove(f.meta.ID)
	if !f.authenticateAndBackfill() {
		return
	}
	defer (func() {
		if f.session != nil {
			// Persist the meta-row annotations a hook accumulated over the session.
			// Reading them never touches the VM, so this is safe even if the session
			// was tainted by a hook timeout. This defer runs on every path after the
			// session is created (success, failures, stream interruption, both
			// gateway and unified routes).
			f.persistRequestAnnotations(f.meta.ID, f.meta.CreatedAt, f.session.MetaAnnotations())
			f.session.Close()
		}
	})()
	if !f.resolveAndRewriteModel() {
		return
	}
	sorted, sidecars, ok := f.resolveAndSortCandidates()
	if !ok {
		return
	}
	result := f.runAttempts(sorted, sidecars)
	if result.Handled {
		return
	}
	f.failAllProviders(result.LastErr)
}

func (f *gatewayFlow) readBody() bool {
	body, err := io.ReadAll(f.r.Body)
	_ = f.r.Body.Close()
	if err != nil {
		writeGatewayError(f.w, http.StatusInternalServerError, "failed to read request body", errorx.InternalError.Error())
		return false
	}
	f.body = body
	return true
}

// parseHeaderOTR validates the X-PicoTera-OTR override before any meta row is
// created. A present-but-invalid value is rejected with 400 (no meta row); a
// valid value is stashed on the flow and applied post-auth; absence falls
// through to the user setting.
func (f *gatewayFlow) parseHeaderOTR() bool {
	v := f.r.Header.Get(otrHeaderName)
	if v == "" {
		return true
	}
	mode, ok := parseOTRValue(v)
	if !ok {
		writeGatewayError(f.w, http.StatusBadRequest, "invalid "+otrHeaderName+" header", errorx.InvalidRequest.Error())
		return false
	}
	f.headerOTR = mode
	f.headerOTRSet = true
	return true
}

// artifactBody returns b unchanged when the effective OTR mode records bodies,
// otherwise nil so the artifact keeps headers/status but no body.
func (f *gatewayFlow) artifactBody(b []byte) []byte {
	if f.otr.recordBody() {
		return b
	}
	return nil
}

func (f *gatewayFlow) insertMetaRequest() bool {
	metaID, metaIDCreatedAt := newRequestID()
	header := f.r.Header.Clone()
	parentSpanID := extractParentSpanID(header)
	parentSpanIDPg := pgtype.Text{String: parentSpanID, Valid: parentSpanID != ""}
	// Project identification happens post-auth (in authenticateAndBackfill), once
	// the user is known — projects are per-user. The meta row starts with no
	// project; project_id is backfilled post-auth in authenticateAndBackfill.
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	createdAt := f.h.insertRequest(pctx, db.InsertRequestParams{
		ID:            metaID,
		SpanID:        pgtype.Text{String: metaID, Valid: true},
		ParentSpanID:  parentSpanIDPg,
		Type:          db.RequestTypeMeta,
		ProviderID:    pgtype.Int4{Valid: false},
		EndpointPath:  pgtype.Text{String: f.config.Endpoint.Path, Valid: true},
		ApiKeyID:      pgtype.Int4{Valid: false},
		Model:         pgtype.Text{Valid: false},
		UpstreamModel: pgtype.Text{Valid: false},
		StatusCode:    pgtype.Int4{Valid: false},
		ErrorMessage:  pgtype.Text{Valid: false},
		TimeSpentMs:   pgtype.Int4{Valid: false},
		// user_message_preview depends on the OTR mode, which is only known
		// post-auth; it is backfilled in authenticateAndBackfill.
		UserMessagePreview: pgtype.Text{Valid: false},
		ProjectID:          pgtype.Int4{Valid: false},
		CreatedAt:          pgtype.Timestamp{Time: metaIDCreatedAt, Valid: true},
		// User is unknown until authentication; the trace is created (with the
		// real user_id) in authenticateAndBackfill, so insertRequest's upsertTrace
		// is skipped for the meta row.
		UserID:             pgtype.Int8{Valid: false},
		ExternalRequestID:  matchExternalIDHeader(f.r.Header, f.h.externalRequestIDHeaders),
		ExternalResponseID: pgtype.Text{Valid: false},
	})
	f.meta = gatewayMetaState{
		ID:             metaID,
		CreatedAt:      createdAt,
		ParentSpanID:   parentSpanID,
		ParentSpanIDPg: parentSpanIDPg,
		ProjectID:      pgtype.Int4{Valid: false},
		RequestHeader:  header,
		RequestMethod:  f.r.Method,
		RequestURL:     f.r.URL.String(),
	}
	f.h.liveRequests.RegisterMeta(metaID, f.ctxs.cancelRequest)
	// The request artifact and user_message_preview both depend on the OTR mode
	// (taken from the user setting), which is only known post-auth — see
	// authenticateAndBackfill.
	return true
}

func (f *gatewayFlow) authenticateAndBackfill() bool {
	apiKey, user, err := f.h.authenticateClient(f.ctxs.Request, f.r)
	if err != nil {
		var gwErr *gatewayError
		if errors.As(err, &gwErr) {
			f.failMeta(int32(gwErr.status), gwErr.message, db.FinishReasonInternal, nil)
		} else {
			f.failMeta(http.StatusInternalServerError, "auth validation failed", db.FinishReasonInternal, nil)
		}
		f.failGatewayError(err)
		return false
	}
	apiKeyJS := apiKeySummaryFromRow(apiKey)
	userJS := userSummaryFromRow(user)
	f.auth = gatewayAuthState{
		APIKey:     apiKey,
		APIKeyID:   pgtype.Int4{Int32: apiKey.ID, Valid: true},
		UserID:     pgtype.Int8{Int64: apiKey.UserID, Valid: true},
		APIKeyJS:   apiKeyJS,
		APIKeyAnno: apiKeyJS.Annotations,
		UserJS:     userJS,
		UserAnno:   userJS.Annotations,
	}
	// The effective OTR mode is now resolvable: a valid request header overrides
	// the user's default setting. It gates the request artifact and preview below
	// and is read again at every later recording point.
	if f.headerOTRSet {
		f.otr = f.headerOTR
	} else {
		f.otr = f.h.otrSetting(f.ctxs.Request, apiKey.UserID)
	}
	// Project identification is scoped to the authenticated user, so it runs here
	// rather than at meta-insert time. The result is backfilled onto the meta row
	// alongside user_id; upstream attempt rows read it from f.meta.ProjectID.
	projectIDPg := f.h.extractProjectID(f.ctxs.Request, f.body, apiKey.UserID)
	f.meta.ProjectID = projectIDPg
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.updateRequest(pctx, newRequestUpdate(f.meta.ID, f.meta.CreatedAt).
		ApiKeyID(f.auth.APIKeyID).
		UserID(f.auth.UserID).
		ProjectID(projectIDPg))
	// Backfill user_message_preview unless the OTR mode moves it out of the
	// record. Only the preview column is set, so it cannot clobber the fields
	// written above.
	if f.otr.recordPreview() {
		if preview := extractUserMessagePreview(f.body, f.config.Endpoint.EndpointType); preview.Valid {
			f.h.updateRequest(pctx, newRequestUpdate(f.meta.ID, f.meta.CreatedAt).
				UserMessagePreview(preview))
		}
	}
	// Upload the request artifact now (deferred from insertMetaRequest); the body
	// is cleared when the OTR mode moves bodies out of the record.
	redactedHeader, redactedURL := redactRequestCredentials(f.meta.RequestHeader.Clone(), f.meta.RequestURL)
	f.h.uploadRequestArtifact(pctx, f.meta.ID, f.meta.CreatedAt, f.meta.RequestMethod, redactedURL, redactedHeader, f.artifactBody(f.body))
	// The trace is created now (post-auth, user known) anchored to the meta
	// row's created_at, so ListRequestTraces' time-window LATERALs still match
	// the meta row. Subsequent upstream rows extend the window via upsertTrace.
	f.h.upsertTrace(pctx, f.meta.ParentSpanIDPg, f.auth.UserID, f.meta.CreatedAt)
	if projectIDPg.Valid {
		seenCtx, seenCancel := f.ctxs.Persist()
		go func() {
			defer seenCancel()
			f.h.upsertProjectSeen(seenCtx, projectIDPg.Int32, f.meta.CreatedAt)
		}()
	}
	return true
}

func (f *gatewayFlow) resolveAndRewriteModel() bool {
	mode, err := f.config.ExtractModel(f.r, f.body, f.config.PathVars)
	if err != nil {
		f.failGatewayErrorWithFallback(err, http.StatusBadRequest, "model extraction failed")
		return false
	}
	mode.RoutedModel = mode.OriginalModel
	mode.Streaming = detectStreaming(f.config.SourceFormat, f.r, f.body)
	f.model = gatewayModelState{Mode: mode, Original: mode.OriginalModel, Routed: mode.RoutedModel}
	f.updateMetaModel(mode.RoutedModel)
	session, err := f.h.jsxEngine.NewSession(f.ctxs.Request, f.meta.ID)
	if err != nil {
		f.failInternal(http.StatusBadGateway, "failed to load js hooks: "+err.Error(), errorx.UpstreamError.Error())
		return false
	}
	f.session = session
	f.model.Annotations = f.h.fetchModelAnnotations(f.ctxs.Request, mode.RoutedModel)
	f.preRewriteBody = append([]byte(nil), f.body...)

	endpointType := "gateway"
	if f.config.Kind == gatewayRouteUnified {
		endpointType = "unified"
	}
	epSummary := endpointSummaryFromRow(f.config.Endpoint)
	clientReq := serializeClientRequest(f.r, mode.RoutedModel, f.config.PathVars)
	mergedAnno := annotations.Merge(f.model.Annotations, f.auth.UserAnno, f.auth.APIKeyAnno)
	streaming := mode.Streaming
	srcFormat := f.config.SourceFormat.String()
	if err := f.session.PatchContext(jsx.ContextPatch{
		EndpointType: &endpointType,
		Endpoint:     &epSummary,
		RequestModel: &mode.OriginalModel,
		Request:      &clientReq,
		ApiKey:       f.auth.APIKeyJS,
		User:         f.auth.UserJS,
		Annotations:  &mergedAnno,
		Stream:       &streaming,
		SourceFormat: &srcFormat,
	}); err != nil {
		f.failHook(err)
		return false
	}
	if err := f.session.SetClientBody([]byte(jsonBodyOrNil(f.r.Header, f.body))); err != nil {
		f.failHook(err)
		return false
	}

	newModel, err := f.session.RunRewriteModel(mode.RoutedModel)
	if err != nil {
		f.failHook(err)
		return false
	}
	if !mode.HasModel && newModel != "" {
		f.failHook(errors.New("rewriteModel returned non-empty model on no-model endpoint"))
		return false
	}
	if newModel != mode.RoutedModel {
		updated, serr := f.config.SetBodyModel(f.body, newModel)
		if serr != nil {
			f.failInternal(http.StatusInternalServerError, "failed to set model in body: "+serr.Error(), errorx.InternalError.Error())
			return false
		}
		f.body = updated
		mode.RoutedModel = newModel
		f.model.Routed = newModel
		f.model.Mode = mode
		f.model.Annotations = f.h.fetchModelAnnotations(f.ctxs.Request, newModel)
		f.updateMetaModel(newModel)
	}

	// Reflect the final routed model (and, if the body/annotations changed, the
	// updated request/annotations) onto the persistent ctx.
	routed := jsx.ModelSummary{Name: f.model.Routed, Annotations: f.model.Annotations}
	finalReq := serializeClientRequest(f.r, f.model.Routed, f.config.PathVars)
	finalAnno := annotations.Merge(f.model.Annotations, f.auth.UserAnno, f.auth.APIKeyAnno)
	if err := f.session.PatchContext(jsx.ContextPatch{
		RoutedModel: &routed,
		Request:     &finalReq,
		Annotations: &finalAnno,
	}); err != nil {
		f.failHook(err)
		return false
	}
	// The model rewrite may have changed the body bytes; re-register so the
	// ctx.request.body Proxy reflects the final body (and any prior Proxy is
	// invalidated).
	if err := f.session.SetClientBody([]byte(jsonBodyOrNil(f.r.Header, f.body))); err != nil {
		f.failHook(err)
		return false
	}
	return true
}

func (f *gatewayFlow) resolveAndSortCandidates() ([]jsx.CandidateView, map[string]gatewayCandidateSidecar, bool) {
	set, err := f.config.ResolveCandidates(f.ctxs.Request, f.model.Mode, f.auth)
	if err != nil {
		f.failGatewayErrorWithFallback(err, http.StatusInternalServerError, "failed to query providers")
		return nil, nil, false
	}
	if set.ModelAnno != nil {
		f.model.Annotations = set.ModelAnno
	}
	candidates := make([]jsx.CandidateView, 0, len(set.Items))
	for _, item := range set.Items {
		candidates = append(candidates, item.Candidate)
	}
	// Candidate resolution may have refined the model annotations; reflect the
	// final routedModel + merged annotations onto ctx before sortProviders.
	routed := jsx.ModelSummary{Name: f.model.Routed, Annotations: f.model.Annotations}
	mergedAnno := annotations.Merge(f.model.Annotations, f.auth.UserAnno, f.auth.APIKeyAnno)
	if err := f.session.PatchContext(jsx.ContextPatch{
		RoutedModel: &routed,
		Annotations: &mergedAnno,
	}); err != nil {
		f.failHook(err)
		return nil, nil, false
	}
	sorted, err := f.session.RunSortProviders(candidates)
	if err != nil {
		f.failHook(err)
		return nil, nil, false
	}
	return sorted, candidateSidecarMap(set), true
}

// persistRequestAnnotations writes the script-supplied annotations onto a request
// row (meta or upstream) with a single partial UPDATE. Empty annotations are a
// no-op (the column stays NULL, keeping the partial GIN index untouched). Not
// gated by OTR: annotations are operator-authored operational labels, not client
// content. Following the recording convention, marshal/update failures are logged
// and never affect the response.
func (f *gatewayFlow) persistRequestAnnotations(id string, createdAt time.Time, anno map[string]string) {
	if len(anno) == 0 {
		return
	}
	b, err := json.Marshal(anno)
	if err != nil {
		logx.WithContext(f.ctxs.Request).WithError(err).Warn("failed to marshal request annotations")
		return
	}
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.updateRequest(pctx, newRequestUpdate(id, createdAt).Annotations(b))
}

func (f *gatewayFlow) updateMetaModel(model string) {
	pctx, pcancel := f.ctxs.Persist()
	defer pcancel()
	f.h.updateRequest(pctx, newRequestUpdate(f.meta.ID, f.meta.CreatedAt).
		Model(pgtype.Text{String: model, Valid: model != ""}))
}

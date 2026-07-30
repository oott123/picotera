package jsx

import "context"

// Engine constructs per-request JS sessions. The in-process implementation is
// backed by QuickJS; the interface leaves room for a future go-plugin (gRPC)
// implementation that the callers can hold without code changes.
type Engine interface {
	// NewSession creates a per-request JS session. The caller MUST call Close().
	NewSession(ctx context.Context, requestID string) (Session, error)
	Config() Config
}

// Session is a single meta-request's JS context. All method parameters and
// return values are JSON-serializable so a gRPC plugin can pass JSON strings.
//
// The persistent globalThis.ctx is shared for the whole session; PatchContext
// shallow-merges determined fields onto it as the request flow progresses.
// Each Run* method evaluates the corresponding waterfall against globalThis.ctx
// and the supplied initial value, returning the (possibly rewritten) value.
type Session interface {
	// PatchContext shallow-merges patch's non-nil fields onto globalThis.ctx,
	// preserving custom fields the scripts attached.
	PatchContext(patch ContextPatch) error

	// SetClientBody installs (or replaces) the JS-visible client request body
	// backing ctx.request.body. body is the raw bytes (nil = no JS-visible
	// body); they are parsed lazily on first script access. Calling it again
	// (e.g. after rewriteModel changed the body) invalidates any Proxy handed
	// out for the previous body.
	SetClientBody(body []byte) error

	RunRewriteModel(initial string) (string, error)
	RunSortProviders(initial []CandidateView) ([]CandidateView, error)
	RunBeforeRequest(initial BeforeRequestDecision) (BeforeRequestDecision, error)
	// RunRewriteRequest runs the rewriteRequest waterfall. body is the raw
	// upstream body bytes the hook may read/mutate via pending.body (nil = no
	// JS-visible body). The returned shape's Body carries the final upstream
	// bytes, or nil to fall back to the caller's pre-hook bytes.
	RunRewriteRequest(initial PendingRequestShape, body []byte) (PendingRequestShape, error)
	RunBeforeTransform(initial OutboundProfile) (OutboundProfile, error)
	RunRewriteProviderModels(initial []ProviderModelEntry) ([]ProviderModelEntry, error)
	// RunAfterUpstreamError runs the afterUpstreamError waterfall after an
	// upstream attempt failed. Passthrough keeps the initial value (break=false).
	RunAfterUpstreamError(initial UpstreamErrorView) (AfterUpstreamErrorDecision, error)

	// SetUpstreamRequest installs ctx.upstreamRequest for the current attempt.
	// ref == nil sets it to null (the state before an upstream row exists).
	SetUpstreamRequest(ref *RequestRef) error

	// RunRequestFinished runs the requestFinished waterfall after the meta row's
	// finish reason landed. It is purely observational: the waterfall's result is
	// discarded and only an evaluation error is returned.
	RunRequestFinished(input RequestFinishedView) error

	Logs() []LogEntry
	Close()
}

// HostAPI is the host capability surface the JS SDK calls into for
// configuration/telemetry writes that outlive a single hook value: annotation
// writes keyed by row id and lookups of provider / api-key configuration. The
// jsx package defines the interface; pkg/server implements it over db.Querier.
//
// A nil value means "delete this annotation key". The Get* methods return
// (nil, nil) when the id does not exist.
type HostAPI interface {
	SetRequestAnnotation(ctx context.Context, requestID, key string, value *string) error
	SetProviderAnnotation(ctx context.Context, providerID int32, key string, value *string) error
	SetApiKeyAnnotation(ctx context.Context, apiKeyID int32, key string, value *string) error
	GetProvider(ctx context.Context, providerID int32) (*ProviderSummary, error)
	GetApiKey(ctx context.Context, apiKeyID int32) (*ApiKeySummary, error)
}

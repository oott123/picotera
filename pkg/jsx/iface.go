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

	// MetaAnnotations returns a snapshot of the string KV pairs a hook wrote to
	// ctx.metaRequest.annotations over the session. Persisted once to the meta
	// row at request end. nil when none were written.
	MetaAnnotations() map[string]string
	// UpstreamAnnotations returns a snapshot of the current attempt's
	// ctx.upstreamRequest.annotations. nil when none were written.
	UpstreamAnnotations() map[string]string
	// ResetUpstreamAnnotations clears the upstream annotation accumulator for a
	// fresh attempt and, on the first call, installs the ctx.upstreamRequest
	// Proxy (before which ctx.upstreamRequest is undefined).
	ResetUpstreamAnnotations() error

	Logs() []LogEntry
	Close()
}

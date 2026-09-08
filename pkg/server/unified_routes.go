package server

import (
	"strings"

	"picotera/pkg/contract"
	"picotera/pkg/llmbridge"
)

// unifiedRoute binds a unified generation route's URL path to its source
// format, synthetic endpoint_type and display name. The unified routes are
// runtime constants mounted directly on the router (see server.go
// registerEndpoints) — they are NOT rows in the endpoint table — so this list
// is the single source of truth shared by:
//   - route registration (server.go),
//   - the unified handler (handle_unified_gateway.go), which reads Path /
//     Format / SourceType off the route rather than deriving them from the
//     format (two routes share FormatOpenAIResponses, so a format → path
//     reverse lookup no longer exists),
//   - the endpoint label list (handle_label.go), which surfaces them in the
//     requests page endpoint filter alongside the path-table endpoints.
//
// Codex is NOT in this list: its sub-paths are open-ended, so it is served by
// the wildcard mount below (codexMountPatterns — two prefixes sharing the one
// mount) with a route value computed per request by codexUnifiedRoute.
var unifiedRoutes = []unifiedRoute{
	{Path: "/api/unified/v1/messages", Name: "Unified Anthropic Messages", Format: llmbridge.FormatAnthropicMessages, SourceType: contract.EndpointType_AnthropicMessages},
	{Path: "/api/unified/v1/responses", Name: "Unified OpenAI Responses", Format: llmbridge.FormatOpenAIResponses, SourceType: contract.EndpointType_OpenAIResponses},
	{Path: "/api/unified/v1/chat/completions", Name: "Unified OpenAI Chat Completions", Format: llmbridge.FormatOpenAIChatCompletions, SourceType: contract.EndpointType_OpenAIChatCompletions},
	{Path: "/api/unified/v1beta/models/{model}:generateContent", Name: "Unified Gemini GenerateContent", Format: llmbridge.FormatGeminiGenerateContent, SourceType: contract.EndpointType_GeminiGenerateContent},
	{Path: "/api/unified/v1beta/models/{model}:streamGenerateContent", Name: "Unified Gemini streamGenerateContent", Format: llmbridge.FormatGeminiStreamGenerateContent, SourceType: contract.EndpointType_GeminiStreamGenerateContent},
	// OpenAI Embeddings: llmbridge has no embedding format (and needs none) —
	// request and response bytes are forwarded verbatim. Non-streaming only;
	// the body carries no `stream` field so detectStreaming is always false,
	// and a passthrough route's candidate set ignores the flag anyway.
	{Path: "/api/unified/v1/embeddings", Name: "Unified OpenAI Embeddings", Format: llmbridge.FormatUnknown, SourceType: contract.EndpointType_OpenAIEmbedding},
}

type unifiedRoute struct {
	// Path is the registered chi route pattern, and also what the meta row
	// records as endpoint_path. For the codex mount it is the normalized
	// concrete path (prefix + suffix), not the wildcard pattern.
	Path string
	// Name is the display name in the endpoint label list.
	Name string
	// Format is the inbound source format; FormatUnknown for passthrough routes.
	Format llmbridge.Format
	// SourceType is the contract.EndpointType_* the synthetic endpoint reports,
	// and — for passthrough routes — the only upstream type considered.
	SourceType int32
	// UpstreamSuffix is what a prefix-matching candidate appends to its upstream
	// URL. Empty for the fixed routes above; the normalized suffix for codex.
	UpstreamSuffix string
	// Codex reports whether this route came from the codex mount, which is what
	// makes `codex` an extra candidate type on the bridged /responses sub-path.
	Codex bool
	// PrefixMount reports whether this route came from a wildcard prefix mount,
	// whose sub-paths are open-ended: a request body without a model field routes
	// as no-model instead of 400. Distinct from Codex, which is about the extra
	// candidate type — a future prefix mount only needs to set this one.
	PrefixMount bool
}

// passthrough reports whether the route forwards bytes verbatim (no
// cross-format conversion). llmbridge having no format for the route and there
// being no converter are the same thing, so this derives from Format rather
// than carrying a separate field.
func (r unifiedRoute) passthrough() bool { return r.Format == llmbridge.FormatUnknown }

const (
	// codexMountPath is the base_url a Codex client is configured with, and the
	// canonical prefix for recording: every codex endpoint_path starts with it,
	// including requests that arrived on the alias below.
	codexMountPath = "/api/unified/codex"
	// codexBackendAPIMountPath is an alias of codexMountPath mirroring ChatGPT's
	// own layout (`<host>/backend-api/codex/responses`), so a client whose
	// base_url is `…/api/unified` reaches the same mount.
	codexBackendAPIMountPath = "/api/unified/backend-api/codex"
	// codexResponsesSuffix is the one codex sub-path that carries a real source
	// format (OpenAI Responses) and can therefore be bridged to non-codex
	// upstreams. Every other sub-path is pure passthrough.
	codexResponsesSuffix = "/responses"
)

// codexMountPatterns are the chi wildcard registrations for the codex mount.
// Both prefixes resolve to the same handler, the same suffix normalization and
// the same recorded endpoint_path (always codexMountPath + suffix).
var codexMountPatterns = []string{
	codexMountPath + "/*",
	codexBackendAPIMountPath + "/*",
}

// normalizeCodexSuffix turns the raw path remainder after either codex mount
// prefix (the chi wildcard with its leading "/" restored) into the canonical
// suffix — the two prefixes leave identical remainders, so this is
// prefix-blind. A leading "/v1" segment is stripped so a base_url of
// `…/api/unified/codex` and one of `…/api/unified/codex/v1` are equivalent.
// Returns false when nothing is left to dispatch on — the bare mount, a
// trailing slash, or a bare "/v1" — and the caller answers 404.
func normalizeCodexSuffix(raw string) (string, bool) {
	suffix := raw
	if suffix == "/v1" {
		suffix = ""
	} else if strings.HasPrefix(suffix, "/v1/") {
		suffix = suffix[len("/v1"):]
	}
	if suffix == "" || suffix == "/" || !strings.HasPrefix(suffix, "/") {
		return "", false
	}
	return suffix, true
}

// codexUnifiedRoute builds the per-request route value for a normalized codex
// suffix. `/responses` keeps OpenAI Responses as its source format, so it can
// still bridge to Anthropic / Gemini / ChatCompletions upstreams; every other
// sub-path is passthrough and therefore codex-only. Path is always
// codexMountPath + suffix, whichever mount prefix the request arrived on.
func codexUnifiedRoute(suffix string) unifiedRoute {
	route := unifiedRoute{
		Path:           codexMountPath + suffix,
		Name:           "Unified Codex",
		Format:         llmbridge.FormatUnknown,
		SourceType:     contract.EndpointType_Codex,
		UpstreamSuffix: suffix,
		Codex:          true,
		PrefixMount:    true,
	}
	if suffix == codexResponsesSuffix {
		route.Format = llmbridge.FormatOpenAIResponses
		route.Name = "Unified Codex Responses"
	}
	return route
}

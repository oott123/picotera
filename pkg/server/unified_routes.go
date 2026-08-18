package server

import (
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
var unifiedRoutes = []unifiedRoute{
	{Path: "/api/unified/v1/messages", Name: "Unified Anthropic Messages", Format: llmbridge.FormatAnthropicMessages, SourceType: contract.EndpointType_AnthropicMessages},
	{Path: "/api/unified/v1/responses", Name: "Unified OpenAI Responses", Format: llmbridge.FormatOpenAIResponses, SourceType: contract.EndpointType_OpenAIResponses},
	{Path: "/api/unified/v1/chat/completions", Name: "Unified OpenAI Chat Completions", Format: llmbridge.FormatOpenAIChatCompletions, SourceType: contract.EndpointType_OpenAIChatCompletions},
	{Path: "/api/unified/v1beta/models/{model}:generateContent", Name: "Unified Gemini GenerateContent", Format: llmbridge.FormatGeminiGenerateContent, SourceType: contract.EndpointType_GeminiGenerateContent},
	{Path: "/api/unified/v1beta/models/{model}:streamGenerateContent", Name: "Unified Gemini streamGenerateContent", Format: llmbridge.FormatGeminiStreamGenerateContent, SourceType: contract.EndpointType_GeminiStreamGenerateContent},
	// Codex: base_url is configured as `…/api/unified/codex`, so the two
	// `/codex/...` routes below are what the Codex client actually hits.
	// `/codex/responses` is a second mount of the OpenAI Responses source —
	// same candidate set and same cross-format bridging as `/v1/responses`.
	{Path: "/api/unified/codex/responses", Name: "Unified Codex Responses", Format: llmbridge.FormatOpenAIResponses, SourceType: contract.EndpointType_OpenAIResponses},
	{Path: "/api/unified/codex/responses/compact", Name: "Unified Codex Compact", Format: llmbridge.FormatUnknown, SourceType: contract.EndpointType_CodexCompact},
	{Path: "/api/unified/v1/alpha/search", Name: "Unified Codex Search v1alpha", Format: llmbridge.FormatUnknown, SourceType: contract.EndpointType_CodexSearchV1Alpha},
	// OpenAI Embeddings: llmbridge has no embedding format (and needs none) —
	// request and response bytes are forwarded verbatim. Non-streaming only;
	// the body carries no `stream` field so detectStreaming is always false,
	// and a passthrough route's candidate set ignores the flag anyway.
	{Path: "/api/unified/v1/embeddings", Name: "Unified OpenAI Embeddings", Format: llmbridge.FormatUnknown, SourceType: contract.EndpointType_OpenAIEmbedding},
}

type unifiedRoute struct {
	// Path is the registered chi route pattern, and also what the meta row
	// records as endpoint_path.
	Path string
	// Name is the display name in the endpoint label list.
	Name string
	// Format is the inbound source format; FormatUnknown for passthrough routes.
	Format llmbridge.Format
	// SourceType is the contract.EndpointType_* the synthetic endpoint reports,
	// and — for passthrough routes — the only upstream type considered.
	SourceType int32
}

// passthrough reports whether the route forwards bytes verbatim (no
// cross-format conversion). llmbridge having no format for the route and there
// being no converter are the same thing, so this derives from Format rather
// than carrying a separate field.
func (r unifiedRoute) passthrough() bool { return r.Format == llmbridge.FormatUnknown }

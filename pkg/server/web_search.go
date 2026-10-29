package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"picotera/pkg/contract"
	"picotera/pkg/db"

	"github.com/rs/xid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const webSearchMaxRounds = 10

// webSearchContext tracks the per-request state needed to emulate
// Anthropic-native web search through a search endpoint when the chosen
// upstream does not natively support web search. It is created by the unified
// gateway after the outbound rewrite fires and consumed by both the non-stream
// and SSE response transformers.
type webSearchContext struct {
	active              bool
	apiKeyToken         string
	metaID              string
	parentSpanID        string
	metaCreatedAt       time.Time
	originalRequestBody []byte // pre-rewrite client body for sub-call construction
	rewrittenBody       []byte
	// backend is picked once per attempt and reused by every round of the
	// server-side loop, so the tool schema and search wire format stay in step.
	backend webSearchBackend
}

// webSearchBackend is the search endpoint web search emulation calls for one
// request: which wire format to speak and which gateway path to send it to.
type webSearchBackend struct {
	endpointType int32  // contract.EndpointType_ExaSearch or EndpointType_DeepSeekSearch
	endpointPath string // endpoint.path the sub-call is routed to
}

// webSearchHit is one search result normalized across backends.
type webSearchHit struct {
	URL           string
	Title         string
	Text          string // plaintext stored in encrypted_content and fed back to the LLM
	PublishedDate string // raw date string; "" when the backend reported none
}

// pickWebSearchBackend chooses the search endpoint from the rows of every
// provider bound to an exaSearch or deepseekSearch endpoint. Rows are ordered
// the way gateway candidates are (provider priority descending, then higher
// provider id), with endpoint path ascending as the tie-break when one provider
// is bound to several search endpoints. Returns false when no row is usable.
func pickWebSearchBackend(rows []db.GetProvidersByEndpointTypesRow) (webSearchBackend, bool) {
	valid := make([]db.GetProvidersByEndpointTypesRow, 0, len(rows))
	for _, row := range rows {
		if row.UpstreamUrl != "" && row.ProviderCredentials != "" {
			valid = append(valid, row)
		}
	}
	if len(valid) == 0 {
		return webSearchBackend{}, false
	}
	slices.SortFunc(valid, func(a, b db.GetProvidersByEndpointTypesRow) int {
		if c := compareCandidateOrder(a.ProviderID, 0, a.ProviderPriority, b.ProviderID, 0, b.ProviderPriority); c != 0 {
			return c
		}
		return strings.Compare(a.EndpointPath, b.EndpointPath)
	})
	return webSearchBackend{endpointType: valid[0].EndpointType, endpointPath: valid[0].EndpointPath}, true
}

// resolveWebSearchBackend picks the search endpoint for one attempt. The pick
// is an endpoint, not a provider: the sub-call still goes through the
// path-based gateway, which resolves and retries across that endpoint's
// providers.
func resolveWebSearchBackend(ctx context.Context, q *db.Queries) (webSearchBackend, error) {
	rows, err := q.GetProvidersByEndpointTypes(ctx, []int32{contract.EndpointType_ExaSearch, contract.EndpointType_DeepSeekSearch})
	if err != nil {
		return webSearchBackend{}, fmt.Errorf("web search emulation: query search providers: %w", err)
	}
	backend, ok := pickWebSearchBackend(rows)
	if !ok {
		return webSearchBackend{}, errors.New("web search emulation: no exaSearch or deepseekSearch provider available")
	}
	return backend, nil
}

// toolJSON returns the function tool the LLM sees in place of the Anthropic
// web search server tool.
func (b webSearchBackend) toolJSON() json.RawMessage {
	switch b.endpointType {
	case contract.EndpointType_ExaSearch:
		return exaWebSearchToolJSON
	case contract.EndpointType_DeepSeekSearch:
		return deepseekWebSearchToolJSON
	default:
		panic(fmt.Sprintf("webSearchBackend: unexpected endpoint type %d", b.endpointType))
	}
}

// hasWebSearchTool reports whether the Anthropic Messages body declares an
// Anthropic server-side web search tool. Recognizes both versions.
func hasWebSearchTool(body []byte) bool {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	found := false
	tools.ForEach(func(_, val gjson.Result) bool {
		t := val.Get("type").Str
		if t == "web_search_20250305" || t == "web_search_20260209" {
			found = true
			return false
		}
		return true
	})
	return found
}

// exaWebSearchToolJSON is the function-tool replacement injected into the
// outbound `tools` array for the Exa backend. The schema mirrors Exa's
// POST /search body so the LLM can populate them directly; only `query` is
// required.
var exaWebSearchToolJSON = json.RawMessage(`{
  "name": "web_search",
  "description": "Search the web for current information. Returns relevant snippets from web pages.",
  "input_schema": {
    "type": "object",
    "properties": {
      "query": {"type": "string", "description": "The search query"},
      "numResults": {"type": "integer", "description": "Number of results to return (default 10, max 25)"},
      "category": {"type": "string", "enum": ["company", "research paper", "news", "personal site", "financial report", "people"], "description": "Optional category filter"},
      "includeDomains": {"type": "array", "items": {"type": "string"}, "description": "Restrict results to these domains"},
      "excludeDomains": {"type": "array", "items": {"type": "string"}, "description": "Exclude results from these domains"},
      "startPublishedDate": {"type": "string", "description": "ISO 8601 date; only results published after this"},
      "endPublishedDate": {"type": "string", "description": "ISO 8601 date; only results published before this"}
    },
    "required": ["query"]
  }
}`)

// deepseekWebSearchToolJSON is the DeepSeek backend's counterpart of
// exaWebSearchToolJSON, mirroring DeepSeek's POST /v1/tools/search body. Value
// ranges live in the descriptions rather than in maxLength / minimum, matching
// the Exa schema and staying clear of keywords llmbridge may not carry over to
// other formats.
var deepseekWebSearchToolJSON = json.RawMessage(`{
  "name": "web_search",
  "description": "Search the web for current information. Returns relevant summaries from web pages.",
  "input_schema": {
    "type": "object",
    "properties": {
      "query": {"type": "string", "description": "The search query, at most 128 characters. Supports operators: site:example.com (join several with OR), -site:example.com, -keyword, -\"phrase\", \"exact phrase\", lang:zh,en, after:2025-01-01, before:2025"},
      "count": {"type": "integer", "description": "Number of results to return (default 10, max 50)"},
      "ori_question": {"type": "string", "description": "The user's original question that prompted this search, at most 128 characters; helps the search engine understand intent"}
    },
    "required": ["query"]
  }
}`)

// rewriteWebSearchTools replaces every Anthropic web-search server tool entry
// in the `tools` array with the backend's function-tool equivalent. Other
// tools pass through unchanged.
func rewriteWebSearchTools(body []byte, backend webSearchBackend) ([]byte, error) {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return body, nil
	}
	out := make([]json.RawMessage, 0)
	tools.ForEach(func(_, val gjson.Result) bool {
		t := val.Get("type").Str
		if t == "web_search_20250305" || t == "web_search_20260209" {
			out = append(out, backend.toolJSON())
		} else {
			out = append(out, json.RawMessage(val.Raw))
		}
		return true
	})
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("rewriteWebSearchTools: marshal tools: %w", err)
	}
	return sjson.SetRawBytes(body, "tools", encoded)
}

// rewriteWebSearchHistory walks the messages array and converts assistant-side
// Anthropic web-search blocks into the equivalent function-tool flow the
// upstream will accept:
//
//   - server_tool_use(web_search)   → tool_use (ID prefix srvtoolu_ → toolu_)
//   - web_search_tool_result       → split into a new user message carrying a
//     single tool_result block; the encoded text is the
//     human-readable search result summary.
//   - text blocks that follow a web_search_tool_result get split into a new
//     assistant message; any `web_search_result_location` citations are
//     stripped because the upstream does not recognize them.
//
// Messages that do not need splitting are passed through verbatim.
func rewriteWebSearchHistory(body []byte) ([]byte, error) {
	msgs := gjson.GetBytes(body, "messages")
	if !msgs.IsArray() {
		return body, nil
	}
	out := make([]json.RawMessage, 0, len(msgs.Array()))
	var convertErr error
	msgs.ForEach(func(_, msg gjson.Result) bool {
		role := msg.Get("role").Str
		if role != "assistant" {
			out = append(out, json.RawMessage(msg.Raw))
			return true
		}
		content := msg.Get("content")
		if !content.IsArray() {
			out = append(out, json.RawMessage(msg.Raw))
			return true
		}
		converted, err := convertAssistantMessage(msg, content)
		if err != nil {
			convertErr = err
			return false
		}
		out = append(out, converted...)
		return true
	})
	if convertErr != nil {
		return nil, convertErr
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("rewriteWebSearchHistory: marshal messages: %w", err)
	}
	return sjson.SetRawBytes(body, "messages", encoded)
}

// convertAssistantMessage walks one assistant message's content blocks and
// returns 1+ messages (assistant / user / assistant / …) in the correct order
// once web-search blocks are expanded.
func convertAssistantMessage(msg, content gjson.Result) ([]json.RawMessage, error) {
	currentAssistant := make([]json.RawMessage, 0)
	out := make([]json.RawMessage, 0, 1)

	flushAssistant := func() {
		if len(currentAssistant) == 0 {
			return
		}
		blob, err := encodeAssistantWithContent(msg, currentAssistant)
		if err != nil {
			return
		}
		out = append(out, blob)
		currentAssistant = make([]json.RawMessage, 0)
	}

	var loopErr error
	content.ForEach(func(_, block gjson.Result) bool {
		blockType := block.Get("type").Str
		switch blockType {
		case "server_tool_use":
			if block.Get("name").Str == "web_search" {
				converted, err := convertServerToolUseToToolUse(block)
				if err != nil {
					loopErr = err
					return false
				}
				currentAssistant = append(currentAssistant, converted)
				return true
			}
			currentAssistant = append(currentAssistant, json.RawMessage(block.Raw))
		case "web_search_tool_result":
			toolUseID := mapWebSearchToolUseID(block.Get("tool_use_id").Str)
			text := renderWebSearchResultsAsText(block.Get("content"))
			toolResult, err := encodeUserToolResult(toolUseID, text)
			if err != nil {
				loopErr = err
				return false
			}
			flushAssistant()
			out = append(out, toolResult)
		case "text":
			currentAssistant = append(currentAssistant, stripWebSearchCitations(block))
		default:
			currentAssistant = append(currentAssistant, json.RawMessage(block.Raw))
		}
		return true
	})
	if loopErr != nil {
		return nil, loopErr
	}
	flushAssistant()
	if len(out) == 0 {
		out = append(out, json.RawMessage(msg.Raw))
	}
	return out, nil
}

// encodeAssistantWithContent rebuilds an assistant message preserving every
// non-content field from the original (so things like custom metadata stay
// intact) and swapping in the converted content slice.
func encodeAssistantWithContent(msg gjson.Result, blocks []json.RawMessage) (json.RawMessage, error) {
	fields := make(map[string]json.RawMessage)
	msg.ForEach(func(key, val gjson.Result) bool {
		if key.Str == "content" {
			return true
		}
		fields[key.Str] = json.RawMessage(val.Raw)
		return true
	})
	if _, ok := fields["role"]; !ok {
		fields["role"] = json.RawMessage(`"assistant"`)
	}
	contentBytes, err := json.Marshal(blocks)
	if err != nil {
		return nil, fmt.Errorf("encodeAssistantWithContent: marshal content: %w", err)
	}
	fields["content"] = contentBytes
	return json.Marshal(fields)
}

func encodeUserToolResult(toolUseID, text string) (json.RawMessage, error) {
	msg := map[string]any{
		"role": "user",
		"content": []map[string]any{
			{
				"type":        "tool_result",
				"tool_use_id": toolUseID,
				"content":     text,
			},
		},
	}
	return json.Marshal(msg)
}

func convertServerToolUseToToolUse(block gjson.Result) (json.RawMessage, error) {
	fields := make(map[string]json.RawMessage)
	block.ForEach(func(key, val gjson.Result) bool {
		switch key.Str {
		case "type":
			fields["type"] = json.RawMessage(`"tool_use"`)
		case "id":
			rawID := val.Str
			mapped, err := json.Marshal(mapServerToolUseID(rawID))
			if err != nil {
				return false
			}
			fields["id"] = mapped
		default:
			fields[key.Str] = json.RawMessage(val.Raw)
		}
		return true
	})
	if _, ok := fields["type"]; !ok {
		fields["type"] = json.RawMessage(`"tool_use"`)
	}
	return json.Marshal(fields)
}

func mapServerToolUseID(id string) string {
	if strings.HasPrefix(id, "srvtoolu_") {
		return "toolu_" + strings.TrimPrefix(id, "srvtoolu_")
	}
	return id
}

func mapWebSearchToolUseID(id string) string {
	return mapServerToolUseID(id)
}

// renderWebSearchResultsAsText produces the prose injected as tool_result
// content. Plain text format keeps the LLM-facing context compact while still
// surfacing the URL + title + result body for each search hit.
func renderWebSearchResultsAsText(content gjson.Result) string {
	if !content.IsArray() {
		return "No results."
	}
	var b strings.Builder
	b.WriteString("Search results:\n")
	idx := 0
	content.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").Str != "web_search_result" {
			return true
		}
		idx++
		title := item.Get("title").Str
		url := item.Get("url").Str
		body := item.Get("encrypted_content").Str
		fmt.Fprintf(&b, "\n%d. %s\n   URL: %s\n", idx, title, url)
		if body != "" {
			for _, line := range strings.Split(body, "\n") {
				if line == "" {
					continue
				}
				fmt.Fprintf(&b, "   %s\n", line)
			}
		}
		return true
	})
	if idx == 0 {
		return "No results."
	}
	return b.String()
}

// stripWebSearchCitations removes citations whose type is
// web_search_result_location (the upstream cannot validate them). If a text
// block carries no other citations, the field is dropped entirely.
func stripWebSearchCitations(block gjson.Result) json.RawMessage {
	citations := block.Get("citations")
	if !citations.Exists() || !citations.IsArray() {
		return json.RawMessage(block.Raw)
	}
	kept := make([]json.RawMessage, 0)
	citations.ForEach(func(_, c gjson.Result) bool {
		if c.Get("type").Str == "web_search_result_location" {
			return true
		}
		kept = append(kept, json.RawMessage(c.Raw))
		return true
	})
	fields := make(map[string]json.RawMessage)
	block.ForEach(func(key, val gjson.Result) bool {
		if key.Str == "citations" {
			return true
		}
		fields[key.Str] = json.RawMessage(val.Raw)
		return true
	})
	if len(kept) > 0 {
		encoded, err := json.Marshal(kept)
		if err == nil {
			fields["citations"] = encoded
		}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return json.RawMessage(block.Raw)
	}
	return raw
}

// ExaSearchRequest mirrors the Exa /search payload fields we let the LLM
// populate (via the function-tool input schema), plus the fixed
// contents.highlights flag we always set.
type ExaSearchRequest struct {
	Query              string         `json:"query"`
	NumResults         int            `json:"numResults,omitempty"`
	Category           string         `json:"category,omitempty"`
	IncludeDomains     []string       `json:"includeDomains,omitempty"`
	ExcludeDomains     []string       `json:"excludeDomains,omitempty"`
	StartPublishedDate string         `json:"startPublishedDate,omitempty"`
	EndPublishedDate   string         `json:"endPublishedDate,omitempty"`
	Contents           ExaContentsReq `json:"contents"`
}

type ExaContentsReq struct {
	Highlights bool `json:"highlights"`
}

// ExaSearchResponse only decodes the fields the gateway uses to build the
// Anthropic web_search_tool_result block; everything else is ignored.
type ExaSearchResponse struct {
	Results []ExaResult `json:"results"`
}

type ExaResult struct {
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	Highlights    []string `json:"highlights"`
	PublishedDate string   `json:"publishedDate"`
}

// DeepSeekSearchRequest mirrors the DeepSeek POST /v1/tools/search fields the
// LLM may populate. need_content and localization are never sent.
type DeepSeekSearchRequest struct {
	Query       string `json:"query"`
	Count       int    `json:"count,omitempty"`
	OriQuestion string `json:"ori_question,omitempty"`
}

// DeepSeekSearchResponse only decodes the fields the gateway uses to build the
// Anthropic web_search_tool_result block; everything else is ignored.
type DeepSeekSearchResponse struct {
	Results []DeepSeekResult `json:"results"`
}

// DeepSeekResult.Date is null when DeepSeek knows no date; that decodes to "".
type DeepSeekResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Date    string `json:"date"`
}

// deepseekSearchMaxQueryRunes is DeepSeek's query length limit, counted in
// Unicode characters.
const deepseekSearchMaxQueryRunes = 128

// webSearchToolError carries an Anthropic web_search_tool_result_error code
// for failures that have a more specific code than "unavailable".
type webSearchToolError struct{ code string }

func (e *webSearchToolError) Error() string {
	return "web search: " + e.code
}

// webSearchErrorCode maps a search failure to the error_code of the
// web_search_tool_result_error block; anything without a specific code is
// "unavailable".
func webSearchErrorCode(err error) string {
	var toolErr *webSearchToolError
	if errors.As(err, &toolErr) {
		return toolErr.code
	}
	return "unavailable"
}

func buildExaSearchBody(toolInput json.RawMessage) ([]byte, error) {
	var exaReq ExaSearchRequest
	if len(toolInput) > 0 {
		if err := json.Unmarshal(toolInput, &exaReq); err != nil {
			return nil, fmt.Errorf("decode tool input: %w", err)
		}
	}
	if exaReq.Query == "" {
		return nil, errors.New("query is empty")
	}
	exaReq.Contents = ExaContentsReq{Highlights: true}
	return json.Marshal(exaReq)
}

func decodeExaSearchHits(body []byte) ([]webSearchHit, error) {
	var resp ExaSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	hits := make([]webSearchHit, 0, len(resp.Results))
	for _, r := range resp.Results {
		hits = append(hits, webSearchHit{URL: r.URL, Title: r.Title, Text: strings.Join(r.Highlights, "\n\n"), PublishedDate: r.PublishedDate})
	}
	return hits, nil
}

func buildDeepSeekSearchBody(toolInput json.RawMessage) ([]byte, error) {
	var dsReq DeepSeekSearchRequest
	if len(toolInput) > 0 {
		if err := json.Unmarshal(toolInput, &dsReq); err != nil {
			return nil, fmt.Errorf("decode tool input: %w", err)
		}
	}
	if dsReq.Query == "" {
		return nil, errors.New("query is empty")
	}
	if utf8.RuneCountInString(dsReq.Query) > deepseekSearchMaxQueryRunes {
		return nil, &webSearchToolError{code: "query_too_long"}
	}
	return json.Marshal(dsReq)
}

func decodeDeepSeekSearchHits(body []byte) ([]webSearchHit, error) {
	var resp DeepSeekSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	hits := make([]webSearchHit, 0, len(resp.Results))
	for _, r := range resp.Results {
		hits = append(hits, webSearchHit{URL: r.URL, Title: r.Title, Text: r.Summary, PublishedDate: r.Date})
	}
	return hits, nil
}

// callWebSearch routes a search through the path-based gateway so the request
// goes through full provider resolution, JS hooks, retry, and logging. The
// search upstream is reached by `httptest.NewRequest` against the endpoint
// path wsCtx.backend picked, and the backend decides the request body and
// response decoding. Errors here surface to the caller and are rendered as a
// web_search_tool_result_error block.
func (h *gatewayHandler) callWebSearch(ctx context.Context, toolInput json.RawMessage, wsCtx *webSearchContext) ([]webSearchHit, error) {
	if wsCtx == nil || !wsCtx.active {
		return nil, errors.New("callWebSearch: web search context inactive")
	}
	var (
		buildBody  func(json.RawMessage) ([]byte, error)
		decodeHits func([]byte) ([]webSearchHit, error)
	)
	switch wsCtx.backend.endpointType {
	case contract.EndpointType_ExaSearch:
		buildBody, decodeHits = buildExaSearchBody, decodeExaSearchHits
	case contract.EndpointType_DeepSeekSearch:
		buildBody, decodeHits = buildDeepSeekSearchBody, decodeDeepSeekSearchHits
	default:
		return nil, fmt.Errorf("callWebSearch: unexpected endpoint type %d", wsCtx.backend.endpointType)
	}

	bodyBytes, err := buildBody(toolInput)
	if err != nil {
		return nil, fmt.Errorf("callWebSearch: %w", err)
	}

	subReq := httptest.NewRequestWithContext(ctx, http.MethodPost, wsCtx.backend.endpointPath, bytes.NewReader(bodyBytes))
	subReq.Header.Set("Content-Type", "application/json")
	if wsCtx.apiKeyToken != "" {
		subReq.Header.Set("Authorization", "Bearer "+wsCtx.apiKeyToken)
	}
	if wsCtx.parentSpanID != "" {
		subReq.Header.Set("X-Session-Affinity", wsCtx.parentSpanID)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, subReq)
	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("callWebSearch: upstream status %d: %s", resp.StatusCode, string(respBytes))
	}
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("callWebSearch: read response: %w", err)
	}
	hits, err := decodeHits(respBytes)
	if err != nil {
		return nil, fmt.Errorf("callWebSearch: decode response: %w", err)
	}
	return hits, nil
}

// buildWebSearchToolResult constructs an Anthropic web_search_tool_result
// block from normalized search hits. Each hit maps to one web_search_result
// with its text stored plaintext in encrypted_content.
func buildWebSearchToolResult(toolUseID string, hits []webSearchHit) json.RawMessage {
	results := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		entry := map[string]any{
			"type":              "web_search_result",
			"url":               hit.URL,
			"title":             hit.Title,
			"encrypted_content": hit.Text,
		}
		if hit.PublishedDate != "" {
			if pageAge := formatPageAge(hit.PublishedDate); pageAge != "" {
				entry["page_age"] = pageAge
			}
		}
		results = append(results, entry)
	}
	block := map[string]any{
		"type":        "web_search_tool_result",
		"tool_use_id": toolUseID,
		"content":     results,
	}
	out, _ := json.Marshal(block)
	return out
}

func buildWebSearchToolResultError(toolUseID, errorCode string) json.RawMessage {
	block := map[string]any{
		"type":        "web_search_tool_result",
		"tool_use_id": toolUseID,
		"content": map[string]any{
			"type":       "web_search_tool_result_error",
			"error_code": errorCode,
		},
	}
	out, _ := json.Marshal(block)
	return out
}

// formatPageAge tries the date formats search backends report and renders the
// date as "January 2, 2006". Unrecognized inputs return empty string so the
// caller drops the field.
func formatPageAge(s string) string {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("January 2, 2006")
		}
	}
	return ""
}

// mapToolUseID flips the prefix from toolu_ → srvtoolu_ when emitting an
// Anthropic-native server_tool_use back to the client.
func mapToolUseIDToServer(id string) string {
	if strings.HasPrefix(id, "toolu_") {
		return "srvtoolu_" + strings.TrimPrefix(id, "toolu_")
	}
	return id
}

// generateToolUseID returns a synthetic server_tool_use id used when the
// upstream tool_use lacked one (defensive — Anthropic always supplies one).
func generateToolUseID() string {
	return "srvtoolu_" + xid.New().String()
}

// transformWebSearchResponse rewrites a non-stream Anthropic Messages JSON
// response in-place: every tool_use(web_search) is converted to
// server_tool_use and followed by a web_search_tool_result block populated
// via the search backend. stop_reason is downgraded to "pause_turn" only when every tool_use
// in the response is a web_search call.
func (h *gatewayHandler) transformWebSearchResponse(ctx context.Context, body []byte, wsCtx *webSearchContext) ([]byte, error) {
	content := gjson.GetBytes(body, "content")
	if !content.IsArray() {
		return body, nil
	}
	out := make([]json.RawMessage, 0, len(content.Array()))
	webSearchCalls := 0
	otherToolCalls := 0
	var loopErr error
	content.ForEach(func(_, block gjson.Result) bool {
		if block.Get("type").Str != "tool_use" {
			out = append(out, json.RawMessage(block.Raw))
			return true
		}
		if block.Get("name").Str != "web_search" {
			otherToolCalls++
			out = append(out, json.RawMessage(block.Raw))
			return true
		}
		webSearchCalls++
		toolUseID := block.Get("id").Str
		serverToolUseID := mapToolUseIDToServer(toolUseID)
		if serverToolUseID == "" {
			serverToolUseID = generateToolUseID()
		}
		serverToolUse, err := buildServerToolUseFromToolUse(block, serverToolUseID)
		if err != nil {
			loopErr = err
			return false
		}
		out = append(out, serverToolUse)

		toolInput := block.Get("input").Raw
		hits, err := h.callWebSearch(ctx, json.RawMessage(toolInput), wsCtx)
		if err != nil {
			out = append(out, buildWebSearchToolResultError(serverToolUseID, webSearchErrorCode(err)))
			return true
		}
		out = append(out, buildWebSearchToolResult(serverToolUseID, hits))
		return true
	})
	if loopErr != nil {
		return nil, loopErr
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("transformWebSearchResponse: marshal content: %w", err)
	}
	body, err = sjson.SetRawBytes(body, "content", encoded)
	if err != nil {
		return nil, err
	}
	if webSearchCalls > 0 && otherToolCalls == 0 {
		stopReason := gjson.GetBytes(body, "stop_reason").Str
		if stopReason == "tool_use" {
			body, err = sjson.SetBytes(body, "stop_reason", "pause_turn")
			if err != nil {
				return nil, err
			}
		}
	}
	return body, nil
}

// buildServerToolUseFromToolUse converts an Anthropic tool_use block into the
// matching server_tool_use block. The whole input object is preserved so the
// client can replay the same search parameters in a follow-up request.
func buildServerToolUseFromToolUse(block gjson.Result, serverToolUseID string) (json.RawMessage, error) {
	fields := make(map[string]json.RawMessage)
	block.ForEach(func(key, val gjson.Result) bool {
		switch key.Str {
		case "type":
			fields["type"] = json.RawMessage(`"server_tool_use"`)
		case "id":
			encoded, err := json.Marshal(serverToolUseID)
			if err != nil {
				return false
			}
			fields["id"] = encoded
		default:
			fields[key.Str] = json.RawMessage(val.Raw)
		}
		return true
	})
	if _, ok := fields["type"]; !ok {
		fields["type"] = json.RawMessage(`"server_tool_use"`)
	}
	return json.Marshal(fields)
}

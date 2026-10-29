package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// buildWebSearchSubBody constructs the body for the next self-call round to
// /api/unified/v1/messages. originalBody still carries the original
// web_search_2025xxxx / web_search_20260209 tools and any server_tool_use /
// web_search_tool_result history from the client. accumulatedContent is the
// Anthropic-native content blocks produced so far (including server_tool_use +
// web_search_tool_result pairs).
//
// The returned body has:
//   - messages: original messages + an assistant turn with accumulatedContent
//   - tools: web_search server tools replaced with backend's function-tool form
//   - history: server_tool_use/web_search_tool_result converted to tool_use/tool_result
//
// The inner unified handler's hasWebSearchTool check returns false.
func buildWebSearchSubBody(originalBody []byte, accumulatedContent []json.RawMessage, backend webSearchBackend) ([]byte, error) {
	body := append([]byte(nil), originalBody...)

	msgs := gjson.GetBytes(body, "messages")
	existingMsgs := make([]json.RawMessage, 0)
	if msgs.IsArray() {
		msgs.ForEach(func(_, msg gjson.Result) bool {
			existingMsgs = append(existingMsgs, json.RawMessage(msg.Raw))
			return true
		})
	}

	assistantTurn := map[string]any{
		"role":    "assistant",
		"content": accumulatedContent,
	}
	assistantBytes, err := json.Marshal(assistantTurn)
	if err != nil {
		return nil, err
	}
	existingMsgs = append(existingMsgs, assistantBytes)

	msgsBytes, err := json.Marshal(existingMsgs)
	if err != nil {
		return nil, err
	}
	body, err = sjson.SetRawBytes(body, "messages", msgsBytes)
	if err != nil {
		return nil, err
	}

	body, err = rewriteWebSearchTools(body, backend)
	if err != nil {
		return nil, err
	}
	body, err = rewriteWebSearchHistory(body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// gjsonContentToRawSlice extracts the "content" array from an Anthropic
// Messages JSON response as a slice of json.RawMessage.
func gjsonContentToRawSlice(body []byte) []json.RawMessage {
	content := gjson.GetBytes(body, "content")
	if !content.IsArray() {
		return nil
	}
	out := make([]json.RawMessage, 0, len(content.Array()))
	content.ForEach(func(_, block gjson.Result) bool {
		out = append(out, json.RawMessage(block.Raw))
		return true
	})
	return out
}

// mergeNonStreamRound merges a sub-round response into the accumulated
// response. Content blocks are appended, stop_reason/stop_sequence are
// replaced, and usage fields are summed.
func mergeNonStreamRound(outer, sub []byte) []byte {
	subContent := gjson.GetBytes(sub, "content")
	if subContent.IsArray() {
		outerContent := gjson.GetBytes(outer, "content")
		merged := make([]json.RawMessage, 0)
		if outerContent.IsArray() {
			outerContent.ForEach(func(_, block gjson.Result) bool {
				merged = append(merged, json.RawMessage(block.Raw))
				return true
			})
		}
		subContent.ForEach(func(_, block gjson.Result) bool {
			merged = append(merged, json.RawMessage(block.Raw))
			return true
		})
		encoded, err := json.Marshal(merged)
		if err == nil {
			outer, _ = sjson.SetRawBytes(outer, "content", encoded)
		}
	}

	if sr := gjson.GetBytes(sub, "stop_reason"); sr.Exists() {
		outer, _ = sjson.SetRawBytes(outer, "stop_reason", []byte(sr.Raw))
	}
	if ss := gjson.GetBytes(sub, "stop_sequence"); ss.Exists() {
		outer, _ = sjson.SetRawBytes(outer, "stop_sequence", []byte(ss.Raw))
	}

	outer = mergeUsageBytes(outer, sub)
	return outer
}

// mergeUsageBytes sums usage fields from sub into outer at the JSON level.
func mergeUsageBytes(outer, sub []byte) []byte {
	outerUsage := gjson.GetBytes(outer, "usage")
	subUsage := gjson.GetBytes(sub, "usage")
	if !subUsage.Exists() {
		return outer
	}
	if !outerUsage.Exists() {
		outer, _ = sjson.SetRawBytes(outer, "usage", []byte(subUsage.Raw))
		return outer
	}

	for _, key := range []string{
		"input_tokens",
		"output_tokens",
		"cache_creation_input_tokens",
		"cache_read_input_tokens",
	} {
		ov := outerUsage.Get(key)
		sv := subUsage.Get(key)
		if sv.Exists() {
			sum := ov.Int() + sv.Int()
			outer, _ = sjson.SetBytes(outer, "usage."+key, sum)
		}
	}

	subWSR := subUsage.Get("server_tool_use.web_search_requests")
	if subWSR.Exists() {
		outerWSR := outerUsage.Get("server_tool_use.web_search_requests")
		sum := outerWSR.Int() + subWSR.Int()
		outer, _ = sjson.SetBytes(outer, "usage.server_tool_use.web_search_requests", sum)
	}

	return outer
}

// loopWebSearchNonStream runs the server-side web search loop for non-streaming
// responses. Each iteration self-calls /api/unified/v1/messages and merges the
// result into accumulated.
func (h *gatewayHandler) loopWebSearchNonStream(ctx context.Context, accumulated []byte, wsCtx *webSearchContext, fwdHeaders http.Header) []byte {
	round := 1
	for {
		stopReason := gjson.GetBytes(accumulated, "stop_reason").Str
		if stopReason != "pause_turn" {
			return accumulated
		}
		if round >= webSearchMaxRounds {
			return accumulated
		}

		contentArr := gjsonContentToRawSlice(accumulated)
		subBody, err := buildWebSearchSubBody(wsCtx.originalRequestBody, contentArr, wsCtx.backend)
		if err != nil {
			return accumulated
		}

		subReq := httptest.NewRequestWithContext(ctx, "POST", "/api/unified/v1/messages", bytes.NewReader(subBody))
		for k, vs := range fwdHeaders {
			for _, v := range vs {
				subReq.Header.Add(k, v)
			}
		}
		subReq.Header.Set("Content-Type", "application/json")
		subReq.Header.Set("Accept", "application/json")
		subReq.Header.Set("Authorization", "Bearer "+wsCtx.apiKeyToken)
		if wsCtx.parentSpanID != "" {
			subReq.Header.Set("X-Session-Affinity", wsCtx.parentSpanID)
		}

		rec := httptest.NewRecorder()
		h.Server.router.ServeHTTP(rec, subReq)
		if rec.Code != http.StatusOK {
			return accumulated
		}
		subBytes := rec.Body.Bytes()

		subTransformed, err := h.transformWebSearchResponse(ctx, subBytes, wsCtx)
		if err != nil {
			return accumulated
		}

		accumulated = mergeNonStreamRound(accumulated, subTransformed)
		round++
	}
}

// buildForwardedHeaders extracts headers from the original request that should
// be forwarded to self-call sub-requests.
func buildForwardedHeaders(r *http.Request) http.Header {
	h := make(http.Header)
	if v := r.Header.Get("Authorization"); v != "" {
		h.Set("Authorization", v)
	}
	if v := r.Header.Get("X-Claude-Code-Session-Id"); v != "" {
		h.Set("X-Claude-Code-Session-Id", v)
	}
	return h
}

// mergeUsageInto sums sub usage map values into dst (both are map[string]any).
func mergeUsageInto(dst, sub map[string]any) {
	if dst == nil || sub == nil {
		return
	}
	for _, key := range []string{
		"input_tokens",
		"output_tokens",
		"cache_creation_input_tokens",
		"cache_read_input_tokens",
	} {
		if sv, ok := sub[key]; ok {
			dv, _ := dst[key]
			dst[key] = toFloat64(dv) + toFloat64(sv)
		}
	}
	dstSTU, _ := dst["server_tool_use"].(map[string]any)
	subSTU, _ := sub["server_tool_use"].(map[string]any)
	if subSTU != nil {
		if dstSTU == nil {
			dstSTU = map[string]any{}
			dst["server_tool_use"] = dstSTU
		}
		if sv, ok := subSTU["web_search_requests"]; ok {
			dv, _ := dstSTU["web_search_requests"]
			dstSTU["web_search_requests"] = toFloat64(dv) + toFloat64(sv)
		}
	}
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

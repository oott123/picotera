package server

import (
	"encoding/json"
	"testing"

	"picotera/pkg/contract"

	"github.com/tidwall/gjson"
)

var (
	exaTestBackend      = webSearchBackend{endpointType: contract.EndpointType_ExaSearch, endpointPath: "/exa/search"}
	deepseekTestBackend = webSearchBackend{endpointType: contract.EndpointType_DeepSeekSearch, endpointPath: "/deepseek/v1/tools/search"}
)

func TestBuildWebSearchSubBody(t *testing.T) {
	original := []byte(`{
		"model": "claude-3-5-sonnet",
		"stream": true,
		"tools": [
			{"type": "web_search_20250305"},
			{"name": "other_tool", "type": "function"}
		],
		"messages": [
			{"role": "user", "content": "hello"}
		]
	}`)

	accumulated := []json.RawMessage{
		json.RawMessage(`{"type":"text","text":"Here is info."}`),
		json.RawMessage(`{"type":"server_tool_use","id":"srvtoolu_abc","name":"web_search","input":{"query":"test"}}`),
		json.RawMessage(`{"type":"web_search_tool_result","tool_use_id":"srvtoolu_abc","content":[{"type":"web_search_result","url":"https://example.com","title":"Example"}]}`),
	}

	result, err := buildWebSearchSubBody(original, accumulated, exaTestBackend)
	if err != nil {
		t.Fatal(err)
	}

	msgs := gjson.GetBytes(result, "messages")
	if !msgs.IsArray() {
		t.Fatal("messages should be an array")
	}
	msgArr := msgs.Array()
	if len(msgArr) < 2 {
		t.Fatalf("expected at least 2 messages (original user + assistant), got %d", len(msgArr))
	}

	// After rewriteWebSearchHistory, the assistant turn with server_tool_use/web_search_tool_result
	// should have been split into assistant/user/assistant messages.
	// The exact count depends on the rewrite; just verify the last original message
	// and that assistant content was added.
	found := false
	for _, msg := range msgArr {
		if msg.Get("role").Str == "assistant" {
			found = true
		}
	}
	if !found {
		t.Error("expected at least one assistant message in output")
	}

	// Verify tools were rewritten — no web_search_20250305 should remain.
	tools := gjson.GetBytes(result, "tools")
	if tools.IsArray() {
		tools.ForEach(func(_, tool gjson.Result) bool {
			tp := tool.Get("type").Str
			if tp == "web_search_20250305" || tp == "web_search_20260209" {
				t.Error("web search server tool should have been rewritten")
			}
			return true
		})
	}

	// Check that a function-tool web_search was added.
	foundFunctionTool := false
	tools.ForEach(func(_, tool gjson.Result) bool {
		if tool.Get("name").Str == "web_search" && tool.Get("input_schema").Exists() {
			foundFunctionTool = true
			return false
		}
		return true
	})
	if !foundFunctionTool {
		t.Error("expected function-tool web_search in output tools")
	}
}

func TestBuildWebSearchSubBody_DoesNotMutateInput(t *testing.T) {
	original := []byte(`{"model":"m","tools":[{"type":"web_search_20250305"}],"messages":[{"role":"user","content":"hi"}]}`)
	snapshot := append([]byte(nil), original...)

	content := []json.RawMessage{json.RawMessage(`{"type":"text","text":"ok"}`)}
	_, err := buildWebSearchSubBody(original, content, exaTestBackend)
	if err != nil {
		t.Fatal(err)
	}

	// Call again to double-check.
	_, err = buildWebSearchSubBody(original, content, exaTestBackend)
	if err != nil {
		t.Fatal(err)
	}

	if string(original) != string(snapshot) {
		t.Error("originalBody was mutated")
	}
}

func TestBuildWebSearchSubBody_DeepSeekBackend(t *testing.T) {
	original := []byte(`{"model":"m","tools":[{"type":"web_search_20260209"}],"messages":[{"role":"user","content":"hi"}]}`)
	content := []json.RawMessage{json.RawMessage(`{"type":"text","text":"ok"}`)}

	result, err := buildWebSearchSubBody(original, content, deepseekTestBackend)
	if err != nil {
		t.Fatal(err)
	}

	tool := gjson.GetBytes(result, "tools.0")
	if tool.Get("name").Str != "web_search" {
		t.Fatalf("tools[0].name = %q, want web_search", tool.Get("name").Str)
	}
	props := tool.Get("input_schema.properties")
	for _, key := range []string{"query", "count", "ori_question"} {
		if !props.Get(key).Exists() {
			t.Errorf("input_schema.properties missing %q", key)
		}
	}
	if props.Get("numResults").Exists() {
		t.Error("deepseek schema should not carry Exa's numResults")
	}
}

func TestMergeNonStreamRound(t *testing.T) {
	outer := []byte(`{
		"id": "msg_001",
		"model": "claude-3-5-sonnet",
		"type": "message",
		"role": "assistant",
		"content": [{"type":"text","text":"round 1"}],
		"stop_reason": "pause_turn",
		"stop_sequence": null,
		"usage": {"input_tokens": 100, "output_tokens": 50}
	}`)

	sub := []byte(`{
		"id": "msg_002",
		"model": "claude-3-5-sonnet",
		"type": "message",
		"role": "assistant",
		"content": [{"type":"text","text":"round 2"}],
		"stop_reason": "end_turn",
		"stop_sequence": null,
		"usage": {"input_tokens": 120, "output_tokens": 30}
	}`)

	result := mergeNonStreamRound(outer, sub)

	if gjson.GetBytes(result, "id").Str != "msg_001" {
		t.Error("id should be preserved from outer")
	}

	content := gjson.GetBytes(result, "content")
	if !content.IsArray() || len(content.Array()) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(content.Array()))
	}
	if content.Array()[0].Get("text").Str != "round 1" {
		t.Error("first content block should be from round 1")
	}
	if content.Array()[1].Get("text").Str != "round 2" {
		t.Error("second content block should be from round 2")
	}

	if gjson.GetBytes(result, "stop_reason").Str != "end_turn" {
		t.Errorf("stop_reason should be 'end_turn', got %q", gjson.GetBytes(result, "stop_reason").Str)
	}

	inputTokens := gjson.GetBytes(result, "usage.input_tokens").Int()
	outputTokens := gjson.GetBytes(result, "usage.output_tokens").Int()
	if inputTokens != 220 {
		t.Errorf("input_tokens = %d, want 220", inputTokens)
	}
	if outputTokens != 80 {
		t.Errorf("output_tokens = %d, want 80", outputTokens)
	}
}

func TestMergeUsageBytes(t *testing.T) {
	outer := []byte(`{
		"usage": {
			"input_tokens": 100,
			"output_tokens": 50,
			"server_tool_use": {"web_search_requests": 2}
		}
	}`)

	sub := []byte(`{
		"usage": {
			"input_tokens": 80,
			"output_tokens": 30,
			"cache_read_input_tokens": 10,
			"server_tool_use": {"web_search_requests": 1}
		}
	}`)

	result := mergeUsageBytes(outer, sub)
	usage := gjson.GetBytes(result, "usage")

	if usage.Get("input_tokens").Int() != 180 {
		t.Errorf("input_tokens = %d, want 180", usage.Get("input_tokens").Int())
	}
	if usage.Get("output_tokens").Int() != 80 {
		t.Errorf("output_tokens = %d, want 80", usage.Get("output_tokens").Int())
	}
	if usage.Get("cache_read_input_tokens").Int() != 10 {
		t.Errorf("cache_read_input_tokens = %d, want 10", usage.Get("cache_read_input_tokens").Int())
	}
	if usage.Get("server_tool_use.web_search_requests").Int() != 3 {
		t.Errorf("web_search_requests = %d, want 3", usage.Get("server_tool_use.web_search_requests").Int())
	}
}

func TestMergeUsageBytes_SubMissing(t *testing.T) {
	outer := []byte(`{"usage": {"input_tokens": 100}}`)
	sub := []byte(`{}`)

	result := mergeUsageBytes(outer, sub)
	if gjson.GetBytes(result, "usage.input_tokens").Int() != 100 {
		t.Error("usage should be preserved when sub has no usage")
	}
}

func TestMergeUsageInto(t *testing.T) {
	dst := map[string]any{
		"input_tokens":  float64(100),
		"output_tokens": float64(50),
		"server_tool_use": map[string]any{
			"web_search_requests": float64(2),
		},
	}

	sub := map[string]any{
		"input_tokens":  float64(80),
		"output_tokens": float64(30),
		"server_tool_use": map[string]any{
			"web_search_requests": float64(1),
		},
	}

	mergeUsageInto(dst, sub)

	if dst["input_tokens"] != float64(180) {
		t.Errorf("input_tokens = %v, want 180", dst["input_tokens"])
	}
	if dst["output_tokens"] != float64(80) {
		t.Errorf("output_tokens = %v, want 80", dst["output_tokens"])
	}
	stu := dst["server_tool_use"].(map[string]any)
	if stu["web_search_requests"] != float64(3) {
		t.Errorf("web_search_requests = %v, want 3", stu["web_search_requests"])
	}
}

package server

import (
	"encoding/base64"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"picotera/pkg/db"

	"google.golang.org/protobuf/encoding/protowire"
)

// chunkReader delivers pre-defined chunks sequentially.
type chunkReader struct {
	chunks []string
	idx    int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.idx])
	r.idx++
	return n, nil
}

func TestResponseExtractor_SSE_ForwardsBytesUnchanged(t *testing.T) {
	sseData := "data: {\"id\":\"chatcmpl-1\"}\n\ndata: {\"id\":\"chatcmpl-2\"}\n\n"
	inner := strings.NewReader(sseData)
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != sseData {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), sseData)
	}
}

func TestResponseExtractor_SSE_EventsAcrossReadCalls(t *testing.T) {
	parts := []string{
		"data: {\"id\":\"ch",
		"atcmpl-1\"}\n\ndata: [DONE]\n\n",
	}
	inner := &chunkReader{chunks: parts}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := "data: {\"id\":\"chatcmpl-1\"}\n\ndata: [DONE]\n\n"
	if string(got) != want {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), want)
	}
}

func TestResponseExtractor_JSON_ForwardsBytesUnchanged(t *testing.T) {
	jsonData := `{"id":"chatcmpl-1","usage":{"prompt_tokens":10,"completion_tokens":20}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != jsonData {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), jsonData)
	}
}

func TestResponseExtractor_SSE_OpenAI_TTFT(t *testing.T) {
	start := time.Now().Add(-100 * time.Millisecond)
	events := []string{
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n",
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n",
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", start)

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.TTFTMs == nil {
		t.Fatal("expected TTFTMs to be set")
	}
	if *m.TTFTMs < 50 {
		t.Errorf("TTFTMs too low: got %d, expected >= 50", *m.TTFTMs)
	}
}

func TestResponseExtractor_SSE_OpenAI_ToolCallTTFT(t *testing.T) {
	start := time.Now()
	events := []string{
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0}]}}]}\n\n",
		"data: [DONE]\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", start)

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.TTFTMs == nil {
		t.Fatal("expected TTFTMs to be set for tool_calls delta")
	}
}

func TestResponseExtractor_SSE_OpenAI_Usage(t *testing.T) {
	events := []string{
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n",
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"prompt_tokens_details\":{\"cached_tokens\":30}}}\n\n",
		"data: [DONE]\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 70 {
		t.Errorf("InputTokens: got %v, want 70", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 50 {
		t.Errorf("OutputTokens: got %v, want 50", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 30 {
		t.Errorf("CacheReadTokens: got %v, want 30", m.CacheReadTokens)
	}
}

func TestResponseExtractor_SSE_Anthropic_FullFlow(t *testing.T) {
	start := time.Now().Add(-80 * time.Millisecond)
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":200,\"cache_read_input_tokens\":50,\"cache_creation_input_tokens\":10}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":25}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", start)

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.TTFTMs == nil {
		t.Fatal("expected TTFTMs to be set")
	}
	if m.InputTokens == nil || *m.InputTokens != 200 {
		t.Errorf("InputTokens: got %v, want 200", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 25 {
		t.Errorf("OutputTokens: got %v, want 25", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 50 {
		t.Errorf("CacheReadTokens: got %v, want 50", m.CacheReadTokens)
	}
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 10 {
		t.Errorf("CacheWriteTokens: got %v, want 10", m.CacheWriteTokens)
	}
	if m.CacheWrite1HTokens != nil {
		t.Errorf("CacheWrite1HTokens should be nil for fallback usage, got %v", m.CacheWrite1HTokens)
	}
}

func TestResponseExtractor_SSE_Anthropic_CacheCreationDetails(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":200,\"cache_creation_input_tokens\":668,\"cache_creation\":{\"ephemeral_5m_input_tokens\":0,\"ephemeral_1h_input_tokens\":668}}}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 0 {
		t.Errorf("CacheWriteTokens: got %v, want 0", m.CacheWriteTokens)
	}
	if m.CacheWrite1HTokens == nil || *m.CacheWrite1HTokens != 668 {
		t.Errorf("CacheWrite1HTokens: got %v, want 668", m.CacheWrite1HTokens)
	}
}

func TestResponseExtractor_SSE_Anthropic_CacheCreationMissingDetailFallsBack(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":200,\"cache_creation_input_tokens\":668,\"cache_creation\":{\"ephemeral_5m_input_tokens\":0}}}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 668 {
		t.Errorf("CacheWriteTokens: got %v, want fallback 668", m.CacheWriteTokens)
	}
	if m.CacheWrite1HTokens != nil {
		t.Errorf("CacheWrite1HTokens should be nil when a detail is missing, got %v", m.CacheWrite1HTokens)
	}
}

func TestResponseExtractor_SSE_Anthropic_ToolUseTTFT(t *testing.T) {
	events := []string{
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\"}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.TTFTMs == nil {
		t.Fatal("expected TTFTMs to be set for tool_use content_block_start")
	}
}

func TestResponseExtractor_JSON_OpenAI(t *testing.T) {
	jsonData := `{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"Hi"}}],"usage":{"prompt_tokens":150,"completion_tokens":75,"prompt_tokens_details":{"cached_tokens":40}}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 110 {
		t.Errorf("InputTokens: got %v, want 110", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 75 {
		t.Errorf("OutputTokens: got %v, want 75", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 40 {
		t.Errorf("CacheReadTokens: got %v, want 40", m.CacheReadTokens)
	}
	if m.TTFTMs != nil {
		t.Errorf("TTFTMs should be nil for non-streaming JSON, got %v", m.TTFTMs)
	}
}

// TestResponseExtractor_JSON_OpenAIEmbedding pins that an OpenAI Embeddings
// response needs no dedicated extraction branch: its usage shape is
// {prompt_tokens, total_tokens}, and the existing prompt_tokens path already
// yields the input count. total_tokens is deliberately not read — for
// embeddings it is identical to prompt_tokens. There is no output and no first
// token, so OutputTokens and TTFTMs stay nil, which is what lands as NULL on
// the request row.
func TestResponseExtractor_JSON_OpenAIEmbedding(t *testing.T) {
	jsonData := `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.0023,-0.0092]}],"model":"text-embedding-3-small","usage":{"prompt_tokens":8,"total_tokens":8}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 8 {
		t.Errorf("InputTokens: got %v, want 8", m.InputTokens)
	}
	if m.OutputTokens != nil {
		t.Errorf("OutputTokens should be nil (embeddings have no output), got %v", *m.OutputTokens)
	}
	if m.CacheReadTokens != nil {
		t.Errorf("CacheReadTokens should be nil, got %v", *m.CacheReadTokens)
	}
	if m.CacheWriteTokens != nil {
		t.Errorf("CacheWriteTokens should be nil, got %v", *m.CacheWriteTokens)
	}
	if m.TTFTMs != nil {
		t.Errorf("TTFTMs should be nil for non-streaming JSON, got %v", *m.TTFTMs)
	}
	if m.InferredModel != "text-embedding-3-small" {
		t.Errorf("InferredModel: got %q, want text-embedding-3-small", m.InferredModel)
	}
	if m.InferredModelSource != db.InferredModelSourceResponse {
		t.Errorf("InferredModelSource: got %d, want %d", m.InferredModelSource, db.InferredModelSourceResponse)
	}
}

func TestResponseExtractor_JSON_Anthropic(t *testing.T) {
	jsonData := `{"id":"msg_1","type":"message","content":[{"type":"text","text":"Hi"}],"usage":{"input_tokens":300,"output_tokens":100,"cache_read_input_tokens":60,"cache_creation_input_tokens":15}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 300 {
		t.Errorf("InputTokens: got %v, want 300", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 100 {
		t.Errorf("OutputTokens: got %v, want 100", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 60 {
		t.Errorf("CacheReadTokens: got %v, want 60", m.CacheReadTokens)
	}
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 15 {
		t.Errorf("CacheWriteTokens: got %v, want 15", m.CacheWriteTokens)
	}
	if m.CacheWrite1HTokens != nil {
		t.Errorf("CacheWrite1HTokens should be nil for fallback usage, got %v", m.CacheWrite1HTokens)
	}
}

func TestResponseExtractor_JSON_Anthropic_CacheCreationDetails(t *testing.T) {
	jsonData := `{"id":"msg_1","type":"message","content":[{"type":"text","text":"Hi"}],"usage":{"input_tokens":300,"output_tokens":100,"cache_read_input_tokens":60,"cache_creation_input_tokens":668,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":668}}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 0 {
		t.Errorf("CacheWriteTokens: got %v, want 0", m.CacheWriteTokens)
	}
	if m.CacheWrite1HTokens == nil || *m.CacheWrite1HTokens != 668 {
		t.Errorf("CacheWrite1HTokens: got %v, want 668", m.CacheWrite1HTokens)
	}
}

func TestResponseExtractor_JSON_Anthropic_CacheCreationMissingDetailFallsBack(t *testing.T) {
	jsonData := `{"id":"msg_1","type":"message","content":[{"type":"text","text":"Hi"}],"usage":{"input_tokens":300,"output_tokens":100,"cache_creation_input_tokens":668,"cache_creation":{"ephemeral_1h_input_tokens":668}}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 668 {
		t.Errorf("CacheWriteTokens: got %v, want fallback 668", m.CacheWriteTokens)
	}
	if m.CacheWrite1HTokens != nil {
		t.Errorf("CacheWrite1HTokens should be nil when a detail is missing, got %v", m.CacheWrite1HTokens)
	}
}

func TestResponseExtractor_JSON_UnrecognizedFormat(t *testing.T) {
	jsonData := `{"some":"random","data":true}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens != nil || m.OutputTokens != nil || m.CacheReadTokens != nil || m.CacheWriteTokens != nil || m.CacheWrite1HTokens != nil || m.TTFTMs != nil {
		t.Errorf("expected all nil metrics for unrecognized JSON, got %+v", m)
	}
}

func TestResponseExtractor_SSE_DONESentinel(t *testing.T) {
	events := []string{
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	want := strings.Join(events, "")
	if string(got) != want {
		t.Errorf("bytes forwarded unchanged with [DONE]:\ngot:  %q\nwant: %q", string(got), want)
	}

	// Should have TTFT from the first event but no usage
	m := extractor.Metrics()
	if m.TTFTMs == nil {
		t.Fatal("expected TTFTMs to be set from first content event")
	}
	if m.InputTokens != nil {
		t.Errorf("InputTokens should be nil when no usage event, got %v", m.InputTokens)
	}
}

func TestResponseExtractor_SSE_OpenAIResponses_UsageAndTTFT(t *testing.T) {
	start := time.Now().Add(-100 * time.Millisecond)
	events := []string{
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n",
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\" world\"}\n\n",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"usage\":{\"input_tokens\":22,\"input_tokens_details\":{\"cached_tokens\":5},\"output_tokens\":42,\"output_tokens_details\":{\"reasoning_tokens\":17},\"total_tokens\":64}}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", start)

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.TTFTMs == nil {
		t.Fatal("expected TTFTMs to be set from response.output_text.delta")
	}
	if *m.TTFTMs < 50 {
		t.Errorf("TTFTMs too low: got %d, expected >= 50", *m.TTFTMs)
	}
	if m.InputTokens == nil || *m.InputTokens != 17 {
		t.Errorf("InputTokens: got %v, want 17", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 42 {
		t.Errorf("OutputTokens: got %v, want 42", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 5 {
		t.Errorf("CacheReadTokens: got %v, want 5", m.CacheReadTokens)
	}
	if m.CacheWriteTokens != nil {
		t.Errorf("CacheWriteTokens should be nil without cache_write_tokens, got %v", m.CacheWriteTokens)
	}
}

func TestResponseExtractor_SSE_OpenAIResponses_FunctionCallTTFT(t *testing.T) {
	events := []string{
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"arg\\\"\"}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.TTFTMs == nil {
		t.Fatal("expected TTFTMs to be set for function_call_arguments.delta")
	}
}

func TestResponseExtractor_JSON_OpenAIResponses(t *testing.T) {
	jsonData := `{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}],"usage":{"input_tokens":22,"input_tokens_details":{"cached_tokens":5},"output_tokens":40,"output_tokens_details":{"reasoning_tokens":15},"total_tokens":62}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 17 {
		t.Errorf("InputTokens: got %v, want 17", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 40 {
		t.Errorf("OutputTokens: got %v, want 40", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 5 {
		t.Errorf("CacheReadTokens: got %v, want 5", m.CacheReadTokens)
	}
	if m.CacheWriteTokens != nil {
		t.Errorf("CacheWriteTokens should be nil without cache_write_tokens, got %v", m.CacheWriteTokens)
	}
}

func TestResponseExtractor_SSE_OpenAI_CacheWrite(t *testing.T) {
	events := []string{
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n",
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":50,\"prompt_tokens_details\":{\"cached_tokens\":30,\"cache_write_tokens\":20}}}\n\n",
		"data: [DONE]\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 50 {
		t.Errorf("InputTokens: got %v, want 50", m.InputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 30 {
		t.Errorf("CacheReadTokens: got %v, want 30", m.CacheReadTokens)
	}
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 20 {
		t.Errorf("CacheWriteTokens: got %v, want 20", m.CacheWriteTokens)
	}
}

func TestResponseExtractor_JSON_OpenAI_CacheWrite(t *testing.T) {
	jsonData := `{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"Hi"}}],"usage":{"prompt_tokens":150,"completion_tokens":75,"prompt_tokens_details":{"cached_tokens":40,"cache_write_tokens":25}}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 85 {
		t.Errorf("InputTokens: got %v, want 85", m.InputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 40 {
		t.Errorf("CacheReadTokens: got %v, want 40", m.CacheReadTokens)
	}
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 25 {
		t.Errorf("CacheWriteTokens: got %v, want 25", m.CacheWriteTokens)
	}
}

func TestResponseExtractor_SSE_OpenAIResponses_CacheWrite(t *testing.T) {
	events := []string{
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n",
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"usage\":{\"input_tokens\":22,\"input_tokens_details\":{\"cached_tokens\":5,\"cache_write_tokens\":7},\"output_tokens\":42,\"total_tokens\":64}}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 10 {
		t.Errorf("InputTokens: got %v, want 10", m.InputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 5 {
		t.Errorf("CacheReadTokens: got %v, want 5", m.CacheReadTokens)
	}
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 7 {
		t.Errorf("CacheWriteTokens: got %v, want 7", m.CacheWriteTokens)
	}
}

func TestResponseExtractor_JSON_OpenAIResponses_CacheWrite(t *testing.T) {
	jsonData := `{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi"}]}],"usage":{"input_tokens":22,"input_tokens_details":{"cached_tokens":5,"cache_write_tokens":7},"output_tokens":40,"total_tokens":62}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 10 {
		t.Errorf("InputTokens: got %v, want 10", m.InputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 5 {
		t.Errorf("CacheReadTokens: got %v, want 5", m.CacheReadTokens)
	}
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 7 {
		t.Errorf("CacheWriteTokens: got %v, want 7", m.CacheWriteTokens)
	}
}

func TestResponseExtractor_SSE_Anthropic_CacheReadInMessageDelta(t *testing.T) {
	// mimo.sse: message_start has empty usage (no cache_read_input_tokens),
	// while message_delta carries cache_read_input_tokens in its own usage.
	data, err := os.ReadFile("../../fixtures/mimo.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	extractor := NewResponseExtractor(strings.NewReader(string(data)), "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 2000 {
		t.Errorf("InputTokens: got %v, want 2000", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 1822 {
		t.Errorf("OutputTokens: got %v, want 1822", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 59008 {
		t.Errorf("CacheReadTokens: got %v, want 59008", m.CacheReadTokens)
	}
}

func TestResponseExtractor_SSE_Anthropic_CacheReadWriteFixture(t *testing.T) {
	// anthropic-cache-read-write.sse: message_start carries the detailed
	// cache_creation breakdown (ephemeral_5m=0, ephemeral_1h=250), while
	// message_delta carries only the flat cache_creation_input_tokens=250.
	// The flat fallback in message_delta must NOT clobber the correctly
	// split values established in message_start.
	data, err := os.ReadFile("../../fixtures/anthropic-cache-read-write.sse")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	extractor := NewResponseExtractor(strings.NewReader(string(data)), "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 1 {
		t.Errorf("InputTokens: got %v, want 1", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 504 {
		t.Errorf("OutputTokens: got %v, want 504", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 124342 {
		t.Errorf("CacheReadTokens: got %v, want 124342", m.CacheReadTokens)
	}
	if m.CacheWriteTokens == nil || *m.CacheWriteTokens != 0 {
		t.Errorf("CacheWriteTokens: got %v, want 0", m.CacheWriteTokens)
	}
	if m.CacheWrite1HTokens == nil || *m.CacheWrite1HTokens != 250 {
		t.Errorf("CacheWrite1HTokens: got %v, want 250", m.CacheWrite1HTokens)
	}
}

func TestResponseExtractor_SSE_MultiLineData(t *testing.T) {
	// SSE spec: multiple data: lines in one event are joined with \n
	events := "data: {\"id\":\"1\",\ndata: \"choices\":[]}\n\n"
	inner := strings.NewReader(events)
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	// Should parse the concatenated payload {"id":"1",\n"choices":[]}
	// gjson can handle this
	m := extractor.Metrics()
	// No usage expected, just verify no panic/crash
	if m.InputTokens != nil {
		t.Errorf("InputTokens should be nil, got %v", m.InputTokens)
	}
}

func TestResponseExtractor_SSE_StreamError_Anthropic(t *testing.T) {
	events := []string{
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5}}}\n\n",
		"data: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"code\":null,\"message\":\"upstream connect error or disconnect/reset before headers. reset reason: connection termination\",\"param\":null},\"sequence_number\":3}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	want := "upstream connect error or disconnect/reset before headers. reset reason: connection termination"
	if got := extractor.StreamError(); got != want {
		t.Errorf("StreamError() = %q, want %q", got, want)
	}
	// Metrics still extracted alongside the error.
	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 5 {
		t.Errorf("InputTokens = %v, want 5", m.InputTokens)
	}
}

func TestResponseExtractor_SSE_StreamError_None(t *testing.T) {
	events := []string{
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n",
		"data: [DONE]\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	if got := extractor.StreamError(); got != "" {
		t.Errorf("StreamError() = %q, want empty", got)
	}
}

func TestResponseExtractor_SSE_StreamError_OpenAI(t *testing.T) {
	events := "data: {\"error\":{\"message\":\"rate limit exceeded\",\"type\":\"rate_limit_error\"}}\n\n"
	inner := strings.NewReader(events)
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	if got := extractor.StreamError(); got != "rate limit exceeded" {
		t.Errorf("StreamError() = %q, want %q", got, "rate limit exceeded")
	}
}

func TestResponseExtractor_SSE_StreamError_OpenAIResponsesFailed(t *testing.T) {
	events := "event: response.failed\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_533aab23ed564f6a90c6ad8bca884101\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"failed\",\"output\":[],\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"Concurrency limit exceeded for account, please retry later\"}}}\n\n"
	inner := strings.NewReader(events)
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	want := "Concurrency limit exceeded for account, please retry later"
	if got := extractor.StreamError(); got != want {
		t.Errorf("StreamError() = %q, want %q", got, want)
	}
}

func TestResponseExtractor_SSE_StreamError_OpenAIChoiceFinishReasonFixtures(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		wantError     string
		wantModel     string
		wantModelFrom int32
	}{
		{
			name:          "network error",
			path:          "../../fixtures/zai-stream-error.sse",
			wantError:     "network_error",
			wantModel:     "glm-5.2",
			wantModelFrom: db.InferredModelSourceResponse,
		},
		{
			name:          "context window exceeded",
			path:          "../../fixtures/zai-context-window-error.sse",
			wantError:     "model_context_window_exceeded",
			wantModel:     "glm-5v-turbo",
			wantModelFrom: db.InferredModelSourceResponse,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			extractor := NewResponseExtractor(strings.NewReader(string(data)), "text/event-stream", time.Now())

			got, err := io.ReadAll(extractor)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}

			if string(got) != string(data) {
				t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), string(data))
			}
			if got := extractor.StreamError(); got != tt.wantError {
				t.Errorf("StreamError() = %q, want %q", got, tt.wantError)
			}
			m := extractor.Metrics()
			if m.InferredModel != tt.wantModel {
				t.Errorf("InferredModel = %q, want %q", m.InferredModel, tt.wantModel)
			}
			if m.InferredModelSource != tt.wantModelFrom {
				t.Errorf("InferredModelSource = %d, want %d", m.InferredModelSource, tt.wantModelFrom)
			}
		})
	}
}

func TestResponseExtractor_SSE_StreamError_OpenAIChoiceFinishReasonNonErrors(t *testing.T) {
	tests := []struct {
		name    string
		choices string
	}{
		{name: "stop", choices: `[{"index":0,"finish_reason":"stop","delta":{"role":"assistant","content":""}}]`},
		{name: "length", choices: `[{"index":0,"finish_reason":"length","delta":{"role":"assistant","content":""}}]`},
		{name: "tool calls", choices: `[{"index":0,"finish_reason":"tool_calls","delta":{"role":"assistant","content":""}}]`},
		{name: "null", choices: `[{"index":0,"finish_reason":null,"delta":{"role":"assistant","content":""}}]`},
		{name: "number", choices: `[{"index":0,"finish_reason":1,"delta":{"role":"assistant","content":""}}]`},
		{name: "error in second choice", choices: `[{"index":0,"finish_reason":"stop","delta":{"role":"assistant","content":""}},{"index":1,"finish_reason":"network_error","delta":{"role":"assistant","content":""}}]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := `data: {"id":"chatcmpl-1","model":"gpt-5.5","choices":` + tt.choices + `}` + "\n\n"
			extractor := NewResponseExtractor(strings.NewReader(events), "text/event-stream", time.Now())

			_, _ = io.ReadAll(extractor)

			if got := extractor.StreamError(); got != "" {
				t.Errorf("StreamError() = %q, want empty", got)
			}
		})
	}
}

func TestResponseExtractor_SSE_StreamError_FirstWins(t *testing.T) {
	events := []string{
		"data: {\"type\":\"error\",\"error\":{\"message\":\"first error\"}}\n\n",
		"data: {\"type\":\"error\",\"error\":{\"message\":\"second error\"}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	if got := extractor.StreamError(); got != "first error" {
		t.Errorf("StreamError() = %q, want %q", got, "first error")
	}
}

func TestResponseExtractor_SSE_StreamError_FirstWinsOverRefusal(t *testing.T) {
	events := []string{
		"data: {\"type\":\"error\",\"error\":{\"message\":\"first error\"}}\n\n",
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"refusal\",\"stop_details\":{\"type\":\"refusal\",\"explanation\":\"blocked\"}}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	if got := extractor.StreamError(); got != "first error" {
		t.Errorf("StreamError() = %q, want %q", got, "first error")
	}
}

func TestResponseExtractor_SSE_StreamError_AnthropicRefusalFixture(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/anthropic-refusal.sse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	extractor := NewResponseExtractor(strings.NewReader(string(data)), "text/event-stream", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if string(got) != string(data) {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), string(data))
	}

	want := "This request was blocked as it seems to violate Anthropic's Terms of Service restrictions on reverse engineering or duplicating model outputs. To learn more, visit https://www.anthropic.com/legal/commercial-terms."
	if got := extractor.StreamError(); got != want {
		t.Errorf("StreamError() = %q, want %q", got, want)
	}
	// The stream still terminates normally; only the finish reason downstream
	// changes. Tokens are still billed — a refusal consumed the cache read.
	if !extractor.StreamCompleted() {
		t.Error("StreamCompleted() = false, want true")
	}
	m := extractor.Metrics()
	if m.OutputTokens == nil || *m.OutputTokens != 0 {
		t.Errorf("OutputTokens = %v, want 0", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 244090 {
		t.Errorf("CacheReadTokens = %v, want 244090", m.CacheReadTokens)
	}
}

func TestResponseExtractor_SSE_StreamError_AnthropicRefusalWithoutExplanation(t *testing.T) {
	tests := []struct {
		name  string
		delta string
	}{
		{name: "no stop_details", delta: `{"stop_reason":"refusal"}`},
		{name: "null stop_details", delta: `{"stop_reason":"refusal","stop_details":null}`},
		{name: "empty explanation", delta: `{"stop_reason":"refusal","stop_details":{"type":"refusal","explanation":""}}`},
		{name: "no explanation field", delta: `{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"reasoning_extraction"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := `data: {"type":"message_delta","delta":` + tt.delta + `,"usage":{"output_tokens":0}}` + "\n\n"
			extractor := NewResponseExtractor(strings.NewReader(events), "text/event-stream", time.Now())

			_, _ = io.ReadAll(extractor)

			if got := extractor.StreamError(); got != "refusal" {
				t.Errorf("StreamError() = %q, want %q", got, "refusal")
			}
		})
	}
}

func TestResponseExtractor_SSE_StreamError_AnthropicStopReasonNonRefusal(t *testing.T) {
	tests := []struct {
		name       string
		stopReason string
	}{
		{name: "end turn", stopReason: `"end_turn"`},
		{name: "max tokens", stopReason: `"max_tokens"`},
		{name: "tool use", stopReason: `"tool_use"`},
		{name: "stop sequence", stopReason: `"stop_sequence"`},
		{name: "null", stopReason: `null`},
		{name: "number", stopReason: `1`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := `data: {"type":"message_delta","delta":{"stop_reason":` + tt.stopReason + `,"stop_sequence":null},"usage":{"output_tokens":12}}` + "\n\n"
			extractor := NewResponseExtractor(strings.NewReader(events), "text/event-stream", time.Now())

			_, _ = io.ReadAll(extractor)

			if got := extractor.StreamError(); got != "" {
				t.Errorf("StreamError() = %q, want empty", got)
			}
		})
	}
}

func TestResponseExtractor_JSON_StreamError_AnthropicRefusal(t *testing.T) {
	body := `{"id":"msg_01Qk7TrEfBz4mNwYpLxDv2Hs","type":"message","role":"assistant","model":"claude-opus-4-7","content":[],` +
		`"stop_reason":"refusal","stop_sequence":null,` +
		`"stop_details":{"type":"refusal","category":"reasoning_extraction","explanation":"This request was blocked."},` +
		`"usage":{"input_tokens":7,"cache_read_input_tokens":244090,"output_tokens":0}}`
	extractor := NewResponseExtractor(strings.NewReader(body), "application/json", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != body {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), body)
	}

	if got := extractor.StreamError(); got != "This request was blocked." {
		t.Errorf("StreamError() = %q, want %q", got, "This request was blocked.")
	}
	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 7 {
		t.Errorf("InputTokens = %v, want 7", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 0 {
		t.Errorf("OutputTokens = %v, want 0", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 244090 {
		t.Errorf("CacheReadTokens = %v, want 244090", m.CacheReadTokens)
	}
}

func TestResponseExtractor_JSON_StreamError_AnthropicNonRefusal(t *testing.T) {
	body := `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-4-7",` +
		`"content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","stop_sequence":null,` +
		`"usage":{"input_tokens":7,"output_tokens":2}}`
	extractor := NewResponseExtractor(strings.NewReader(body), "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	if got := extractor.StreamError(); got != "" {
		t.Errorf("StreamError() = %q, want empty", got)
	}
}

func TestResponseExtractor_SSE_InferredProvider_OpenRouter(t *testing.T) {
	events := []string{
		"data: {\"id\":\"gen-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"nvidia/nemotron-3-ultra-550b-a55b-20260604:free\",\"provider\":\"Nvidia\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\"}}]}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredProvider != "Nvidia" {
		t.Errorf("InferredProvider: got %q, want %q", m.InferredProvider, "Nvidia")
	}
	if m.InferredModel != "nvidia/nemotron-3-ultra-550b-a55b-20260604:free" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "nvidia/nemotron-3-ultra-550b-a55b-20260604:free")
	}
}

func TestResponseExtractor_SSE_InferredProvider_BedrockMessageStart(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_bdrk_7xxvzn3y5guhlrkyxno2katnow5gsvlkk7hu3x3ddo6nwhily4ra\",\"model\":\"claude-opus-4-7\",\"role\":\"assistant\"}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredProvider != "Amazon Bedrock" {
		t.Errorf("InferredProvider: got %q, want %q", m.InferredProvider, "Amazon Bedrock")
	}
}

func TestResponseExtractor_SSE_InferredProvider_BedrockInvocationMetrics(t *testing.T) {
	events := []string{
		"event: message_stop\ndata: {\"type\":\"message_stop\",\"amazon-bedrock-invocationMetrics\":{\"inputTokenCount\":100,\"outputTokenCount\":91}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredProvider != "Amazon Bedrock" {
		t.Errorf("InferredProvider: got %q, want %q", m.InferredProvider, "Amazon Bedrock")
	}
}

func TestResponseExtractor_SSE_InferredProvider_FirstHitWins(t *testing.T) {
	// provider field appears first and should lock; later invocationMetrics is ignored.
	events := []string{
		"data: {\"provider\":\"OpenRouter\"}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\",\"amazon-bedrock-invocationMetrics\":{}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredProvider != "OpenRouter" {
		t.Errorf("InferredProvider: got %q, want %q", m.InferredProvider, "OpenRouter")
	}
}

func TestResponseExtractor_SSE_InferredModel_SignatureDecodes(t *testing.T) {
	model := "claude-opus-4-5-20251101"
	sig := buildSignaturePayload(model)
	events := []string{
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"" + sig + "\"}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != model {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, model)
	}
}

func TestResponseExtractor_SSE_InferredModel_SignaturePriorityOverModelField(t *testing.T) {
	model := "from-signature"
	sig := buildSignaturePayload(model)
	events := []string{
		"data: {\"model\":\"from-model\"}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"" + sig + "\"}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != model {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, model)
	}
}

func TestResponseExtractor_SSE_InferredModel_FallsBackToModelField(t *testing.T) {
	events := []string{
		"data: {\"model\":\"fallback-model\"}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != "fallback-model" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "fallback-model")
	}
}

func TestResponseExtractor_SSE_InferredModel_MessageModelField(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"claude-opus-4-8\",\"id\":\"msg_01Pfr7Jo77eNMGoFEumXYh9t\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != "claude-opus-4-8" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "claude-opus-4-8")
	}
	if m.InferredModelSource != db.InferredModelSourceResponse {
		t.Errorf("InferredModelSource: got %d, want response", m.InferredModelSource)
	}
}

func TestResponseExtractor_SSE_InferredModel_ResponseModelField(t *testing.T) {
	events := []string{
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5-codex\",\"status\":\"in_progress\"}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != "gpt-5-codex" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "gpt-5-codex")
	}
	if m.InferredModelSource != db.InferredModelSourceResponse {
		t.Errorf("InferredModelSource: got %d, want response", m.InferredModelSource)
	}
}

func TestResponseExtractor_SSE_InferredModel_TopLevelModelWins(t *testing.T) {
	events := []string{
		"data: {\"model\":\"top-level-model\"}\n\n",
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"message-model\"}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != "top-level-model" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "top-level-model")
	}
}

func TestResponseExtractor_SSE_InferredModel_BadSignatureFallsBack(t *testing.T) {
	events := []string{
		"data: {\"model\":\"fallback-model\"}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"not-base64!!!\"}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != "fallback-model" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "fallback-model")
	}
}

func TestResponseExtractor_SSE_InferredModel_SignatureNonASCIIFallsBack(t *testing.T) {
	model := "claude-opus-4\xff"
	sig := buildSignaturePayload(model)
	events := []string{
		"data: {\"model\":\"fallback-model\"}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"" + sig + "\"}}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != "fallback-model" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "fallback-model")
	}
}

func TestResponseExtractor_JSON_InferredModel_SignatureDecodes(t *testing.T) {
	model := "claude-sonnet-4-20251101"
	sig := buildSignaturePayload(model)
	jsonData := `{"id":"msg_1","type":"message","model":"json-model","content":[{"type":"thinking","thinking":"...","signature":"` + sig + `"},{"type":"text","text":"Hi"}],"usage":{"input_tokens":10,"output_tokens":5}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != model {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, model)
	}
}

func TestResponseExtractor_JSON_InferredModel_FallsBackToModelField(t *testing.T) {
	jsonData := `{"id":"msg_1","type":"message","model":"json-model","content":[{"type":"text","text":"Hi"}],"usage":{"input_tokens":10,"output_tokens":5}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredModel != "json-model" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "json-model")
	}
}

func TestResponseExtractor_JSON_InferredProvider_BedrockTopLevelID(t *testing.T) {
	jsonData := `{"id":"msg_bdrk_abc123","type":"message","model":"claude-opus-4-7","content":[{"type":"text","text":"Hi"}],"usage":{"input_tokens":10,"output_tokens":5}}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InferredProvider != "Amazon Bedrock" {
		t.Errorf("InferredProvider: got %q, want %q", m.InferredProvider, "Amazon Bedrock")
	}
}

func TestResponseExtractor_SignatureFixture_File(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/signature.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sig := strings.TrimSpace(string(data))

	extractor := &ResponseExtractor{}
	extractor.inferSignatureModel(sig)

	if extractor.sigModel == "" {
		t.Fatal("expected model to be inferred from fixture signature, got empty")
	}
	want := "claude-opus-4-8"
	if extractor.sigModel != want {
		t.Errorf("InferredModel from fixture: got %q, want %q", extractor.sigModel, want)
	}
}

func TestResponseExtractor_SSE_Gemini_Usage(t *testing.T) {
	start := time.Now().Add(-100 * time.Millisecond)
	events := []string{
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hello\"}]}}],\"usageMetadata\":{\"trafficType\":\"ON_DEMAND\"},\"modelVersion\":\"google/gemini-2.5-flash-lite\",\"responseId\":\"cAY8\"}\n\n",
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\" there! How\"}]}}],\"usageMetadata\":{\"trafficType\":\"ON_DEMAND\"},\"modelVersion\":\"google/gemini-2.5-flash-lite\",\"responseId\":\"cAY8\"}\n\n",
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\" can I help you today?\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":8,\"candidatesTokenCount\":11,\"totalTokenCount\":19,\"trafficType\":\"ON_DEMAND\"},\"modelVersion\":\"google/gemini-2.5-flash-lite\",\"responseId\":\"cAY8\"}\n\n",
	}
	// strings.NewReader, not chunkReader: these events exceed the read buffer
	// and chunkReader drops the tail of any chunk past the buffer's free space.
	inner := strings.NewReader(strings.Join(events, ""))
	extractor := NewResponseExtractor(inner, "text/event-stream", start)

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.TTFTMs == nil {
		t.Fatal("expected TTFTMs to be set from first content event")
	}
	if *m.TTFTMs < 50 {
		t.Errorf("TTFTMs too low: got %d, expected >= 50", *m.TTFTMs)
	}
	if m.InputTokens == nil || *m.InputTokens != 8 {
		t.Errorf("InputTokens: got %v, want 8", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 11 {
		t.Errorf("OutputTokens: got %v, want 11", m.OutputTokens)
	}
	if m.InferredModel != "google/gemini-2.5-flash-lite" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "google/gemini-2.5-flash-lite")
	}
	if m.InferredModelSource != db.InferredModelSourceResponse {
		t.Errorf("InferredModelSource: got %d, want response", m.InferredModelSource)
	}
}

func TestResponseExtractor_SSE_Gemini_EarlyUsageMetadataIgnored(t *testing.T) {
	events := []string{
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hello\"}]}}],\"usageMetadata\":{\"trafficType\":\"ON_DEMAND\"},\"modelVersion\":\"google/gemini-2.5-flash-lite\"}\n\n",
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\" there!\"}]}}],\"usageMetadata\":{\"trafficType\":\"ON_DEMAND\"},\"modelVersion\":\"google/gemini-2.5-flash-lite\"}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens != nil {
		t.Errorf("InputTokens should be nil when usageMetadata has no counts, got %v", m.InputTokens)
	}
	if m.OutputTokens != nil {
		t.Errorf("OutputTokens should be nil when usageMetadata has no counts, got %v", m.OutputTokens)
	}
}

func TestResponseExtractor_JSON_Gemini(t *testing.T) {
	jsonData := `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello there!"}]},"finishReason":"STOP","avgLogprobs":-0.0417}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":10,"totalTokenCount":18,"trafficType":"ON_DEMAND"},"modelVersion":"google/gemini-2.5-flash-lite","responseId":"iwY8"}`
	inner := strings.NewReader(jsonData)
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 8 {
		t.Errorf("InputTokens: got %v, want 8", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 10 {
		t.Errorf("OutputTokens: got %v, want 10", m.OutputTokens)
	}
	if m.InferredModel != "google/gemini-2.5-flash-lite" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "google/gemini-2.5-flash-lite")
	}
}

func TestResponseExtractor_SSE_Gemini_CachedAndThoughts(t *testing.T) {
	events := []string{
		"data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hi\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":100,\"cachedContentTokenCount\":40,\"candidatesTokenCount\":11,\"thoughtsTokenCount\":5,\"totalTokenCount\":116},\"modelVersion\":\"google/gemini-2.5-flash-lite\"}\n\n",
	}
	inner := &chunkReader{chunks: []string{strings.Join(events, "")}}
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 60 {
		t.Errorf("InputTokens: got %v, want 60", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 16 {
		t.Errorf("OutputTokens: got %v, want 16", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 40 {
		t.Errorf("CacheReadTokens: got %v, want 40", m.CacheReadTokens)
	}
}

func TestResponseExtractor_SSE_Gemini_CRLFFraming(t *testing.T) {
	// Google's Gemini endpoint frames SSE events with CRLF (\r\n\r\n), not LF.
	// Every chunk repeats usageMetadata with cumulative counts (last wins).
	start := time.Now().Add(-100 * time.Millisecond)
	events := []string{
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hello\"}],\"role\":\"model\"},\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":9,\"candidatesTokenCount\":1,\"totalTokenCount\":10},\"modelVersion\":\"gemini-2.5-flash-lite\",\"responseId\":\"f4U8\"}\r\n\r\n",
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" there!\"}],\"role\":\"model\"},\"finishReason\":\"STOP\",\"index\":0}],\"usageMetadata\":{\"promptTokenCount\":9,\"candidatesTokenCount\":10,\"totalTokenCount\":19},\"modelVersion\":\"gemini-2.5-flash-lite\",\"responseId\":\"f4U8\"}\r\n\r\n",
	}
	inner := strings.NewReader(strings.Join(events, ""))
	extractor := NewResponseExtractor(inner, "text/event-stream", start)

	_, _ = io.ReadAll(extractor)

	m := extractor.Metrics()
	if m.TTFTMs == nil {
		t.Fatal("expected TTFTMs to be set despite CRLF framing")
	}
	if m.InputTokens == nil || *m.InputTokens != 9 {
		t.Errorf("InputTokens: got %v, want 9", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 10 {
		t.Errorf("OutputTokens: got %v, want 10", m.OutputTokens)
	}
	if m.InferredModel != "gemini-2.5-flash-lite" {
		t.Errorf("InferredModel: got %q, want %q", m.InferredModel, "gemini-2.5-flash-lite")
	}
}

func TestResponseExtractor_SSE_CRLF_BytesForwardedUnchanged(t *testing.T) {
	// The CR-stripping is parse-only: bytes forwarded to the client must be
	// byte-for-byte identical, CRs included.
	sseData := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\r\n\r\ndata: [DONE]\r\n\r\n"
	inner := strings.NewReader(sseData)
	extractor := NewResponseExtractor(inner, "text/event-stream", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != sseData {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), sseData)
	}
}

func TestResponseExtractor_JSONArray_Gemini_Usage(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/d8u8kj0s9a291pp7cakg.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	extractor := NewResponseExtractor(strings.NewReader(string(data)), "application/json", time.Now())

	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 9 {
		t.Errorf("InputTokens: got %v, want 9", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 11 {
		t.Errorf("OutputTokens: got %v, want 11 (last wins)", m.OutputTokens)
	}
	if m.TTFTMs == nil {
		t.Errorf("TTFTMs: got nil, want recorded")
	}
	if m.InferredModel != "gemini-2.5-flash-lite" {
		t.Errorf("InferredModel: got %q, want gemini-2.5-flash-lite", m.InferredModel)
	}
	if m.InferredModelSource != db.InferredModelSourceResponse {
		t.Errorf("InferredModelSource: got %d, want %d", m.InferredModelSource, db.InferredModelSourceResponse)
	}
}

func TestResponseExtractor_JSONArray_Gemini_BytesForwardedUnchanged(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/d8u8kj0s9a291pp7cakg.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	extractor := NewResponseExtractor(strings.NewReader(string(data)), "application/json", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), string(data))
	}
}

func TestResponseExtractor_JSONArray_Gemini_AcrossReadCalls(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/d8u8kj0s9a291pp7cakg.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// Split into single-byte chunks to stress cross-Read state persistence.
	var chunks []string
	for _, b := range []byte(data) {
		chunks = append(chunks, string([]byte{b}))
	}
	inner := &chunkReader{chunks: chunks}
	extractor := NewResponseExtractor(inner, "application/json", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("bytes forwarded unchanged across reads:\ngot:  %q\nwant: %q", string(got), string(data))
	}

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 9 {
		t.Errorf("InputTokens: got %v, want 9", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 11 {
		t.Errorf("OutputTokens: got %v, want 11", m.OutputTokens)
	}
	if m.InferredModel != "gemini-2.5-flash-lite" {
		t.Errorf("InferredModel: got %q, want gemini-2.5-flash-lite", m.InferredModel)
	}
}

func TestResponseExtractor_JSONArray_Gemini_StringBraces(t *testing.T) {
	// Element text contains {, }, ", and ] — must not confuse value delimiting.
	jsonData := `[{"candidates":[{"content":{"parts":[{"text":"a{b}c,d]e\"f"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":7,"totalTokenCount":12},"modelVersion":"gemini-2.5-flash-lite"}]`
	extractor := NewResponseExtractor(strings.NewReader(jsonData), "application/json", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != jsonData {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), jsonData)
	}

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 5 {
		t.Errorf("InputTokens: got %v, want 5", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 7 {
		t.Errorf("OutputTokens: got %v, want 7", m.OutputTokens)
	}
}

func TestResponseExtractor_JSON_Gemini_SingleObjectStillWorks(t *testing.T) {
	jsonData := `{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":11,"totalTokenCount":20},"modelVersion":"gemini-2.5-flash-lite"}`
	extractor := NewResponseExtractor(strings.NewReader(jsonData), "application/json", time.Now())

	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 9 {
		t.Errorf("InputTokens: got %v, want 9", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 11 {
		t.Errorf("OutputTokens: got %v, want 11", m.OutputTokens)
	}
	if m.InferredModel != "gemini-2.5-flash-lite" {
		t.Errorf("InferredModel: got %q, want gemini-2.5-flash-lite", m.InferredModel)
	}
}

func TestStreamCompletedOpenAIDone(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"
	extractor := NewResponseExtractor(strings.NewReader(sse), "text/event-stream", time.Now())

	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !extractor.StreamCompleted() {
		t.Error("StreamCompleted: got false, want true after [DONE]")
	}
}

func TestStreamCompletedAnthropicMessageStop(t *testing.T) {
	sse := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	extractor := NewResponseExtractor(strings.NewReader(sse), "text/event-stream", time.Now())

	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !extractor.StreamCompleted() {
		t.Error("StreamCompleted: got false, want true after message_stop")
	}
}

func TestStreamCompletedOpenAIResponses(t *testing.T) {
	tests := []struct {
		name  string
		event string
	}{
		{"completed", "response.completed"},
		{"incomplete", "response.incomplete"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sse := "data: {\"type\":\"" + tt.event + "\",\"response\":{\"id\":\"resp_1\"}}\n\n"
			extractor := NewResponseExtractor(strings.NewReader(sse), "text/event-stream", time.Now())

			if _, err := io.ReadAll(extractor); err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if !extractor.StreamCompleted() {
				t.Errorf("StreamCompleted: got false, want true after %s", tt.event)
			}
		})
	}
}

func TestStreamCompletedGeminiSSE(t *testing.T) {
	first := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hello\"}],\"role\":\"model\"}}]}\n\n"
	last := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"!\"}],\"role\":\"model\"},\"finishReason\":\"STOP\"}]}\n\n"

	extractor := NewResponseExtractor(strings.NewReader(first), "text/event-stream", time.Now())
	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if extractor.StreamCompleted() {
		t.Error("StreamCompleted: got true, want false for a chunk without finishReason")
	}

	extractor = NewResponseExtractor(strings.NewReader(first+last), "text/event-stream", time.Now())
	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !extractor.StreamCompleted() {
		t.Error("StreamCompleted: got false, want true after a finishReason chunk")
	}
}

func TestStreamCompletedGeminiJSONArray(t *testing.T) {
	t.Run("elementFinishReason", func(t *testing.T) {
		// Fed in chunks that split mid-element, and stopping before the closing
		// ']' so only the element's finishReason can set the flag.
		chunks := []string{
			`[{"candidates":[{"content":{"parts":[{"text":"Hel`,
			`lo"}],"role":"model"}}]},`,
			`{"candidates":[{"content":{"parts":[{"text":"!"}],"role":"model"},"fini`,
			`shReason":"STOP"}]}`,
		}
		extractor := NewResponseExtractor(&chunkReader{chunks: chunks}, "application/json", time.Now())
		if _, err := io.ReadAll(extractor); err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !extractor.StreamCompleted() {
			t.Error("StreamCompleted: got false, want true after an element with finishReason")
		}
	})

	t.Run("arrayClosedWithoutFinishReason", func(t *testing.T) {
		data := `[{"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"}}]}]`
		extractor := NewResponseExtractor(strings.NewReader(data), "application/json", time.Now())
		if _, err := io.ReadAll(extractor); err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if !extractor.StreamCompleted() {
			t.Error("StreamCompleted: got false, want true once the array closes")
		}
	})
}

func TestStreamNotCompletedTruncatedSSE(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"He\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n"
	extractor := NewResponseExtractor(strings.NewReader(sse), "text/event-stream", time.Now())

	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if extractor.StreamCompleted() {
		t.Error("StreamCompleted: got true, want false for a stream missing its terminating event")
	}
}

func TestStreamNotCompletedNonStreamJSON(t *testing.T) {
	jsonData := `{"id":"chatcmpl-1","choices":[{"message":{"content":"Hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":9,"completion_tokens":11}}`
	extractor := NewResponseExtractor(strings.NewReader(jsonData), "application/json", time.Now())

	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if extractor.StreamCompleted() {
		t.Error("StreamCompleted: got true, want false for a non-stream JSON body")
	}
}

func buildSignaturePayload(model string) string {
	// Build a protobuf message whose [2][1][6] path contains `model`.
	inner := protowire.AppendTag(nil, 6, protowire.BytesType)
	inner = protowire.AppendBytes(inner, []byte(model))
	middle := protowire.AppendTag(nil, 1, protowire.BytesType)
	middle = protowire.AppendBytes(middle, inner)
	outer := protowire.AppendTag(nil, 2, protowire.BytesType)
	outer = protowire.AppendBytes(outer, middle)
	return base64.StdEncoding.EncodeToString(outer)
}

// codexSSE mimics a ChatGPT Codex response: OpenAI Responses SSE with no
// Content-Type header at all.
const codexSSE = "event: response.created\n" +
	"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5-codex\"}}\n\n" +
	"event: response.output_text.delta\n" +
	"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hi\"}\n\n" +
	"event: response.completed\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":120,\"input_tokens_details\":{\"cached_tokens\":20},\"output_tokens\":37}}}\n\n"

func TestResponseExtractor_NoContentType_SniffsSSE(t *testing.T) {
	inner := &chunkReader{chunks: []string{codexSSE}}
	extractor := NewResponseExtractor(inner, "", time.Now().Add(-50*time.Millisecond))

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != codexSSE {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), codexSSE)
	}

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 100 {
		t.Errorf("InputTokens: got %v, want 100", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 37 {
		t.Errorf("OutputTokens: got %v, want 37", m.OutputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 20 {
		t.Errorf("CacheReadTokens: got %v, want 20", m.CacheReadTokens)
	}
	if m.TTFTMs == nil {
		t.Error("TTFTMs: got nil, want recorded")
	}
	if !extractor.StreamCompleted() {
		t.Error("StreamCompleted: got false, want true")
	}
}

func TestResponseExtractor_NoContentType_SniffsSSEAcrossReadCalls(t *testing.T) {
	// The first chunk is too short to decide; the second settles it mid-line, so
	// the withheld bytes have to be replayed intact for the second event's
	// "data:" line — and therefore TTFT — to survive.
	head := "event: response.created\nda"
	inner := &chunkReader{chunks: []string{"ev", head[2:], codexSSE[len(head):]}}
	extractor := NewResponseExtractor(inner, "", time.Now().Add(-50*time.Millisecond))

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != codexSSE {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), codexSSE)
	}

	m := extractor.Metrics()
	if m.OutputTokens == nil || *m.OutputTokens != 37 {
		t.Errorf("OutputTokens: got %v, want 37", m.OutputTokens)
	}
	if m.TTFTMs == nil {
		t.Error("TTFTMs: got nil, want recorded (buffered prefix must be replayed)")
	}
}

func TestResponseExtractor_NoContentType_FallsBackToJSON(t *testing.T) {
	jsonData := `{"id":"chatcmpl-1","usage":{"prompt_tokens":10,"completion_tokens":20}}`
	extractor := NewResponseExtractor(strings.NewReader(jsonData), "", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != jsonData {
		t.Errorf("bytes forwarded unchanged:\ngot:  %q\nwant: %q", string(got), jsonData)
	}

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 10 {
		t.Errorf("InputTokens: got %v, want 10", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 20 {
		t.Errorf("OutputTokens: got %v, want 20", m.OutputTokens)
	}
}

func TestResponseExtractor_NoContentType_JSONArrayStreamStillWorks(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/d8u8kj0s9a291pp7cakg.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	extractor := NewResponseExtractor(strings.NewReader(string(data)), "", time.Now())

	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 9 {
		t.Errorf("InputTokens: got %v, want 9", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 11 {
		t.Errorf("OutputTokens: got %v, want 11", m.OutputTokens)
	}
}

func TestResponseExtractor_NoContentType_ShortBody(t *testing.T) {
	// Shorter than the longest candidate prefix: sniffing never decides on its
	// own and EOF must force the JSON path rather than drop the body.
	extractor := NewResponseExtractor(strings.NewReader("null"), "", time.Now())

	got, err := io.ReadAll(extractor)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "null" {
		t.Errorf("bytes forwarded unchanged: got %q", string(got))
	}
	if m := extractor.Metrics(); m.InputTokens != nil || m.OutputTokens != nil {
		t.Errorf("expected no metrics from a body without usage, got %+v", m)
	}
}

func TestResponseExtractor_JSONContentType_DoesNotSniff(t *testing.T) {
	// The header is present and says JSON: take it at face value, no guessing.
	extractor := NewResponseExtractor(strings.NewReader(codexSSE), "application/json", time.Now())

	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	m := extractor.Metrics()
	if m.OutputTokens != nil {
		t.Errorf("OutputTokens: got %v, want nil (SSE body must not be parsed as SSE)", m.OutputTokens)
	}
	if extractor.StreamCompleted() {
		t.Error("StreamCompleted: got true, want false")
	}
}

// ---------------------------------------------------------------------------
// tool_usage extraction
// ---------------------------------------------------------------------------

// toolUsageEqual compares two entry slices field by field. A zero counter is
// indistinguishable from an unreported one by design, so plain equality is the
// whole comparison.
func toolUsageEqual(a, b []ToolUsageEntry) bool {
	return slices.Equal(a, b)
}

func TestResponseExtractor_ToolUsage(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        []ToolUsageEntry
	}{
		{
			// OpenAI Responses nests tool_usage in the event's response envelope,
			// as a sibling of response.usage — not at the event's top level.
			name:        "openai responses sse completed event",
			contentType: "text/event-stream",
			body: "event: response.completed\n" +
				`data: {"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":50},` +
				`"tool_usage":{"image_gen":{"input_tokens":222,"input_tokens_details":{"image_tokens":0,"text_tokens":222},` +
				`"output_tokens":1630,"output_tokens_details":{"image_tokens":1630,"text_tokens":0},"total_tokens":1852},` +
				`"web_search":{"num_requests":1}}}}` + "\n\n",
			want: []ToolUsageEntry{
				{Name: "image_gen", InputTokens: 222, OutputTokens: 1630},
				{Name: "web_search", NumRequests: 1},
			},
		},
		{
			// Chat Completions / Anthropic / Gemini keep usage at the payload's
			// top level, so tool_usage sits there too.
			name:        "top-level sibling of a top-level usage",
			contentType: "text/event-stream",
			body: `data: {"usage":{"prompt_tokens":100,"completion_tokens":50},` +
				`"tool_usage":{"web_search":{"num_requests":1}}}` + "\n\n",
			want: []ToolUsageEntry{{Name: "web_search", NumRequests: 1}},
		},
		{
			name:        "non-stream json body",
			contentType: "application/json",
			body: `{"usage":{"prompt_tokens":10,"completion_tokens":20},` +
				`"tool_usage":{"image_gen":{"input_tokens":222,"output_tokens":1630,"num_images":2},"web_search":{"num_requests":3}}}`,
			want: []ToolUsageEntry{
				{Name: "image_gen", InputTokens: 222, OutputTokens: 1630, NumImages: 2},
				{Name: "web_search", NumRequests: 3},
			},
		},
		{
			name:        "anthropic sse message_delta - top-level path is format-agnostic",
			contentType: "text/event-stream",
			body: "event: message_delta\n" +
				`data: {"type":"message_delta","usage":{"output_tokens":42},"tool_usage":{"web_search":{"num_requests":2}}}` + "\n\n",
			want: []ToolUsageEntry{{Name: "web_search", NumRequests: 2}},
		},
		{
			name:        "absent",
			contentType: "application/json",
			body:        `{"usage":{"prompt_tokens":10,"completion_tokens":20}}`,
			want:        nil,
		},
		{
			name:        "not an object - array",
			contentType: "application/json",
			body:        `{"tool_usage":[{"name":"web_search"}]}`,
			want:        nil,
		},
		{
			name:        "not an object - string",
			contentType: "application/json",
			body:        `{"tool_usage":"web_search"}`,
			want:        nil,
		},
		{
			name:        "not an object - null",
			contentType: "application/json",
			body:        `{"tool_usage":null}`,
			want:        nil,
		},
		{
			name:        "non-object value is skipped, siblings survive",
			contentType: "application/json",
			body:        `{"tool_usage":{"a":1,"web_search":{"num_requests":1}}}`,
			want:        []ToolUsageEntry{{Name: "web_search", NumRequests: 1}},
		},
		{
			name:        "empty usage object is dropped - nothing ran",
			contentType: "application/json",
			body:        `{"tool_usage":{"web_search":{}}}`,
			want:        nil,
		},
		{
			name:        "whitelisted field of the wrong type counts as not reported",
			contentType: "application/json",
			body:        `{"tool_usage":{"web_search":{"num_requests":"1","input_tokens":true,"output_tokens":5}}}`,
			want:        []ToolUsageEntry{{Name: "web_search", OutputTokens: 5}},
		},
		{
			name:        "an all-zero tool is dropped entirely",
			contentType: "application/json",
			body:        `{"tool_usage":{"image_gen":{"input_tokens":0,"output_tokens":0,"num_images":0}}}`,
			want:        nil,
		},
		{
			name:        "zero counters are omitted, non-zero siblings survive",
			contentType: "application/json",
			body:        `{"tool_usage":{"image_gen":{"input_tokens":0,"output_tokens":1630,"num_images":0}}}`,
			want:        []ToolUsageEntry{{Name: "image_gen", OutputTokens: 1630}},
		},
		{
			name:        "an all-zero tool is dropped, a used sibling is kept",
			contentType: "application/json",
			body:        `{"tool_usage":{"image_gen":{"input_tokens":0,"output_tokens":0},"web_search":{"num_requests":1}}}`,
			want:        []ToolUsageEntry{{Name: "web_search", NumRequests: 1}},
		},
		{
			// image_gen usage is declared as tools[].type "image_generation" —
			// the two vocabularies disagree, hence the alias table.
			name:        "model comes from the matching tools[] declaration",
			contentType: "text/event-stream",
			body: `data: {"type":"response.completed","response":{"tool_usage":{"image_gen":{"num_images":1,` +
				`"input_tokens":222,"output_tokens":1630},"web_search":{"num_requests":2}},` +
				`"tools":[{"type":"web_search","search_context_size":"medium"},` +
				`{"type":"image_generation","model":"gpt-image-2-codex","size":"auto"}]}}` + "\n\n",
			want: []ToolUsageEntry{
				{Name: "image_gen", Model: "gpt-image-2-codex", InputTokens: 222, OutputTokens: 1630, NumImages: 1},
				// web_search matches verbatim but declares no model.
				{Name: "web_search", NumRequests: 2},
			},
		},
		{
			name:        "no tools array - entries keep no model",
			contentType: "application/json",
			body:        `{"tool_usage":{"image_gen":{"num_images":1}}}`,
			want:        []ToolUsageEntry{{Name: "image_gen", NumImages: 1}},
		},
		{
			name:        "declared tool without a model field",
			contentType: "application/json",
			body: `{"tool_usage":{"image_gen":{"num_images":1}},` +
				`"tools":[{"type":"image_generation","size":"auto"}]}`,
			want: []ToolUsageEntry{{Name: "image_gen", NumImages: 1}},
		},
		{
			name:        "a non-string model is ignored",
			contentType: "application/json",
			body: `{"tool_usage":{"image_gen":{"num_images":1}},` +
				`"tools":[{"type":"image_generation","model":7}]}`,
			want: []ToolUsageEntry{{Name: "image_gen", NumImages: 1}},
		},
		{
			name:        "tools is not an array",
			contentType: "application/json",
			body:        `{"tool_usage":{"image_gen":{"num_images":1}},"tools":"image_generation"}`,
			want:        []ToolUsageEntry{{Name: "image_gen", NumImages: 1}},
		},
		{
			// A declared model does not rescue a tool that never ran.
			name:        "declared image_generation with all-zero usage stays dropped",
			contentType: "application/json",
			body: `{"tool_usage":{"image_gen":{"input_tokens":0,"output_tokens":0,"num_images":0}},` +
				`"tools":[{"type":"image_generation","model":"gpt-image-2-codex"}]}`,
			want: nil,
		},
		{
			name:        "entry order follows the upstream object",
			contentType: "application/json",
			body:        `{"tool_usage":{"z":{"num_requests":1},"a":{"num_requests":2}}}`,
			want: []ToolUsageEntry{
				{Name: "z", NumRequests: 1},
				{Name: "a", NumRequests: 2},
			},
		},
		{
			name:        "last non-empty occurrence wins",
			contentType: "text/event-stream",
			body: `data: {"tool_usage":{"web_search":{"num_requests":1}}}` + "\n\n" +
				`data: {"tool_usage":{"image_gen":{"num_images":4}}}` + "\n\n",
			want: []ToolUsageEntry{{Name: "image_gen", NumImages: 4}},
		},
		{
			name:        "a later empty tool_usage does not clear the earlier one",
			contentType: "text/event-stream",
			body: `data: {"tool_usage":{"web_search":{"num_requests":1}}}` + "\n\n" +
				`data: {"tool_usage":{}}` + "\n\n",
			want: []ToolUsageEntry{{Name: "web_search", NumRequests: 1}},
		},
		{
			// Verbatim from a Codex /responses stream (request dag9d1gs9a269lib21cg):
			// tool_usage repeats on created / in_progress / completed and only the
			// last one carries the final counts, so last-wins is what converges.
			name:        "codex responses stream repeats tool_usage until completed",
			contentType: "text/event-stream",
			body: `data: {"type":"response.created","response":{"tool_usage":{"image_gen":{"input_tokens":0,` +
				`"input_tokens_details":{"image_tokens":0,"text_tokens":0},"output_tokens":0,` +
				`"output_tokens_details":{"image_tokens":0,"text_tokens":0},"total_tokens":0},` +
				`"web_search":{"num_requests":0}}}}` + "\n\n" +
				`data: {"type":"response.completed","response":{"tool_usage":{"image_gen":{"input_tokens":0,` +
				`"input_tokens_details":{"image_tokens":0,"text_tokens":0},"output_tokens":0,` +
				`"output_tokens_details":{"image_tokens":0,"text_tokens":0},"total_tokens":0},` +
				`"web_search":{"num_requests":1}},"usage":{"input_tokens":33601,` +
				`"input_tokens_details":{"cache_write_tokens":0,"cached_tokens":3712},"output_tokens":545,` +
				`"output_tokens_details":{"reasoning_tokens":370},"total_tokens":34146}}}` + "\n\n",
			want: []ToolUsageEntry{{Name: "web_search", NumRequests: 1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := NewResponseExtractor(strings.NewReader(tt.body), tt.contentType, time.Now())
			if _, err := io.ReadAll(extractor); err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if got := extractor.Metrics().ToolUsage; !toolUsageEqual(got, tt.want) {
				t.Errorf("ToolUsage:\ngot:  %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

func TestResponseExtractor_ToolUsage_DoesNotDisturbUsage(t *testing.T) {
	body := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":50},` +
		`"tool_usage":{"web_search":{"num_requests":1}}}}` + "\n\n"
	extractor := NewResponseExtractor(strings.NewReader(body), "text/event-stream", time.Now())
	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	m := extractor.Metrics()
	if m.InputTokens == nil || *m.InputTokens != 100 {
		t.Errorf("InputTokens: got %v, want 100", m.InputTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 50 {
		t.Errorf("OutputTokens: got %v, want 50", m.OutputTokens)
	}
}

// TestResponseExtractor_UsageRaw covers which usageRawPaths entry each upstream
// format hits and the whole-object last-wins rule. The want values are compared
// byte-for-byte: the column is meant to be verbatim upstream JSON.
func TestResponseExtractor_UsageRaw(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{
			// The raw object keeps the prompt_tokens_details breakdown that
			// normalization folds into two counters.
			name:        "openai chat non-stream hits the top-level usage",
			contentType: "application/json",
			body:        `{"usage":{"prompt_tokens":120,"prompt_tokens_details":{"cached_tokens":20},"completion_tokens":50,"total_tokens":170}}`,
			want:        `{"prompt_tokens":120,"prompt_tokens_details":{"cached_tokens":20},"completion_tokens":50,"total_tokens":170}`,
		},
		{
			name:        "openai responses sse hits response.usage",
			contentType: "text/event-stream",
			body: `data: {"type":"response.completed","response":{"usage":{"input_tokens":33601,` +
				`"input_tokens_details":{"cached_tokens":3712},"output_tokens":545,` +
				`"output_tokens_details":{"reasoning_tokens":370}}}}` + "\n\n",
			want: `{"input_tokens":33601,"input_tokens_details":{"cached_tokens":3712},"output_tokens":545,"output_tokens_details":{"reasoning_tokens":370}}`,
		},
		{
			name:        "anthropic non-stream hits the top-level usage",
			contentType: "application/json",
			body:        `{"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":5}}`,
			want:        `{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":5}`,
		},
		{
			name:        "anthropic message_start alone hits message.usage",
			contentType: "text/event-stream",
			body: `data: {"type":"message_start","message":{"usage":{"input_tokens":7,` +
				`"cache_read_input_tokens":3}}}` + "\n\n",
			want: `{"input_tokens":7,"cache_read_input_tokens":3}`,
		},
		{
			// Whole-object overwrite: message_delta's usage replaces the
			// message_start object rather than merging into it, so the input /
			// cache breakdown does not survive.
			name:        "anthropic message_delta replaces message_start's usage wholesale",
			contentType: "text/event-stream",
			body: `data: {"type":"message_start","message":{"usage":{"input_tokens":7,"cache_read_input_tokens":3}}}` + "\n\n" +
				`data: {"type":"message_delta","usage":{"output_tokens":42}}` + "\n\n",
			want: `{"output_tokens":42}`,
		},
		{
			name:        "gemini hits usageMetadata",
			contentType: "text/event-stream",
			body:        `data: {"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3,"trafficType":"ON_DEMAND"}}` + "\n\n",
			want:        `{"promptTokenCount":8,"candidatesTokenCount":3,"trafficType":"ON_DEMAND"}`,
		},
		{
			// Gemini's early chunks carry a count-less usageMetadata; the final
			// chunk's complete one replaces it.
			name:        "gemini count-less first chunk is replaced by the final one",
			contentType: "text/event-stream",
			body: `data: {"usageMetadata":{"trafficType":"ON_DEMAND"}}` + "\n\n" +
				`data: {"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3,"trafficType":"ON_DEMAND"}}` + "\n\n",
			want: `{"promptTokenCount":8,"candidatesTokenCount":3,"trafficType":"ON_DEMAND"}`,
		},
		{
			name:        "gemini json array stream",
			contentType: "application/json",
			body:        `[{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":8}}]`,
			want:        `{"promptTokenCount":8}`,
		},
		{
			name:        "absent",
			contentType: "application/json",
			body:        `{"id":"x"}`,
			want:        "",
		},
		{
			// Chat Completions sends usage: null on every incremental frame.
			name:        "null is not a hit",
			contentType: "text/event-stream",
			body:        `data: {"choices":[{"delta":{"content":"hi"}}],"usage":null}` + "\n\n",
			want:        "",
		},
		{
			name:        "empty object is not a hit",
			contentType: "application/json",
			body:        `{"usage":{}}`,
			want:        "",
		},
		{
			name:        "non-object is not a hit",
			contentType: "application/json",
			body:        `{"usage":"none"}`,
			want:        "",
		},
		{
			name:        "a later empty usage does not clear the earlier one",
			contentType: "text/event-stream",
			body: `data: {"usage":{"prompt_tokens":10,"completion_tokens":20}}` + "\n\n" +
				`data: {"usage":null}` + "\n\n",
			want: `{"prompt_tokens":10,"completion_tokens":20}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := NewResponseExtractor(strings.NewReader(tt.body), tt.contentType, time.Now())
			if _, err := io.ReadAll(extractor); err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if got := string(extractor.Metrics().UsageRaw); got != tt.want {
				t.Errorf("UsageRaw:\ngot:  %s\nwant: %s", got, tt.want)
			}
		})
	}
}

// TestResponseExtractor_UsageRaw_DoesNotDisturbTokens pins the two paths as
// independent: the raw object loses message_start's counters to the whole-object
// overwrite while the per-field token accumulation keeps them.
func TestResponseExtractor_UsageRaw_DoesNotDisturbTokens(t *testing.T) {
	body := `data: {"type":"message_start","message":{"usage":{"input_tokens":7,"cache_read_input_tokens":3}}}` + "\n\n" +
		`data: {"type":"message_delta","usage":{"output_tokens":42}}` + "\n\n"
	extractor := NewResponseExtractor(strings.NewReader(body), "text/event-stream", time.Now())
	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	m := extractor.Metrics()
	if got := string(m.UsageRaw); got != `{"output_tokens":42}` {
		t.Errorf("UsageRaw: got %s, want {\"output_tokens\":42}", got)
	}
	if m.InputTokens == nil || *m.InputTokens != 7 {
		t.Errorf("InputTokens: got %v, want 7", m.InputTokens)
	}
	if m.CacheReadTokens == nil || *m.CacheReadTokens != 3 {
		t.Errorf("CacheReadTokens: got %v, want 3", m.CacheReadTokens)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 42 {
		t.Errorf("OutputTokens: got %v, want 42", m.OutputTokens)
	}
}

// TestResponseExtractor_ToolUsageRaw covers the raw tool usage capture, whose
// point is exactly the information normalization discards.
func TestResponseExtractor_ToolUsageRaw(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
		wantNorm    []ToolUsageEntry
	}{
		{
			// The whole reason the raw column exists: a Codex response reports a
			// zero-filled image_gen every time, which normalization drops.
			name:        "an all-zero tool survives in raw while normalization drops it",
			contentType: "application/json",
			body:        `{"tool_usage":{"image_gen":{"input_tokens":0,"output_tokens":0,"num_images":0}}}`,
			want:        `{"image_gen":{"input_tokens":0,"output_tokens":0,"num_images":0}}`,
			wantNorm:    nil,
		},
		{
			name:        "openai responses sse hits response.tool_usage",
			contentType: "text/event-stream",
			body:        `data: {"type":"response.completed","response":{"tool_usage":{"web_search":{"num_requests":1}}}}` + "\n\n",
			want:        `{"web_search":{"num_requests":1}}`,
			wantNorm:    []ToolUsageEntry{{Name: "web_search", NumRequests: 1}},
		},
		{
			// An empty object counts as "not reported" on both paths, so a
			// top-level one no longer masks the response envelope.
			name:        "an empty top-level tool_usage does not mask response.tool_usage",
			contentType: "text/event-stream",
			body:        `data: {"tool_usage":{},"response":{"tool_usage":{"web_search":{"num_requests":2}}}}` + "\n\n",
			want:        `{"web_search":{"num_requests":2}}`,
			wantNorm:    []ToolUsageEntry{{Name: "web_search", NumRequests: 2}},
		},
		{
			// Last non-empty wins, matching the normalized column.
			name:        "responses stream keeps the last occurrence",
			contentType: "text/event-stream",
			body: `data: {"type":"response.created","response":{"tool_usage":{"web_search":{"num_requests":0}}}}` + "\n\n" +
				`data: {"type":"response.completed","response":{"tool_usage":{"web_search":{"num_requests":1}}}}` + "\n\n",
			want:     `{"web_search":{"num_requests":1}}`,
			wantNorm: []ToolUsageEntry{{Name: "web_search", NumRequests: 1}},
		},
		{
			name:        "absent",
			contentType: "application/json",
			body:        `{"usage":{"prompt_tokens":10}}`,
			want:        "",
			wantNorm:    nil,
		},
		{
			name:        "empty object is not a hit",
			contentType: "application/json",
			body:        `{"tool_usage":{}}`,
			want:        "",
			wantNorm:    nil,
		},
		{
			name:        "non-object is not a hit",
			contentType: "application/json",
			body:        `{"tool_usage":[{"name":"web_search"}]}`,
			want:        "",
			wantNorm:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := NewResponseExtractor(strings.NewReader(tt.body), tt.contentType, time.Now())
			if _, err := io.ReadAll(extractor); err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			m := extractor.Metrics()
			if got := string(m.ToolUsageRaw); got != tt.want {
				t.Errorf("ToolUsageRaw:\ngot:  %s\nwant: %s", got, tt.want)
			}
			if !toolUsageEqual(m.ToolUsage, tt.wantNorm) {
				t.Errorf("ToolUsage:\ngot:  %+v\nwant: %+v", m.ToolUsage, tt.wantNorm)
			}
		})
	}
}

func TestResponseExtractor_ToolUsage_GeminiJSONArrayStream(t *testing.T) {
	body := `[{"candidates":[{"content":{"parts":[{"text":"hi"}]}}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":3},` +
		`"tool_usage":{"web_search":{"num_requests":1}}}]`
	extractor := NewResponseExtractor(strings.NewReader(body), "application/json", time.Now())
	if _, err := io.ReadAll(extractor); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	m := extractor.Metrics()
	want := []ToolUsageEntry{{Name: "web_search", NumRequests: 1}}
	if !toolUsageEqual(m.ToolUsage, want) {
		t.Errorf("ToolUsage:\ngot:  %+v\nwant: %+v", m.ToolUsage, want)
	}
	if m.OutputTokens == nil || *m.OutputTokens != 3 {
		t.Errorf("OutputTokens: got %v, want 3", m.OutputTokens)
	}
}

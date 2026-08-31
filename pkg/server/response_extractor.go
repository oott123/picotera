package server

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"time"

	"picotera/pkg/db"

	"github.com/go-json-experiment/json/jsontext"
	"github.com/tidwall/gjson"
)

// ResponseMetrics holds extracted TTFT, token usage, and inferred provider/model
// from a provider response.
type ResponseMetrics struct {
	TTFTMs             *int64
	InputTokens        *int64
	OutputTokens       *int64
	CacheReadTokens    *int64
	CacheWriteTokens   *int64
	CacheWrite1HTokens *int64
	InferredProvider   string
	InferredModel      string
	// InferredModelSource is a db.InferredModelSource* enum value describing
	// where InferredModel came from (signature vs response model field).
	InferredModelSource int32
}

// ResponseExtractor wraps an io.Reader and inspects bytes as they flow through,
// extracting TTFT, token usage, and inferred provider/model from SSE or JSON
// provider responses.
type ResponseExtractor struct {
	inner        io.Reader
	mode         string // "sse" or "json"
	startTime    time.Time
	metrics      ResponseMetrics
	ttftRecorded bool

	// SSE: line buffer for reassembling events across Read() boundaries
	lineBuf []byte

	// JSON: accumulate full body for post-stream parsing
	jsonBuf []byte

	// JSON shape: 0 = undetermined, '[' = array stream (incremental), '{' =
	// single object (buffered). Determined from the first non-whitespace byte.
	jsonShape byte

	// jaBuf holds unconsumed bytes of a JSON array stream (never includes the
	// leading '['). Mirrors lineBuf: accumulate → delimit elements → discard.
	jaBuf []byte

	// streamError holds the first error seen in a response payload (empty means
	// no error): an error event in an SSE data payload, or an Anthropic refusal
	// stop_reason in either an SSE data payload or a non-stream JSON body.
	// Detected independently of metrics.
	streamError string

	// streamCompleted records whether the upstream stream reached its
	// terminating event (OpenAI [DONE], Anthropic message_stop, OpenAI
	// Responses response.completed/incomplete, Gemini finishReason). Sticky.
	streamCompleted bool

	// Inference accumulators. Once non-empty they are locked (first hit wins).
	inferredProvider string
	sigModel         string
	respModel        string
}

// NewResponseExtractor creates a new extractor. contentType is the upstream
// response Content-Type header. startTime is when the upstream request was sent.
func NewResponseExtractor(inner io.Reader, contentType string, startTime time.Time) *ResponseExtractor {
	mode := "json"
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") {
		mode = "sse"
	}
	return &ResponseExtractor{
		inner:     inner,
		mode:      mode,
		startTime: startTime,
	}
}

// Metrics returns the extracted metrics. Call after the Read loop finishes.
func (e *ResponseExtractor) Metrics() ResponseMetrics {
	e.metrics.InferredProvider = e.inferredProvider
	if e.sigModel != "" {
		e.metrics.InferredModel = e.sigModel
		e.metrics.InferredModelSource = db.InferredModelSourceSignature
	} else if e.respModel != "" {
		e.metrics.InferredModel = e.respModel
		e.metrics.InferredModelSource = db.InferredModelSourceResponse
	} else {
		e.metrics.InferredModel = ""
		e.metrics.InferredModelSource = db.InferredModelSourceUnknown
	}
	return e.metrics
}

// StreamError returns the first error detected in a response payload, or "" if
// the response carried no error. Call after the Read loop.
func (e *ResponseExtractor) StreamError() string {
	return e.streamError
}

// StreamCompleted reports whether the upstream stream reached its terminating
// event. Call after the Read loop. Always false for non-stream JSON bodies.
func (e *ResponseExtractor) StreamCompleted() bool {
	return e.streamCompleted
}

// Read implements io.Reader. Bytes are forwarded to the caller unchanged.
// SSE bytes are also fed into the line buffer for event parsing.
// JSON bytes are accumulated for post-stream extraction.
func (e *ResponseExtractor) Read(p []byte) (int, error) {
	n, err := e.inner.Read(p)
	if n > 0 {
		chunk := p[:n]
		switch e.mode {
		case "sse":
			// Strip raw CR so CRLF-framed SSE (Google's Gemini API uses
			// \r\n\r\n event boundaries) parses identically to LF-framed.
			// Per the SSE spec, lines are delimited by CR, LF, or CRLF and a
			// data field value cannot contain a raw CR, so dropping CR from the
			// parse buffer is lossless. lineBuf is parse-only; the bytes
			// forwarded to the client (p) are untouched.
			for _, b := range chunk {
				if b != '\r' {
					e.lineBuf = append(e.lineBuf, b)
				}
			}
			e.processSSEBuffer()
		case "json":
			e.feedJSON(chunk)
		}
	}
	if err == io.EOF && e.mode == "json" && e.jsonShape != '[' && len(e.jsonBuf) > 0 {
		e.extractJSONMetrics()
	}
	return n, err
}

// processSSEBuffer scans the line buffer for complete SSE events (delimited by \n\n),
// processes each event, and removes processed bytes from the buffer.
func (e *ResponseExtractor) processSSEBuffer() {
	for {
		idx := bytesIndex(e.lineBuf, "\n\n")
		if idx == -1 {
			break
		}
		eventBytes := e.lineBuf[:idx]
		e.lineBuf = e.lineBuf[idx+2:]
		e.processSSEEvent(eventBytes)
	}
}

// processSSEEvent parses a single SSE event and extracts metrics.
func (e *ResponseExtractor) processSSEEvent(eventBytes []byte) {
	// Extract data: lines and concatenate (per SSE spec, multi-line data is joined with \n)
	var dataPayloads []string
	lines := strings.Split(string(eventBytes), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataPayloads = append(dataPayloads, strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, "data:") {
			dataPayloads = append(dataPayloads, strings.TrimPrefix(line, "data:"))
		}
	}
	if len(dataPayloads) == 0 {
		return
	}
	payload := strings.Join(dataPayloads, "\n")

	// [DONE] sentinel: terminating event for OpenAI Chat Completions and
	// compatible providers. Mark completion before skipping the payload.
	if payload == "[DONE]" {
		e.streamCompleted = true
		return
	}

	// Detect in-stream errors (HTTP 200 with an error event). Independent of
	// metric extraction — metrics already pulled from the stream still count.
	e.detectStreamError(payload)
	e.detectStreamCompletion(payload)

	// Try OpenAI Chat Completions format
	e.extractOpenAISSE(payload)
	// Try OpenAI Responses format
	e.extractOpenAIResponsesSSE(payload)
	// Try Anthropic format
	e.extractAnthropicSSE(payload)
	// Try Gemini format
	e.extractGeminiSSE(payload)

	// Infer provider/model from this payload.
	e.inferProvider(payload)
	e.inferModelField(payload)
	e.inferSignatureDeltaFromSSE(payload)
}

// inferSignatureDeltaFromSSE detects Anthropic signature_delta events and
// tries to decode the first signature seen.
func (e *ResponseExtractor) inferSignatureDeltaFromSSE(payload string) {
	if e.sigModel != "" {
		return
	}
	result := gjson.Parse(payload)
	if result.Get("type").String() != "content_block_delta" {
		return
	}
	if result.Get("delta.type").String() != "signature_delta" {
		return
	}
	if sig := result.Get("delta.signature").String(); sig != "" {
		e.inferSignatureModel(sig)
	}
}

// detectStreamError records the first stream error found in an SSE data
// payload. OpenAI Responses uses response.error.message on response.failed;
// Anthropic Messages, OpenAI Chat Completions, and Gemini native error shapes
// use error.message. Anthropic also refuses to produce content with an HTTP 200
// message_delta carrying delta.stop_reason = "refusal". Some OpenAI Chat
// compatible providers return errors as strict choices[].finish_reason values.
func (e *ResponseExtractor) detectStreamError(payload string) {
	if e.streamError != "" {
		return
	}
	if v := gjson.Get(payload, "response.error.message"); v.Exists() && v.Type == gjson.String && v.String() != "" {
		e.streamError = v.String()
		return
	}
	if v := gjson.Get(payload, "error.message"); v.Exists() && v.Type == gjson.String && v.String() != "" {
		e.streamError = v.String()
		return
	}
	// A top-level "delta" object is unique to Anthropic among the supported
	// formats: OpenAI Chat nests delta under choices[], Gemini has none.
	e.detectRefusal(gjson.Get(payload, "delta.stop_reason"), gjson.Get(payload, "delta.stop_details"))
	if e.streamError != "" {
		return
	}
	finishReason := gjson.Get(payload, "choices.0.finish_reason")
	if !finishReason.Exists() || finishReason.Type != gjson.String {
		return
	}
	switch finishReason.String() {
	case "network_error", "model_context_window_exceeded":
		e.streamError = finishReason.String()
	}
}

// detectRefusal records an Anthropic refusal stop_reason as a stream error.
// stopReason / stopDetails are the payload's stop_reason field and its sibling
// stop_details object — delta.* in a streaming message_delta, top-level in a
// non-stream body. First error wins, matching detectStreamError. stop_details is
// optional in the Anthropic contract, so a missing explanation falls back to the
// bare "refusal" marker.
func (e *ResponseExtractor) detectRefusal(stopReason, stopDetails gjson.Result) {
	if e.streamError != "" {
		return
	}
	if stopReason.Type != gjson.String || stopReason.String() != "refusal" {
		return
	}
	if v := stopDetails.Get("explanation"); v.Type == gjson.String && v.String() != "" {
		e.streamError = v.String()
		return
	}
	e.streamError = "refusal"
}

// detectStreamCompletion marks the stream complete when an SSE data payload
// carries the terminating event of any supported upstream format. The OpenAI
// Chat Completions [DONE] sentinel is handled by its caller (it is not JSON).
// response.failed is deliberately absent: it always carries
// response.error.message, so detectStreamError already covers it and the
// resulting finish reason is overridden to FinishReasonStreamError anyway.
func (e *ResponseExtractor) detectStreamCompletion(payload string) {
	if e.streamCompleted {
		return
	}
	result := gjson.Parse(payload)
	switch result.Get("type").String() {
	case "message_stop", "response.completed", "response.incomplete":
		e.streamCompleted = true
		return
	}
	if v := result.Get("candidates.0.finishReason"); v.Exists() && v.Type == gjson.String && v.String() != "" {
		e.streamCompleted = true
	}
}

func (e *ResponseExtractor) extractOpenAISSE(payload string) {
	result := gjson.Parse(payload)

	// TTFT: first content or tool_calls delta
	if !e.ttftRecorded {
		content := result.Get("choices.0.delta.content")
		reasoning := result.Get("choices.0.delta.reasoning")
		reasoningContent := result.Get("choices.0.delta.reasoning_content")
		toolCalls := result.Get("choices.0.delta.tool_calls")
		if (content.Exists()) || (reasoning.Exists()) || (reasoningContent.Exists()) || toolCalls.Exists() {
			ttft := time.Since(e.startTime).Milliseconds()
			e.metrics.TTFTMs = &ttft
			e.ttftRecorded = true
		}
	}

	// Usage
	usage := result.Get("usage")
	if usage.Exists() {
		e.setOpenAIInputTokens(usage)
		if v := usage.Get("completion_tokens"); v.Exists() {
			val := v.Int()
			e.metrics.OutputTokens = &val
		}
	}
}

func (e *ResponseExtractor) extractOpenAIResponsesSSE(payload string) {
	result := gjson.Parse(payload)
	eventType := result.Get("type").String()

	// Only process OpenAI Responses API events
	if !strings.HasPrefix(eventType, "response.") {
		return
	}

	// TTFT: first output text or function call delta
	if !e.ttftRecorded {
		if eventType == "response.output_text.delta" || eventType == "response.function_call_arguments.delta" || eventType == "response.output_item.added" {
			ttft := time.Since(e.startTime).Milliseconds()
			e.metrics.TTFTMs = &ttft
			e.ttftRecorded = true
		}
	}

	// Usage from response.completed
	if eventType == "response.completed" {
		usage := result.Get("response.usage")
		if usage.Exists() {
			e.setOpenAIInputTokens(usage)
			if v := usage.Get("output_tokens"); v.Exists() {
				val := v.Int()
				e.metrics.OutputTokens = &val
			}
		}
	}
}

func (e *ResponseExtractor) extractAnthropicSSE(payload string) {
	result := gjson.Parse(payload)
	eventType := result.Get("type").String()

	// TTFT: first content_block_delta with text_delta, or content_block_start with tool_use
	if !e.ttftRecorded {
		if eventType == "content_block_delta" || eventType == "content_block_start" {
			ttft := time.Since(e.startTime).Milliseconds()
			e.metrics.TTFTMs = &ttft
			e.ttftRecorded = true
		}
	}

	// Usage from message_start (input tokens, cache tokens)
	if eventType == "message_start" {
		msgUsage := result.Get("message.usage")
		if v := msgUsage.Get("input_tokens"); v.Exists() {
			val := v.Int()
			e.metrics.InputTokens = &val
		}
		if v := msgUsage.Get("cache_read_input_tokens"); v.Exists() {
			val := v.Int()
			e.metrics.CacheReadTokens = &val
		}
		e.extractAnthropicCacheCreation(msgUsage)
	}

	// Usage from message_delta (output tokens, cache tokens)
	if eventType == "message_delta" {
		if v := result.Get("usage.output_tokens"); v.Exists() {
			val := v.Int()
			e.metrics.OutputTokens = &val
		}
		if v := result.Get("usage.cache_read_input_tokens"); v.Exists() {
			val := v.Int()
			e.metrics.CacheReadTokens = &val
		}
		e.extractAnthropicCacheCreation(result.Get("usage"))
	}
}

func (e *ResponseExtractor) extractGeminiSSE(payload string) {
	result := gjson.Parse(payload)

	// TTFT: first chunk carrying generated content parts.
	if !e.ttftRecorded && result.Get("candidates.0.content.parts").Exists() {
		ttft := time.Since(e.startTime).Milliseconds()
		e.metrics.TTFTMs = &ttft
		e.ttftRecorded = true
	}

	// Usage: only the final chunk carries token counts; early chunks have a
	// usageMetadata with just trafficType and must be skipped (never written 0).
	// The last chunk with counts overwrites earlier values (last wins).
	e.setGeminiUsage(result.Get("usageMetadata"))
}

// feedJSON dispatches a JSON-mode chunk by shape. The first non-whitespace byte
// determines the shape: a top-level '[' is a Gemini JSON array stream (processed
// incrementally), anything else is a single object (buffered for post-stream
// parsing). A top-level array uniquely identifies the array-stream form —
// non-streaming generateContent never returns a top-level array — so a byte
// probe suffices and is provider-agnostic.
func (e *ResponseExtractor) feedJSON(chunk []byte) {
	switch e.jsonShape {
	case '[':
		e.feedJSONArray(chunk)
	case '{':
		e.jsonBuf = append(e.jsonBuf, chunk...)
	default: // 0: shape not yet determined
		for i, b := range chunk {
			if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
				continue
			}
			if b == '[' {
				e.jsonShape = '['
				e.feedJSONArray(chunk[i+1:])
			} else {
				e.jsonShape = '{'
				e.jsonBuf = append(e.jsonBuf, chunk...)
			}
			return
		}
		// Whole chunk was whitespace; keep it (harmless) and wait for more.
		e.jsonBuf = append(e.jsonBuf, chunk...)
	}
}

// feedJSONArray incrementally scans a Gemini JSON array stream. It accumulates
// bytes into jaBuf and emits each complete top-level element as soon as its
// bytes have all arrived, then drops the consumed prefix. Element boundaries are
// detected by jsontext.Decoder.ReadValue (a truncated value returns
// io.ErrUnexpectedEOF), so string contents (braces, commas, escapes) and nesting
// are handled by the library, not a hand-rolled state machine. jaBuf never
// includes the leading '['; its memory bound is a single largest element.
func (e *ResponseExtractor) feedJSONArray(data []byte) {
	e.jaBuf = append(e.jaBuf, data...)
	cur := 0
	for {
		// Skip leading whitespace, then an optional single element separator ','.
		for cur < len(e.jaBuf) && isJSONSpace(e.jaBuf[cur]) {
			cur++
		}
		if cur < len(e.jaBuf) && e.jaBuf[cur] == ',' {
			cur++
			for cur < len(e.jaBuf) && isJSONSpace(e.jaBuf[cur]) {
				cur++
			}
		}
		if cur >= len(e.jaBuf) {
			break
		}
		if e.jaBuf[cur] == ']' {
			// Array closed; the stream ended normally. Ignore any trailing bytes.
			e.streamCompleted = true
			e.jaBuf = nil
			return
		}
		dec := jsontext.NewDecoder(bytes.NewReader(e.jaBuf[cur:]))
		val, err := dec.ReadValue()
		if err != nil {
			// io.ErrUnexpectedEOF: element truncated, wait for more bytes.
			// io.EOF: no more data this round. Other: stop scanning this stream.
			break
		}
		e.processGeminiArrayElement(val)
		cur += int(dec.InputOffset())
	}
	e.jaBuf = append(e.jaBuf[:0], e.jaBuf[cur:]...)
}

// processGeminiArrayElement extracts metrics from one complete Gemini array
// element, reusing the same field semantics as the SSE path.
func (e *ResponseExtractor) processGeminiArrayElement(elem []byte) {
	result := gjson.ParseBytes(elem)
	payload := string(elem)

	if !e.ttftRecorded && result.Get("candidates.0.content.parts").Exists() {
		ttft := time.Since(e.startTime).Milliseconds()
		e.metrics.TTFTMs = &ttft
		e.ttftRecorded = true
	}

	e.setGeminiUsage(result.Get("usageMetadata"))
	e.inferModelField(payload)
	e.detectStreamError(payload)
	e.detectStreamCompletion(payload)
	e.inferProvider(payload)
}

// isJSONSpace reports whether b is JSON insignificant whitespace.
func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// setGeminiUsage maps Gemini usageMetadata onto metrics. It only assigns fields
// that actually exist, so an early chunk's count-less usageMetadata is a no-op.
// Shared by the SSE and JSON paths.
func (e *ResponseExtractor) setGeminiUsage(usage gjson.Result) {
	if !usage.Exists() {
		return
	}

	cached := usage.Get("cachedContentTokenCount")
	if prompt := usage.Get("promptTokenCount"); prompt.Exists() {
		in := prompt.Int()
		if cached.Exists() && cached.Int() > 0 {
			in -= cached.Int()
			c := cached.Int()
			e.metrics.CacheReadTokens = &c
		}
		e.metrics.InputTokens = &in
	}
	if candidates := usage.Get("candidatesTokenCount"); candidates.Exists() {
		out := candidates.Int()
		if thoughts := usage.Get("thoughtsTokenCount"); thoughts.Exists() {
			out += thoughts.Int()
		}
		e.metrics.OutputTokens = &out
	}
}

// extractJSONMetrics parses the accumulated JSON body and extracts usage metrics.
func (e *ResponseExtractor) extractJSONMetrics() {
	result := gjson.ParseBytes(e.jsonBuf)

	// Infer provider/model from the accumulated JSON body.
	e.inferProvider(result.String())
	e.inferModelField(result.String())
	e.inferSignatureModelFromJSON(result)

	// Try OpenAI Chat Completions / Responses format.
	// setOpenAIInputTokens handles both prefixes (prompt_tokens* and
	// input_tokens*) and sets InputTokens / CacheReadTokens / CacheWriteTokens.
	usage := result.Get("usage")
	e.setOpenAIInputTokens(usage)
	if v := result.Get("usage.completion_tokens"); v.Exists() {
		val := v.Int()
		e.metrics.OutputTokens = &val
	}
	if e.metrics.OutputTokens == nil {
		if v := result.Get("usage.output_tokens"); v.Exists() {
			val := v.Int()
			e.metrics.OutputTokens = &val
		}
	}

	// Try Anthropic format (only sets if above didn't find fields)
	if e.metrics.InputTokens == nil {
		if v := result.Get("usage.input_tokens"); v.Exists() {
			val := v.Int()
			e.metrics.InputTokens = &val
		}
	}
	if e.metrics.OutputTokens == nil {
		if v := result.Get("usage.output_tokens"); v.Exists() {
			val := v.Int()
			e.metrics.OutputTokens = &val
		}
	}
	if e.metrics.CacheReadTokens == nil {
		if v := result.Get("usage.cache_read_input_tokens"); v.Exists() {
			val := v.Int()
			e.metrics.CacheReadTokens = &val
		}
	}
	if e.metrics.CacheWriteTokens == nil {
		e.extractAnthropicCacheCreation(result.Get("usage"))
	}

	e.detectRefusal(result.Get("stop_reason"), result.Get("stop_details"))

	// Try Gemini format (only fills metrics the formats above didn't set).
	geminiUsage := result.Get("usageMetadata")
	cached := geminiUsage.Get("cachedContentTokenCount")
	if e.metrics.InputTokens == nil {
		if v := geminiUsage.Get("promptTokenCount"); v.Exists() {
			in := v.Int()
			if cached.Exists() && cached.Int() > 0 {
				in -= cached.Int()
			}
			e.metrics.InputTokens = &in
		}
	}
	if e.metrics.OutputTokens == nil {
		if v := geminiUsage.Get("candidatesTokenCount"); v.Exists() {
			out := v.Int()
			if thoughts := geminiUsage.Get("thoughtsTokenCount"); thoughts.Exists() {
				out += thoughts.Int()
			}
			e.metrics.OutputTokens = &out
		}
	}
	if e.metrics.CacheReadTokens == nil {
		if cached.Exists() && cached.Int() > 0 {
			c := cached.Int()
			e.metrics.CacheReadTokens = &c
		}
	}
}

// inferProvider extracts the upstream provider identity from a payload.
// Rules, first match wins and locks:
//  1. top-level "provider" field is a non-empty string;
//  2. message id (SSE "message.id", JSON "id") starts with "msg_bdrk_";
//  3. payload contains an "amazon-bedrock-invocationMetrics" field.
func (e *ResponseExtractor) inferProvider(payload string) {
	if e.inferredProvider != "" {
		return
	}
	result := gjson.Parse(payload)
	if v := result.Get("provider"); v.Exists() && v.Type == gjson.String && v.String() != "" {
		e.inferredProvider = v.String()
		return
	}
	msgID := result.Get("message.id").String()
	if msgID == "" {
		msgID = result.Get("id").String()
	}
	if strings.HasPrefix(msgID, "msg_bdrk_") {
		e.inferredProvider = "Amazon Bedrock"
		return
	}
	if result.Get("amazon-bedrock-invocationMetrics").Exists() {
		e.inferredProvider = "Amazon Bedrock"
	}
}

// inferModelField records the first non-empty "model" or "message.model" field seen.
func (e *ResponseExtractor) inferModelField(payload string) {
	if e.respModel != "" {
		return
	}
	for _, path := range []string{"model", "message.model", "modelVersion"} {
		if v := gjson.Get(payload, path); v.Exists() && v.Type == gjson.String && v.String() != "" {
			e.respModel = v.String()
			return
		}
	}
}

// inferSignatureModel decodes a base64-encoded thinking signature, navigates
// the protobuf wire format to field path [2][1][6], and if the bytes are
// printable ASCII uses them as the inferred model. First valid signature wins.
func (e *ResponseExtractor) inferSignatureModel(sig string) {
	if e.sigModel != "" {
		return
	}
	data, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return
	}
	val, ok := protoNavigate(data, []int{2, 1, 6})
	if !ok || !isPrintableASCII(val) {
		return
	}
	e.sigModel = string(val)
}

// inferSignatureModelFromJSON scans an Anthropic-style non-streaming JSON body
// for thinking content blocks and tries to decode the first signature found.
func (e *ResponseExtractor) inferSignatureModelFromJSON(result gjson.Result) {
	if e.sigModel != "" {
		return
	}
	result.Get("content").ForEach(func(_, value gjson.Result) bool {
		if value.Get("type").String() == "thinking" {
			if sig := value.Get("signature").String(); sig != "" {
				e.inferSignatureModel(sig)
				return false
			}
		}
		return true
	})
}

func (e *ResponseExtractor) extractAnthropicCacheCreation(usage gjson.Result) {
	if !usage.Exists() {
		return
	}

	cacheCreation := usage.Get("cache_creation")
	ephemeral5m := cacheCreation.Get("ephemeral_5m_input_tokens")
	ephemeral1h := cacheCreation.Get("ephemeral_1h_input_tokens")
	if ephemeral5m.Exists() && ephemeral1h.Exists() {
		cacheWrite := ephemeral5m.Int()
		cacheWrite1h := ephemeral1h.Int()
		e.metrics.CacheWriteTokens = &cacheWrite
		e.metrics.CacheWrite1HTokens = &cacheWrite1h
		return
	}

	// Flat total only — don't clobber a previously-extracted breakdown.
	// Why: message_delta repeats cache_creation_input_tokens but often omits
	// the cache_creation breakdown that message_start already supplied.
	if e.metrics.CacheWriteTokens != nil {
		return
	}
	if v := usage.Get("cache_creation_input_tokens"); v.Exists() {
		val := v.Int()
		e.metrics.CacheWriteTokens = &val
	}
}

func (e *ResponseExtractor) setOpenAIInputTokens(usage gjson.Result) {
	if !usage.Exists() {
		return
	}

	total := usage.Get("prompt_tokens")
	details := usage.Get("prompt_tokens_details")
	if !total.Exists() {
		total = usage.Get("input_tokens")
		details = usage.Get("input_tokens_details")
	}
	if !total.Exists() {
		return
	}

	val := total.Int()
	if cached := details.Get("cached_tokens"); cached.Exists() {
		read := cached.Int()
		e.metrics.CacheReadTokens = &read
		val -= read
	}
	if cw := details.Get("cache_write_tokens"); cw.Exists() {
		write := cw.Int()
		e.metrics.CacheWriteTokens = &write
		val -= write
	}
	e.metrics.InputTokens = &val
}

// bytesIndex finds the index of sep in buf, or -1 if not found.
func bytesIndex(buf []byte, sep string) int {
	sepBytes := []byte(sep)
	for i := 0; i <= len(buf)-len(sepBytes); i++ {
		match := true
		for j := range sepBytes {
			if buf[i+j] != sepBytes[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

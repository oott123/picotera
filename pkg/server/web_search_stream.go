package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// webSearchSSETransformer wraps an Anthropic Messages SSE stream and inlines
// web_search emulation: tool_use(web_search) blocks are buffered, the
// accumulated input JSON is dispatched to the search backend, and a
// synthesized server_tool_use + web_search_tool_result pair is emitted to the
// client.
//
// content_block indices on every passthrough event after an expansion are
// shifted by +1 per expansion to keep the client-facing index space
// monotonically increasing.
//
// stop_reason on message_delta is rewritten from "tool_use" to "pause_turn"
// when every tool_use seen during this turn was a web_search call.
type webSearchSSETransformer struct {
	upstream io.ReadCloser
	pr       *io.PipeReader
	pw       *io.PipeWriter
	ctx      context.Context
	wsCtx    *webSearchContext
	h        *gatewayHandler

	upstreamBuf []byte

	state sseState

	bufUpstreamIdx     int64
	bufServerToolUseID string
	bufInputJSON       bytes.Buffer

	idxOffset      int64
	webSearchCalls int
	otherToolCalls int

	holdPauseTurn       bool
	outerContentBlocks  []json.RawMessage
	blockBuilders       map[int64]*streamingBlock
	outerUsage          map[string]any
	pendingMessageDelta []byte
	pendingMessageStop  []byte
}

// streamingBlock accumulates incremental content for one output-index block.
type streamingBlock struct {
	blockType string
	raw       json.RawMessage // content_block_start payload
	textBuf   strings.Builder
	inputBuf  strings.Builder
}

type sseState int

const (
	ssePassthrough sseState = iota
	sseBuffering
)

func newWebSearchSSETransformer(ctx context.Context, upstream io.ReadCloser, wsCtx *webSearchContext, h *gatewayHandler, holdPauseTurn bool) *webSearchSSETransformer {
	pr, pw := io.Pipe()
	t := &webSearchSSETransformer{
		upstream:      upstream,
		pr:            pr,
		pw:            pw,
		ctx:           ctx,
		wsCtx:         wsCtx,
		h:             h,
		holdPauseTurn: holdPauseTurn,
		blockBuilders: make(map[int64]*streamingBlock),
		outerUsage:    make(map[string]any),
	}
	go t.run()
	return t
}

func (t *webSearchSSETransformer) Read(p []byte) (int, error) { return t.pr.Read(p) }

func (t *webSearchSSETransformer) Close() error {
	_ = t.pr.Close()
	return t.upstream.Close()
}

func (t *webSearchSSETransformer) run() {
	defer func() { _ = t.pw.Close() }()
	buf := make([]byte, 8*1024)
	for {
		n, err := t.upstream.Read(buf)
		if n > 0 {
			t.feedChunk(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

func (t *webSearchSSETransformer) feedChunk(chunk []byte) {
	t.upstreamBuf = append(t.upstreamBuf, chunk...)
	for {
		idx := bytesIndex(t.upstreamBuf, "\n\n")
		if idx == -1 {
			return
		}
		evt := append([]byte(nil), t.upstreamBuf[:idx]...)
		t.upstreamBuf = t.upstreamBuf[idx+2:]
		t.handleEvent(evt)
	}
}

func (t *webSearchSSETransformer) handleEvent(evt []byte) {
	eventName, dataPayload := parseSSEFrame(evt)
	if dataPayload == "" {
		t.writeRaw(evt)
		return
	}
	parsed := gjson.Parse(dataPayload)
	eventType := parsed.Get("type").Str
	if eventName == "" {
		eventName = eventType
	}

	switch t.state {
	case sseBuffering:
		switch eventType {
		case "content_block_delta":
			if parsed.Get("index").Int() != t.bufUpstreamIdx {
				t.writeAdjusted(eventName, parsed)
				return
			}
			if pj := parsed.Get("delta.partial_json"); pj.Exists() {
				t.bufInputJSON.WriteString(pj.Str)
			}
			return
		case "content_block_stop":
			if parsed.Get("index").Int() != t.bufUpstreamIdx {
				t.writeAdjusted(eventName, parsed)
				return
			}
			t.flushBufferedWebSearch()
			return
		default:
			t.writeAdjusted(eventName, parsed)
			return
		}
	case ssePassthrough:
		switch eventType {
		case "content_block_start":
			if t.isWebSearchToolUseStart(parsed) {
				t.beginBuffering(parsed)
				return
			}
			if parsed.Get("content_block.type").Str == "tool_use" {
				t.otherToolCalls++
			}
			t.writeAdjusted(eventName, parsed)
			t.onContentBlockStart(parsed)
		case "content_block_delta":
			t.writeAdjusted(eventName, parsed)
			t.onContentBlockDelta(parsed)
		case "content_block_stop":
			t.writeAdjusted(eventName, parsed)
			t.onContentBlockStop(parsed)
		case "message_start":
			t.writeAdjusted(eventName, parsed)
			t.onMessageStart(parsed)
		case "message_delta":
			t.writeMessageDelta(eventName, parsed)
		case "message_stop":
			t.onMessageStop(eventName, evt)
		default:
			t.writeAdjusted(eventName, parsed)
		}
	}
}

func (t *webSearchSSETransformer) isWebSearchToolUseStart(parsed gjson.Result) bool {
	if parsed.Get("type").Str != "content_block_start" {
		return false
	}
	cb := parsed.Get("content_block")
	return cb.Get("type").Str == "tool_use" && cb.Get("name").Str == "web_search"
}

func (t *webSearchSSETransformer) beginBuffering(parsed gjson.Result) {
	t.state = sseBuffering
	t.bufUpstreamIdx = parsed.Get("index").Int()
	t.bufInputJSON.Reset()
	t.bufServerToolUseID = mapToolUseIDToServer(parsed.Get("content_block.id").Str)
	if t.bufServerToolUseID == "" {
		t.bufServerToolUseID = generateToolUseID()
	}
}

func (t *webSearchSSETransformer) flushBufferedWebSearch() {
	t.webSearchCalls++
	outIdxServer := t.bufUpstreamIdx + t.idxOffset
	outIdxResult := outIdxServer + 1

	inputJSON := strings.TrimSpace(t.bufInputJSON.String())
	if inputJSON == "" {
		inputJSON = "{}"
	}

	var inputObj map[string]any
	if err := json.Unmarshal([]byte(inputJSON), &inputObj); err != nil || inputObj == nil {
		inputObj = map[string]any{}
	}

	startBlock := map[string]any{
		"type":  "server_tool_use",
		"id":    t.bufServerToolUseID,
		"name":  "web_search",
		"input": inputObj,
	}
	t.writeEvent("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         outIdxServer,
		"content_block": startBlock,
	})
	t.writeEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": outIdxServer,
	})

	serverToolUseBlock, _ := json.Marshal(startBlock)
	t.recordBlock(outIdxServer, serverToolUseBlock)

	var resultBlock map[string]any
	if hits, err := t.h.callWebSearch(t.ctx, json.RawMessage(inputJSON), t.wsCtx); err != nil {
		resultBlock = rawMessageToMap(buildWebSearchToolResultError(t.bufServerToolUseID, webSearchErrorCode(err)))
	} else {
		resultBlock = rawMessageToMap(buildWebSearchToolResult(t.bufServerToolUseID, hits))
	}

	t.writeEvent("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         outIdxResult,
		"content_block": resultBlock,
	})
	t.writeEvent("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": outIdxResult,
	})

	resultBlockBytes, _ := json.Marshal(resultBlock)
	t.recordBlock(outIdxResult, resultBlockBytes)

	t.idxOffset++
	t.state = ssePassthrough
}

// --- Content block accumulation for Snapshot ---

func (t *webSearchSSETransformer) onMessageStart(parsed gjson.Result) {
	usage := parsed.Get("message.usage")
	if usage.Exists() {
		t.accumulateUsage(usage)
	}
}

func (t *webSearchSSETransformer) onContentBlockStart(parsed gjson.Result) {
	outIdx := parsed.Get("index").Int() + t.idxOffset
	cb := parsed.Get("content_block")
	bt := cb.Get("type").Str
	t.blockBuilders[outIdx] = &streamingBlock{
		blockType: bt,
		raw:       json.RawMessage(cb.Raw),
	}
}

func (t *webSearchSSETransformer) onContentBlockDelta(parsed gjson.Result) {
	outIdx := parsed.Get("index").Int() + t.idxOffset
	b, ok := t.blockBuilders[outIdx]
	if !ok {
		return
	}
	delta := parsed.Get("delta")
	switch delta.Get("type").Str {
	case "text_delta":
		b.textBuf.WriteString(delta.Get("text").Str)
	case "input_json_delta":
		b.inputBuf.WriteString(delta.Get("partial_json").Str)
	}
}

func (t *webSearchSSETransformer) onContentBlockStop(parsed gjson.Result) {
	outIdx := parsed.Get("index").Int() + t.idxOffset
	b, ok := t.blockBuilders[outIdx]
	if !ok {
		return
	}
	delete(t.blockBuilders, outIdx)

	var block json.RawMessage
	switch b.blockType {
	case "text":
		var m map[string]json.RawMessage
		_ = json.Unmarshal(b.raw, &m)
		if m == nil {
			m = map[string]json.RawMessage{"type": json.RawMessage(`"text"`)}
		}
		text := b.textBuf.String()
		encoded, _ := json.Marshal(text)
		m["text"] = encoded
		block, _ = json.Marshal(m)
	case "tool_use":
		var m map[string]json.RawMessage
		_ = json.Unmarshal(b.raw, &m)
		if m == nil {
			m = map[string]json.RawMessage{"type": json.RawMessage(`"tool_use"`)}
		}
		inputStr := b.inputBuf.String()
		if inputStr == "" {
			m["input"] = json.RawMessage(`{}`)
		} else {
			if json.Valid([]byte(inputStr)) {
				m["input"] = json.RawMessage(inputStr)
			} else {
				m["input"] = json.RawMessage(`{}`)
			}
		}
		block, _ = json.Marshal(m)
	default:
		block = b.raw
	}
	t.recordBlock(outIdx, block)
}

func (t *webSearchSSETransformer) recordBlock(outIdx int64, block json.RawMessage) {
	for int64(len(t.outerContentBlocks)) <= outIdx {
		t.outerContentBlocks = append(t.outerContentBlocks, nil)
	}
	t.outerContentBlocks[outIdx] = block
}

func (t *webSearchSSETransformer) accumulateUsage(usage gjson.Result) {
	usage.ForEach(func(key, val gjson.Result) bool {
		k := key.Str
		if val.Type == gjson.JSON {
			sub, ok := t.outerUsage[k].(map[string]any)
			if !ok {
				sub = make(map[string]any)
				t.outerUsage[k] = sub
			}
			val.ForEach(func(sk, sv gjson.Result) bool {
				if sv.Type == gjson.Number {
					existing, _ := sub[sk.Str]
					sub[sk.Str] = toFloat64(existing) + sv.Float()
				}
				return true
			})
		} else if val.Type == gjson.Number {
			existing, _ := t.outerUsage[k]
			t.outerUsage[k] = toFloat64(existing) + val.Float()
		}
		return true
	})
}

// --- message_delta / message_stop handling ---

func (t *webSearchSSETransformer) writeMessageDelta(eventName string, parsed gjson.Result) {
	deltaUsage := parsed.Get("delta.usage")
	if deltaUsage.Exists() {
		t.accumulateUsage(deltaUsage)
	}

	payload := parsed.Raw
	stopReason := parsed.Get("delta.stop_reason").Str

	if stopReason == "tool_use" && t.webSearchCalls > 0 && t.otherToolCalls == 0 {
		if t.holdPauseTurn {
			rewritten, err := setJSONField(payload, "delta.stop_reason", "pause_turn")
			if err == nil {
				payload = rewritten
			}
			t.pendingMessageDelta = []byte("event: " + eventName + "\ndata: " + payload + "\n\n")
			return
		}
		updated, err := setJSONField(payload, "delta.stop_reason", "pause_turn")
		if err == nil {
			payload = updated
		}
	}
	t.writeRawEvent(eventName, []byte(payload))
}

func (t *webSearchSSETransformer) onMessageStop(eventName string, evt []byte) {
	if t.pendingMessageDelta != nil {
		t.pendingMessageStop = append(evt, '\n', '\n')
		return
	}
	t.writeRaw(evt)
}

// --- Public methods for loop driver ---

// HasPendingPauseTurn reports whether this transformer ended with a pending
// pause_turn that was withheld from the pipe (holdPauseTurn == true path).
func (t *webSearchSSETransformer) HasPendingPauseTurn() bool {
	return t.pendingMessageDelta != nil
}

// Snapshot returns the accumulated content blocks and usage from this
// transformer's output. Used by the loop driver to construct the next
// sub-request body.
func (t *webSearchSSETransformer) Snapshot() ([]json.RawMessage, map[string]any) {
	blocks := make([]json.RawMessage, 0, len(t.outerContentBlocks))
	for _, b := range t.outerContentBlocks {
		if b != nil {
			blocks = append(blocks, b)
		}
	}
	return blocks, t.outerUsage
}

// PendingFrames returns the withheld message_delta and message_stop SSE frame
// bytes. Used by the loop driver for fallback when it can't continue looping.
func (t *webSearchSSETransformer) PendingFrames() (delta, stop []byte) {
	return t.pendingMessageDelta, t.pendingMessageStop
}

// --- Low-level SSE helpers ---

func (t *webSearchSSETransformer) writeAdjusted(eventName string, parsed gjson.Result) {
	idxResult := parsed.Get("index")
	payload := parsed.Raw
	if idxResult.Exists() {
		newIdx := idxResult.Int() + t.idxOffset
		updated, err := setJSONField(payload, "index", newIdx)
		if err == nil {
			payload = updated
		}
	}
	t.writeRawEvent(eventName, []byte(payload))
}

func (t *webSearchSSETransformer) writeEvent(eventName string, payload map[string]any) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	t.writeRawEvent(eventName, body)
}

func (t *webSearchSSETransformer) writeRawEvent(eventName string, jsonPayload []byte) {
	if eventName == "" {
		eventName = gjson.GetBytes(jsonPayload, "type").Str
	}
	var b bytes.Buffer
	b.WriteString("event: ")
	b.WriteString(eventName)
	b.WriteString("\ndata: ")
	b.Write(jsonPayload)
	b.WriteString("\n\n")
	_, _ = t.pw.Write(b.Bytes())
}

func (t *webSearchSSETransformer) writeRaw(evt []byte) {
	_, _ = t.pw.Write(evt)
	_, _ = t.pw.Write([]byte("\n\n"))
}

// parseSSEFrame extracts the `event:` name and the concatenated `data:` body
// from a fully buffered SSE frame.
func parseSSEFrame(evt []byte) (string, string) {
	lines := strings.Split(string(evt), "\n")
	var eventName string
	dataLines := make([]string, 0, len(lines))
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "event: "):
			eventName = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "event:"):
			eventName = strings.TrimPrefix(line, "event:")
		case strings.HasPrefix(line, "data: "):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
		}
	}
	return eventName, strings.Join(dataLines, "\n")
}

func setJSONField(raw string, path string, value any) (string, error) {
	out, err := sjson.SetBytes([]byte(raw), path, value)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func rawMessageToMap(raw json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

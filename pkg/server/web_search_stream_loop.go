package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// webSearchSSELoopDriver wraps the first-round webSearchSSETransformer and
// drives subsequent self-call rounds when the transformer reports a pending
// pause_turn. It implements io.ReadCloser so it can replace clientReader in
// the write loop.
type webSearchSSELoopDriver struct {
	transformer      *webSearchSSETransformer
	wsCtx            *webSearchContext
	h                *gatewayHandler
	ctx              context.Context
	forwardedHeaders http.Header

	outPR *io.PipeReader
	outPW *io.PipeWriter

	accumulatedBlocks []json.RawMessage
	accumulatedUsage  map[string]any
	indexOffset       int64
	round             int

	subStreamResult bool
	subStreamErr    error
}

func newWebSearchSSELoopDriver(ctx context.Context, transformer *webSearchSSETransformer, wsCtx *webSearchContext, h *gatewayHandler, fwdHeaders http.Header) *webSearchSSELoopDriver {
	outPR, outPW := io.Pipe()
	d := &webSearchSSELoopDriver{
		transformer:      transformer,
		wsCtx:            wsCtx,
		h:                h,
		ctx:              ctx,
		forwardedHeaders: fwdHeaders,
		outPR:            outPR,
		outPW:            outPW,
	}
	go d.run()
	return d
}

func (d *webSearchSSELoopDriver) Read(p []byte) (int, error) { return d.outPR.Read(p) }
func (d *webSearchSSELoopDriver) Close() error               { return d.outPR.Close() }

func (d *webSearchSSELoopDriver) run() {
	defer func() { _ = d.outPW.Close() }()

	// Phase A: relay first-round transformer output.
	_, _ = io.Copy(d.outPW, d.transformer)

	// Phase B: check if we should loop.
	if !d.transformer.HasPendingPauseTurn() {
		return
	}

	d.accumulatedBlocks, d.accumulatedUsage = d.transformer.Snapshot()
	d.indexOffset = int64(len(d.accumulatedBlocks))
	d.round = 1

	// Phase C: self-call loop.
	for d.round < webSearchMaxRounds {
		subBody, err := buildWebSearchSubBody(d.wsCtx.originalRequestBody, d.accumulatedBlocks)
		if err != nil {
			d.fallbackPauseTurn()
			return
		}

		shouldLoop, ok := d.runSubRound(subBody)
		if !ok {
			return
		}
		if !shouldLoop {
			return
		}
	}
	d.fallbackPauseTurn()
}

func (d *webSearchSSELoopDriver) runSubRound(subBody []byte) (shouldLoop bool, ok bool) {
	subReq := httptest.NewRequestWithContext(d.ctx, "POST", "/api/unified/v1/messages", bytes.NewReader(subBody))
	d.applyHeaders(subReq)
	subReq.Header.Set("Content-Type", "application/json")
	subReq.Header.Set("Accept", "text/event-stream")

	rec := newStreamingResponseRecorder()
	defer rec.Reader().Close()
	go func() { defer rec.Close(); d.h.Server.router.ServeHTTP(rec, subReq) }()

	<-rec.StatusReady()
	if rec.StatusCode() != http.StatusOK {
		d.fallbackPauseTurn()
		return false, false
	}

	subTransformer := newWebSearchSSETransformer(d.ctx, rec.Reader(), d.wsCtx, d.h, false)
	shouldLoop, err := d.forwardSubStream(subTransformer)
	_ = subTransformer.Close()
	if err != nil {
		d.fallbackPauseTurn()
		return false, false
	}

	subBlocks, subUsage := subTransformer.Snapshot()
	d.accumulatedBlocks = append(d.accumulatedBlocks, subBlocks...)
	mergeUsageInto(d.accumulatedUsage, subUsage)

	d.round++
	d.indexOffset = int64(len(d.accumulatedBlocks))
	return shouldLoop, true
}

func (d *webSearchSSELoopDriver) applyHeaders(req *http.Request) {
	for k, vs := range d.forwardedHeaders {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if d.wsCtx.apiKeyToken != "" {
		req.Header.Set("Authorization", "Bearer "+d.wsCtx.apiKeyToken)
	}
	if d.wsCtx.parentSpanID != "" {
		req.Header.Set("X-Claude-Code-Session-Id", d.wsCtx.parentSpanID)
	}
}

// forwardSubStream reads SSE frames from the sub-transformer's pipe, adjusts
// content block indices by d.indexOffset, and writes them to d.outPW. Returns
// shouldLoop=true when the sub-stream ended with pause_turn.
func (d *webSearchSSELoopDriver) forwardSubStream(sub *webSearchSSETransformer) (bool, error) {
	d.subStreamResult = false
	d.subStreamErr = nil
	var buf bytes.Buffer
	tmp := make([]byte, 8*1024)
	for {
		n, err := sub.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		d.processFrames(&buf)
		if err != nil {
			break
		}
	}
	d.processFrames(&buf)

	// After the sub-transformer pipe is drained, check its pending state.
	// holdPauseTurn=false means if stop_reason was pause_turn, it was written
	// normally to the pipe. We detect it by looking at the last message_delta
	// we forwarded.
	return d.subStreamResult, d.subStreamErr
}

func (d *webSearchSSELoopDriver) processFrames(buf *bytes.Buffer) {
	for {
		data := buf.Bytes()
		idx := bytesIndex(data, "\n\n")
		if idx == -1 {
			return
		}
		frame := make([]byte, idx)
		copy(frame, data[:idx])
		buf.Next(idx + 2)
		d.processFrame(frame)
	}
}

func (d *webSearchSSELoopDriver) processFrame(frame []byte) {
	eventName, dataPayload := parseSSEFrame(frame)
	if dataPayload == "" {
		_, _ = d.outPW.Write(frame)
		_, _ = d.outPW.Write([]byte("\n\n"))
		return
	}
	parsed := gjson.Parse(dataPayload)
	eventType := parsed.Get("type").Str
	if eventName == "" {
		eventName = eventType
	}

	switch eventType {
	case "message_start":
		// Drop sub-stream's message_start; client already has the outer one.
		return

	case "content_block_start", "content_block_delta", "content_block_stop":
		payload := dataPayload
		idxField := parsed.Get("index")
		if idxField.Exists() {
			newIdx := idxField.Int() + d.indexOffset
			updated, err := setJSONField(payload, "index", newIdx)
			if err == nil {
				payload = updated
			}
		}
		d.writeSSE(eventName, []byte(payload))

	case "message_delta":
		stopReason := parsed.Get("delta.stop_reason").Str
		deltaUsage := parsed.Get("delta.usage")
		if deltaUsage.Exists() {
			var usageMap map[string]any
			_ = json.Unmarshal([]byte(deltaUsage.Raw), &usageMap)
			mergeUsageInto(d.accumulatedUsage, usageMap)
		}

		if stopReason == "pause_turn" {
			d.subStreamResult = true
			return
		}

		// Terminal stop_reason — rewrite usage to accumulated total.
		payload := dataPayload
		if len(d.accumulatedUsage) > 0 {
			usageBytes, err := json.Marshal(d.accumulatedUsage)
			if err == nil {
				updated, serr := sjson.SetRawBytes([]byte(payload), "delta.usage", usageBytes)
				if serr == nil {
					payload = string(updated)
				}
			}
		}
		d.writeSSE(eventName, []byte(payload))
		d.subStreamResult = false

	case "message_stop":
		if d.subStreamResult {
			return
		}
		d.writeSSE(eventName, []byte(dataPayload))

	default:
		// ping, etc — pass through
		_, _ = d.outPW.Write(frame)
		_, _ = d.outPW.Write([]byte("\n\n"))
	}
}

func (d *webSearchSSELoopDriver) writeSSE(eventName string, payload []byte) {
	var b bytes.Buffer
	b.WriteString("event: ")
	b.WriteString(eventName)
	b.WriteString("\ndata: ")
	b.Write(payload)
	b.WriteString("\n\n")
	_, _ = d.outPW.Write(b.Bytes())
}

// fallbackPauseTurn writes the withheld pause_turn frames from the first-round
// transformer (or synthesizes them) so the client gets a valid termination.
func (d *webSearchSSELoopDriver) fallbackPauseTurn() {
	delta, stop := d.transformer.PendingFrames()
	if delta != nil {
		// Rewrite usage in the pending delta to accumulated totals.
		if len(d.accumulatedUsage) > 0 {
			_, dataPayload := parseSSEFrame(delta[:len(delta)-2]) // strip trailing \n\n
			if dataPayload != "" {
				usageBytes, err := json.Marshal(d.accumulatedUsage)
				if err == nil {
					updated, serr := sjson.SetRawBytes([]byte(dataPayload), "delta.usage", usageBytes)
					if serr == nil {
						var b bytes.Buffer
						b.WriteString("event: message_delta\ndata: ")
						b.Write(updated)
						b.WriteString("\n\n")
						delta = b.Bytes()
					}
				}
			}
		}
		_, _ = d.outPW.Write(delta)
	}
	if stop != nil {
		_, _ = d.outPW.Write(stop)
	}
}

# TTFT & Token Recording Design

## Goal

Record time-to-first-token (TTFT) and token usage breakdown (input, output, cache read, cache write) into the `request` table by parsing upstream provider responses as they stream through the gateway. Support OpenAI and Anthropic formats, both SSE streaming and non-streaming JSON.

## Current State

- The `request` table already has `ttft_ms`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens` columns — they are never populated.
- The gateway does blind chunk-copy of the upstream response body to the client (32KB buffer, flusher for SSE).
- No response body parsing exists today.

## Architecture

### ResponseExtractor (new file: `pkg/server/response_extractor.go`)

An `io.Reader` wrapper that inspects bytes as they flow from upstream to client. Transparent to downstream readers.

```go
type ResponseMetrics struct {
    TTFTMs           *int64
    InputTokens      *int64
    OutputTokens     *int64
    CacheReadTokens  *int64
    CacheWriteTokens *int64
}

type ResponseExtractor struct {
    inner       io.Reader
    contentType string // "sse" or "json"
    startTime   time.Time
    metrics     ResponseMetrics
    ttftRecorded bool
    // SSE: line buffer for reassembling events across Read() boundaries
    lineBuf []byte
    // JSON: accumulate full body
    jsonBuf []byte
}
```

**Constructor**: `NewResponseExtractor(inner io.Reader, contentType string, startTime time.Time) *ResponseExtractor`

**Read() behavior**:
1. Call `inner.Read()`, get bytes.
2. Based on `contentType`:
   - **SSE**: Feed bytes into line buffer. When `\n\n` detected (event boundary), parse accumulated `data:` lines with gjson. Extract TTFT timestamp on first content token. Extract usage from final events. Forward all bytes to caller unchanged.
   - **JSON**: Append bytes to `jsonBuf`. Forward all bytes to caller unchanged. On EOF, parse `jsonBuf` with gjson to extract usage.
3. Return the bytes to the caller — data flows to client in real-time, no added latency.

### JSON Extraction via gjson

Use `github.com/tidwall/gjson` for all JSON field extraction. No full struct unmarshaling — just targeted path lookups.

For SSE, each event may contain multiple `data:` lines. Per SSE spec, concatenate all `data:` lines within one event, then parse the combined payload with gjson.

**OpenAI SSE** (each event's combined data payload):
- TTFT: `gjson.Get(payload, "choices.0.delta.content").String() != ""` or `gjson.Get(payload, "choices.0.delta.tool_calls").Exists()` — first non-empty hit records `time.Since(startTime)`.
- Usage: `gjson.Get(payload, "usage.prompt_tokens")`, `gjson.Get(payload, "usage.completion_tokens")`, `gjson.Get(payload, "usage.prompt_tokens_details.cached_tokens")`.
  - Note: OpenAI only returns usage in SSE if the client sent `stream_options: {include_usage: true}`. Since PicoTera proxies the client's request body unchanged, usage will be present when the client opts in; otherwise token counts will be nil.

**Anthropic SSE** (each event's combined data payload):
- TTFT: `gjson.Get(payload, "type").String() == "content_block_delta"` and `gjson.Get(payload, "delta.type").String() == "text_delta"` — first hit records TTFT.
- Also: `type == "content_block_start"` with `content_block.type == "tool_use"` counts as first token.
- Usage (from `message_delta` event): `gjson.Get(payload, "usage.output_tokens")`.
- Usage (from `message_start` event): `gjson.Get(payload, "message.usage.input_tokens")`, `gjson.Get(payload, "message.usage.cache_read_input_tokens")`, `gjson.Get(payload, "message.usage.cache_creation_input_tokens")`.
  - Anthropic always includes usage in `message_start` and `message_delta`, so token counts will be available.

**OpenAI JSON** (after full body buffered):
- `usage.prompt_tokens`, `usage.completion_tokens`, `usage.prompt_tokens_details.cached_tokens`.
- TTFT: not applicable for non-streaming (record `NULL`).

**Anthropic JSON** (after full body buffered):
- `usage.input_tokens`, `usage.output_tokens`, `usage.cache_read_input_tokens`, `usage.cache_creation_input_tokens`.
- TTFT: not applicable for non-streaming (record `NULL`).

### Content-Type Detection

Check the upstream response `Content-Type` header when creating the extractor:
- `text/event-stream` → SSE mode
- Anything else → JSON mode (will attempt gjson extraction, no-op if structure is unrecognized)

### SSE Line Buffering

SSE events can span multiple `Read()` calls and multiple `data:` lines can appear in one event. The extractor maintains a line buffer:

1. Accumulate bytes in `lineBuf`.
2. When `\n\n` found, split into lines, extract `data:` payloads.
3. Concatenate multi-line `data:` payloads (per SSE spec), then parse with gjson.
4. Clear buffer for next event.

## Gateway Integration

**File**: `pkg/server/handle_gateway.go`

**Current chain**: `resp.Body → idleTimeoutReader → buf → client`

**New chain**: `resp.Body → ResponseExtractor → idleTimeoutReader → buf → client`

Insert the extractor right after receiving the 200 OK response, before the stream copy loop:

```go
extractor := NewResponseExtractor(reader, resp.Header.Get("Content-Type"), upstreamStartTime)
reader = newIdleTimeoutReader(extractor, h.config.GatewayReadTimeout, cancel)
// ... existing stream copy loop unchanged ...
```

After the stream copy loop ends, read metrics:

```go
metrics := extractor.Metrics()
```

**Timing**: `upstreamStartTime` is captured right before `http.DefaultClient.Do(req)` or equivalent.

## Database Changes

**No migration needed** — columns already exist.

**Query changes** (`db/queries/request.sql`):

1. Update `UpdateRequestOnComplete` to include metric fields:

```sql
UPDATE request SET
  status_code = $2, error_message = $3, time_spent_ms = $4, status = $5,
  ttft_ms = $6, input_tokens = $7, output_tokens = $8,
  cache_read_tokens = $9, cache_write_tokens = $10
WHERE id = $1
```

2. Add `UpdateRequestMetrics` for copying metrics to the meta request:

```sql
UPDATE request SET
  ttft_ms = $2, input_tokens = $3, output_tokens = $4,
  cache_read_tokens = $5, cache_write_tokens = $6
WHERE id = $1
```

**Flow change**: Complete upstream request with metrics first, then copy metrics to meta request:

```
1. UpdateRequestOnComplete(upstreamID, ..., metrics) — includes token/TTFT fields
2. UpdateRequestMetrics(metaID, metrics) — copy from upstream
```

## Error Handling

- **Unrecognized format**: Extractor passes all bytes through, metrics stay nil. DB gets NULLs (same as current behavior).
- **Ambiguous content-type**: SSE (`text/event-stream`) takes priority. Fallback to JSON mode for everything else.
- **Partial SSE stream**: TTFT preserved if already captured. Token counts stay nil if stream breaks before final usage event. Partial data is better than no data.
- **Non-200 responses**: Bypass the extractor entirely — no parsing needed for error responses.
- **[DONE] sentinel**: OpenAI sends `data: [DONE]` to end streams. The extractor handles this gracefully (not valid JSON, skip it).

## Dependencies

- Add `github.com/tidwall/gjson` to `go.mod`.

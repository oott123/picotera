# Response Artifact Views: Aggregated JSON & Markdown Rendering

**Date**: 2026-04-25
**Status**: Draft

## Problem

When viewing LLM response artifacts in the request details panel, SSE streaming responses display as raw `data: {...}` text — unreadable and unstructured. Users need to see the aggregated (non-streaming equivalent) JSON and render the thinking/reply content as Markdown.

## Scope

Frontend-only. No backend changes required.

## Design Decisions

- **Approach**: Pure frontend SSE parsing. The artifact payload already stores the raw response body and headers. Parse SSE on the client to aggregate deltas and extract content.
- **SSE detection**: Check the artifact's `Content-Type` header for `text/event-stream` rather than heuristic body parsing.
- **Format support**: OpenAI Chat Completions, Anthropic Messages, OpenAI Responses API. Auto-detect from the first SSE event's JSON structure.
- **View organization**: Sub-view switching within the existing "原始响应" tab using `SegmentedControl`, not additional top-level tabs.
- **Markdown rendering**: `marked` + `@tailwindcss/typography` prose classes + `DOMPurify` for sanitization.

## SSE Parser (`src/composables/useSSEParser.ts`)

### Types

```typescript
type SSEFormat = 'openai-chat' | 'anthropic' | 'openai-responses' | 'unknown'

interface AggregatedResult {
  format: SSEFormat
  json: Record<string, unknown> | null  // null if parsing fails
}

interface ContentResult {
  thinking: string | null  // Anthropic thinking block, OpenAI Responses reasoning
  reply: string | null     // Main text content
}
```

### `aggregateSSE(body: string, format?: SSEFormat): AggregatedResult`

Parse SSE text stream, concatenate deltas, build non-streaming equivalent JSON.

1. Split body on `\n\n` to get events, extract `data: ` lines from each event
2. Skip `[DONE]` sentinel
3. Auto-detect format from the `type` field in the first parsed event's JSON:
   - `type` starts with `response.` (e.g. `response.created`, `response.output_text.delta`) → `openai-responses`
   - `type` is an Anthropic event type (`message_start`, `content_block_delta`, `message_delta`, etc.) → `anthropic`
   - Has `choices` field and no `type` → `openai-chat`
4. Aggregate per format:
   - **OpenAI Chat**: Concatenate `choices[0].delta.content`, build `{id, object:"chat.completion", choices:[{index:0, message:{role, content}, finish_reason}], model, usage}`
   - **Anthropic**: Concatenate `text_delta.text`, collect `thinking` blocks, build `{id, type:"message", role:"assistant", content:[...], model, stop_reason, usage}`
   - **OpenAI Responses**: If a `response.completed` event exists (type === `"response.completed"`), use its `response` field directly as the aggregated JSON. Otherwise, reconstruct from deltas: concatenate `response.output_text.delta` events' `delta` fields, collect reasoning summary deltas, build a response object.
5. Return `{format, json}`; if body is not SSE (no `data:` lines detected after checking content-type says it should be), return `{format:'unknown', json: JSON.parse(body)}`

### `extractContent(body: string, format: SSEFormat): ContentResult`

Extract thinking and reply text from SSE body.

- **Anthropic**: `thinking` content blocks → `thinking`; `text` content blocks → `reply`
- **OpenAI Chat**: `reasoning_content` (o1 models) → `thinking`; `choices[0].delta.content` → `reply`
- **OpenAI Responses**:
  - `reply`: concatenate `response.output_text.delta` events' `delta` fields; or extract from `response.completed` → `response.output[].content[].text`
  - `thinking`: concatenate `response.reasoning_summary_text.delta` events' `delta` fields; or extract from `response.completed` → `response.output[].summary[].text`
- For non-SSE JSON responses, parse directly and extract the same fields from the non-streaming structure.

## Component Changes

### `RawArtifactView.vue`

Keep the `kind="request"` path unchanged. For `kind="response"`, after the fetch completes, pass the loaded `payload` to `ResponseArtifactView` as a prop instead of rendering the inline body section. The fetch logic stays in `RawArtifactView`; `ResponseArtifactView` only handles display.

### `ResponseArtifactView.vue` (new)

Receives the loaded `ArtifactPayload` as a prop. Displays:

1. **Status code** (same as current)
2. **Headers table** (same as current)
3. **Sub-view switcher** — `SegmentedControl` with options:
   - Raw (always visible)
   - Aggregated (visible only when Content-Type is `text/event-stream`)
   - Rendered (always visible)
4. **Body content area** — renders based on selected sub-view

### Sub-views

**Raw**: Existing behavior — pretty-printed JSON or raw text in `<pre>`.

**Aggregated**: Calls `aggregateSSE()`, displays result as pretty-printed JSON in `<pre>`. If aggregation fails, show error message with fallback to Raw.

**Rendered**: Calls `extractContent()`, displays:
- **Thinking section**: If `thinking` is non-null, show in a collapsible `<details>` element with `bg-surface-50` background, default collapsed. Label: "思考过程" with a chevron icon.
- **Reply section**: `reply` text rendered through `marked` → `v-html` inside a `<div class="prose prose-sm max-w-none">`. If `reply` is null/empty, show empty state.

## Markdown Setup

### Dependencies

```bash
pnpm --dir dashboard add marked dompurify
pnpm --dir dashboard add -D @tailwindcss/typography @types/dompurify
```

### Tailwind v4 Integration

Add to `dashboard/src/main.css` (or the main Tailwind entry):

```css
@import "tailwindcss";
@plugin "@tailwindcss/typography";
```

### Rendering

```typescript
import { marked } from 'marked'
import DOMPurify from 'dompurify'

function renderMarkdown(text: string): string {
  const html = marked.parse(text, { async: false }) as string
  return DOMPurify.sanitize(html)
}
```

## Files Changed

| File | Action | Description |
|------|--------|-------------|
| `dashboard/src/composables/useSSEParser.ts` | Create | SSE parsing, aggregation, content extraction |
| `dashboard/src/components/ResponseArtifactView.vue` | Create | Response-specific artifact view with sub-views |
| `dashboard/src/components/RawArtifactView.vue` | Modify | Delegate response rendering to `ResponseArtifactView` |
| `dashboard/src/main.css` | Modify | Add `@plugin "@tailwindcss/typography"` |
| `dashboard/package.json` | Modify | Add `marked`, `dompurify`, `@tailwindcss/typography`, `@types/dompurify` |

## Edge Cases

- **Non-JSON SSE data**: Some `data:` lines may not be valid JSON. Skip gracefully, log warning.
- **Mixed format body**: If format auto-detection fails, fall back to Raw view with a note.
- **Empty content**: Show empty state ("无内容") in Rendered view when reply is empty.
- **Binary body**: Aggregated and Rendered views hidden (same as current base64 handling).
- **Truncated SSE**: If the body ends mid-event, process whatever complete events are available.

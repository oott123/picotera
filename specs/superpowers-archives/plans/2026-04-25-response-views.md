# Response Artifact Views Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add SSE delta aggregation and Markdown rendering views to the response artifact viewer.

**Architecture:** Pure frontend — parse SSE artifacts client-side, aggregate deltas into non-streaming JSON, extract thinking/reply text for Markdown rendering. New composable `useSSEParser` handles parsing; new `ResponseArtifactView` component replaces the response body section in `RawArtifactView` with sub-view switching (Raw / Aggregated / Rendered).

**Tech Stack:** Vue 3, TypeScript, `marked`, `DOMPurify`, `@tailwindcss/typography`, existing `SegmentedControl` UI primitive.

---

### Task 1: Install dependencies

**Goal:** Add `marked`, `dompurify`, `@tailwindcss/typography`, and `@types/dompurify` to the dashboard package.

**Files:**
- Modify: `dashboard/package.json`
- Modify: `dashboard/src/index.css`

**Acceptance Criteria:**
- [ ] `pnpm --dir dashboard install` succeeds
- [ ] `@tailwindcss/typography` plugin registered in CSS
- [ ] `prose` utility class is available (Vite dev server starts without errors)

**Verify:** `pnpm --dir dashboard build` → exit 0

**Steps:**

- [ ] **Step 1: Install packages**

```bash
cd /home/oott123/Work/Projects/picotera && pnpm --dir dashboard add marked dompurify && pnpm --dir dashboard add -D @tailwindcss/typography @types/dompurify
```

- [ ] **Step 2: Register typography plugin in CSS**

Add after `@import "tailwindcss";` in `dashboard/src/index.css` (line 1):

```css
@import "tailwindcss";
@plugin "@tailwindcss/typography";
```

- [ ] **Step 3: Verify build**

```bash
pnpm --dir dashboard build
```

Expected: exit 0, no errors.

- [ ] **Step 4: Commit**

```bash
git add dashboard/package.json dashboard/pnpm-lock.yaml dashboard/src/index.css
git commit -m "feat(dashboard): add marked, dompurify, @tailwindcss/typography"
```

---

### Task 2: Create SSE parser composable

**Goal:** Implement `useSSEParser.ts` with `aggregateSSE()` and `extractContent()` functions that parse SSE text into aggregated JSON and extract thinking/reply content.

**Files:**
- Create: `dashboard/src/composables/useSSEParser.ts`

**Acceptance Criteria:**
- [ ] `parseSSEEvents()` splits SSE body into typed event objects
- [ ] `detectFormat()` correctly identifies `openai-chat`, `anthropic`, `openai-responses`, or `unknown`
- [ ] `aggregateSSE()` builds non-streaming-equivalent JSON for all three formats
- [ ] `extractContent()` returns `{thinking, reply}` for all three formats + non-SSE JSON
- [ ] OpenAI Responses aggregation prefers `response.completed` event when present
- [ ] Graceful handling of malformed events (skip, don't throw)

**Verify:** `pnpm --dir dashboard type-check` → exit 0

**Steps:**

- [ ] **Step 1: Create the composable file**

Create `dashboard/src/composables/useSSEParser.ts` with the full implementation:

```typescript
import { marked } from 'marked'
import DOMPurify from 'dompurify'

// ---- Types ----

export type SSEFormat = 'openai-chat' | 'anthropic' | 'openai-responses' | 'unknown'

export interface AggregatedResult {
  format: SSEFormat
  json: Record<string, unknown> | null
}

export interface ContentResult {
  thinking: string | null
  reply: string | null
}

interface SSEEvent {
  event?: string
  data: string
}

// ---- SSE Line Parsing ----

function parseSSEEvents(body: string): SSEEvent[] {
  const events: SSEEvent[] = []
  const chunks = body.split(/\n\n+/)
  for (const chunk of chunks) {
    const lines = chunk.split('\n')
    let eventType: string | undefined
    const dataParts: string[] = []
    for (const line of lines) {
      if (line.startsWith('event:')) {
        eventType = line.slice(6).trim()
      } else if (line.startsWith('data:')) {
        dataParts.push(line.slice(5).trimStart())
      } else if (line.startsWith('data: ')) {
        dataParts.push(line.slice(6))
      }
    }
    if (dataParts.length === 0) continue
    const data = dataParts.join('\n')
    if (data === '[DONE]') continue
    events.push({ event: eventType, data })
  }
  return events
}

function parseEventData(event: SSEEvent): Record<string, unknown> | null {
  try {
    return JSON.parse(event.data) as Record<string, unknown>
  } catch {
    return null
  }
}

// ---- Format Detection ----

function detectFormat(events: SSEEvent[]): SSEFormat {
  for (const event of events) {
    const parsed = parseEventData(event)
    if (!parsed) continue

    const type = parsed.type as string | undefined
    if (typeof type === 'string') {
      if (type.startsWith('response.')) return 'openai-responses'
      if (
        type === 'message_start' ||
        type === 'message_delta' ||
        type === 'content_block_start' ||
        type === 'content_block_delta' ||
        type === 'content_block_stop' ||
        type === 'message_stop'
      ) {
        return 'anthropic'
      }
    }
    if (parsed.choices !== undefined) return 'openai-chat'
  }
  return 'unknown'
}

// ---- Aggregation: OpenAI Chat ----

function aggregateOpenAIChat(events: SSEEvent[]): Record<string, unknown> | null {
  let content = ''
  let role = ''
  let finishReason: string | null = null
  let id = ''
  let model = ''
  let usage: Record<string, unknown> | null = null

  for (const event of events) {
    const parsed = parseEventData(event)
    if (!parsed) continue
    if (parsed.id) id = parsed.id as string
    if (parsed.model) model = parsed.model as string
    const choices = parsed.choices as Array<Record<string, unknown>> | undefined
    if (choices?.[0]) {
      const choice = choices[0]
      const delta = choice.delta as Record<string, unknown> | undefined
      if (delta) {
        if (delta.content) content += delta.content as string
        if (delta.role) role = delta.role as string
      }
      if (choice.finish_reason) finishReason = choice.finish_reason as string
    }
    if (parsed.usage) usage = parsed.usage as Record<string, unknown>
  }

  return {
    id,
    object: 'chat.completion',
    model,
    choices: [{
      index: 0,
      message: { role: role || 'assistant', content },
      finish_reason: finishReason,
    }],
    ...(usage ? { usage } : {}),
  }
}

// ---- Aggregation: Anthropic ----

function aggregateAnthropic(events: SSEEvent[]): Record<string, unknown> | null {
  let id = ''
  let model = ''
  let role = 'assistant'
  let stopReason: string | null = null
  const contentBlocks: Array<{ type: string; text?: string; thinking?: string }> = []
  let currentBlockIndex = -1
  let usage: Record<string, unknown> | null = null

  for (const event of events) {
    const parsed = parseEventData(event)
    if (!parsed) continue
    const type = parsed.type as string

    if (type === 'message_start') {
      const msg = parsed.message as Record<string, unknown> | undefined
      if (msg) {
        id = (msg.id as string) || id
        model = (msg.model as string) || model
        role = (msg.role as string) || role
        if (msg.usage) usage = msg.usage as Record<string, unknown>
      }
    } else if (type === 'content_block_start') {
      const block = parsed.content_block as Record<string, unknown> | undefined
      if (block) {
        contentBlocks.push({ type: block.type as string })
        currentBlockIndex = contentBlocks.length - 1
      }
    } else if (type === 'content_block_delta') {
      const delta = parsed.delta as Record<string, unknown> | undefined
      if (delta && currentBlockIndex >= 0) {
        const block = contentBlocks[currentBlockIndex]
        if (delta.type === 'text_delta' && delta.text) {
          block.text = (block.text || '') + (delta.text as string)
        } else if (delta.type === 'thinking_delta' && delta.thinking) {
          block.thinking = (block.thinking || '') + (delta.thinking as string)
        }
      }
    } else if (type === 'message_delta') {
      const delta = parsed.delta as Record<string, unknown> | undefined
      if (delta?.stop_reason) stopReason = delta.stop_reason as string
      if (parsed.usage) {
        const u = parsed.usage as Record<string, unknown>
        usage = { ...usage, ...u }
      }
    }
  }

  return {
    id,
    type: 'message',
    role,
    content: contentBlocks,
    model,
    stop_reason: stopReason,
    ...(usage ? { usage } : {}),
  }
}

// ---- Aggregation: OpenAI Responses ----

function aggregateOpenAIResponses(events: SSEEvent[]): Record<string, unknown> | null {
  // Prefer response.completed event if present
  for (const event of events) {
    const parsed = parseEventData(event)
    if (!parsed) continue
    if (parsed.type === 'response.completed') {
      return parsed.response as Record<string, unknown> ?? null
    }
  }

  // Reconstruct from deltas
  const outputTexts: Map<string, string> = new Map()
  const reasoningTexts: Map<string, string> = new Map()
  let id = ''
  let model = ''
  let usage: Record<string, unknown> | null = null

  for (const event of events) {
    const parsed = parseEventData(event)
    if (!parsed) continue

    if (parsed.type === 'response.created' || parsed.type === 'response.in_progress') {
      const resp = parsed.response as Record<string, unknown> | undefined
      if (resp) {
        id = (resp.id as string) || id
        model = (resp.model as string) || model
      }
    } else if (parsed.type === 'response.output_text.delta') {
      const itemId = parsed.item_id as string
      const delta = parsed.delta as string
      outputTexts.set(itemId, (outputTexts.get(itemId) || '') + delta)
    } else if (parsed.type === 'response.reasoning_summary_text.delta') {
      const itemId = parsed.item_id as string
      const delta = parsed.delta as string
      reasoningTexts.set(itemId, (reasoningTexts.get(itemId) || '') + delta)
    }
  }

  const output: Array<Record<string, unknown>> = []
  for (const [itemId, text] of outputTexts) {
    output.push({
      id: itemId,
      type: 'message',
      role: 'assistant',
      content: [{ type: 'output_text', text }],
    })
  }

  return {
    id,
    object: 'response',
    model,
    output,
    ...(usage ? { usage } : {}),
  }
}

// ---- Content Extraction ----

function extractOpenAIChatContent(events: SSEEvent[]): ContentResult {
  let reply = ''
  let thinking = ''
  for (const event of events) {
    const parsed = parseEventData(event)
    if (!parsed) continue
    const choices = parsed.choices as Array<Record<string, unknown>> | undefined
    const delta = choices?.[0]?.delta as Record<string, unknown> | undefined
    if (!delta) continue
    if (delta.content) reply += delta.content as string
    if (delta.reasoning_content) thinking += delta.reasoning_content as string
  }
  return { thinking: thinking || null, reply: reply || null }
}

function extractAnthropicContent(events: SSEEvent[]): ContentResult {
  let thinking = ''
  let reply = ''
  let currentBlockType = ''

  for (const event of events) {
    const parsed = parseEventData(event)
    if (!parsed) continue
    const type = parsed.type as string

    if (type === 'content_block_start') {
      const block = parsed.content_block as Record<string, unknown> | undefined
      currentBlockType = (block?.type as string) || ''
    } else if (type === 'content_block_delta') {
      const delta = parsed.delta as Record<string, unknown> | undefined
      if (delta?.type === 'text_delta' && delta.text) {
        reply += delta.text as string
      } else if (delta?.type === 'thinking_delta' && delta.thinking) {
        thinking += delta.thinking as string
      }
    }
  }
  return { thinking: thinking || null, reply: reply || null }
}

function extractOpenAIResponsesContent(events: SSEEvent[]): ContentResult {
  // Try response.completed first
  for (const event of events) {
    const parsed = parseEventData(event)
    if (!parsed) continue
    if (parsed.type === 'response.completed') {
      const resp = parsed.response as Record<string, unknown>
      const output = resp?.output as Array<Record<string, unknown>> | undefined
      let reply = ''
      let thinking = ''
      if (output) {
        for (const item of output) {
          const content = item.content as Array<Record<string, unknown>> | undefined
          if (content) {
            for (const part of content) {
              if (part.type === 'output_text' && part.text) reply += part.text as string
            }
          }
          const summary = item.summary as Array<Record<string, unknown>> | undefined
          if (summary) {
            for (const part of summary) {
              if (part.text) thinking += part.text as string
            }
          }
        }
      }
      return { thinking: thinking || null, reply: reply || null }
    }
  }

  // Fallback: concatenate deltas
  let reply = ''
  let thinking = ''
  for (const event of events) {
    const parsed = parseEventData(event)
    if (!parsed) continue
    if (parsed.type === 'response.output_text.delta' && parsed.delta) {
      reply += parsed.delta as string
    }
    if (parsed.type === 'response.reasoning_summary_text.delta' && parsed.delta) {
      thinking += parsed.delta as string
    }
  }
  return { thinking: thinking || null, reply: reply || null }
}

function extractJsonContent(body: string): ContentResult {
  try {
    const parsed = JSON.parse(body) as Record<string, unknown>

    // OpenAI Chat format
    const choices = parsed.choices as Array<Record<string, unknown>> | undefined
    if (choices?.[0]) {
      const msg = choices[0].message as Record<string, unknown> | undefined
      const reply = (msg?.content as string) || null
      const thinking = (msg?.reasoning_content as string) || null
      return { thinking, reply }
    }

    // Anthropic format
    if (parsed.type === 'message' && Array.isArray(parsed.content)) {
      let thinking = ''
      let reply = ''
      for (const block of parsed.content as Array<Record<string, unknown>>) {
        if (block.type === 'thinking' && block.thinking) thinking += block.thinking as string
        if (block.type === 'text' && block.text) reply += block.text as string
      }
      return { thinking: thinking || null, reply: reply || null }
    }

    // OpenAI Responses format
    if (parsed.object === 'response' && Array.isArray(parsed.output)) {
      let reply = ''
      let thinking = ''
      for (const item of parsed.output as Array<Record<string, unknown>>) {
        const content = item.content as Array<Record<string, unknown>> | undefined
        if (content) {
          for (const part of content) {
            if (part.type === 'output_text' && part.text) reply += part.text as string
          }
        }
        const summary = item.summary as Array<Record<string, unknown>> | undefined
        if (summary) {
          for (const part of summary) {
            if (part.text) thinking += part.text as string
          }
        }
      }
      return { thinking: thinking || null, reply: reply || null }
    }

    return { thinking: null, reply: null }
  } catch {
    return { thinking: null, reply: null }
  }
}

// ---- Public API ----

export function aggregateSSE(body: string): AggregatedResult {
  const events = parseSSEEvents(body)
  if (events.length === 0) {
    // Not SSE — try plain JSON
    try {
      return { format: 'unknown', json: JSON.parse(body) }
    } catch {
      return { format: 'unknown', json: null }
    }
  }

  const format = detectFormat(events)
  let json: Record<string, unknown> | null = null

  switch (format) {
    case 'openai-chat':
      json = aggregateOpenAIChat(events)
      break
    case 'anthropic':
      json = aggregateAnthropic(events)
      break
    case 'openai-responses':
      json = aggregateOpenAIResponses(events)
      break
    default:
      try {
        json = JSON.parse(body)
      } catch {
        json = null
      }
  }

  return { format, json }
}

export function extractContent(body: string, isSSE: boolean): ContentResult {
  if (!isSSE) return extractJsonContent(body)

  const events = parseSSEEvents(body)
  if (events.length === 0) return extractJsonContent(body)

  const format = detectFormat(events)

  switch (format) {
    case 'openai-chat':
      return extractOpenAIChatContent(events)
    case 'anthropic':
      return extractAnthropicContent(events)
    case 'openai-responses':
      return extractOpenAIResponsesContent(events)
    default:
      return { thinking: null, reply: null }
  }
}

export function isSSEContentType(headers: Record<string, string[]> | undefined): boolean {
  if (!headers) return false
  const ct = headers['Content-type'] ?? headers['content-type'] ?? headers['Content-Type'] ?? []
  const value = Array.isArray(ct) ? ct.join(', ') : ''
  return value.toLowerCase().includes('text/event-stream')
}

export function renderMarkdown(text: string): string {
  const html = marked.parse(text, { async: false }) as string
  return DOMPurify.sanitize(html)
}
```

- [ ] **Step 2: Type-check**

```bash
pnpm --dir dashboard type-check
```

Expected: exit 0

- [ ] **Step 3: Commit**

```bash
git add dashboard/src/composables/useSSEParser.ts
git commit -m "feat(dashboard): add SSE parser composable"
```

---

### Task 3: Create ResponseArtifactView component

**Goal:** Build the `ResponseArtifactView.vue` component with sub-view switching (Raw / Aggregated / Rendered) for response artifacts.

**Files:**
- Create: `dashboard/src/components/ResponseArtifactView.vue`

**Acceptance Criteria:**
- [ ] Shows status code + headers table (same as current RawArtifactView response section)
- [ ] `SegmentedControl` switches between Raw, Aggregated (SSE only), and Rendered
- [ ] Raw view shows pretty-printed JSON in `<pre>`
- [ ] Aggregated view shows `aggregateSSE()` result in `<pre>`, with error fallback
- [ ] Rendered view shows thinking (collapsible `<details>`, default closed) and reply (Markdown via `prose`)
- [ ] Binary body hides Aggregated and Rendered views
- [ ] SSE detection via `isSSEContentType()` from composable

**Verify:** `pnpm --dir dashboard type-check` → exit 0

**Steps:**

- [ ] **Step 1: Create the component**

Create `dashboard/src/components/ResponseArtifactView.vue`:

```vue
<script setup lang="ts">
import { ref, computed } from 'vue'
import { DataTable, Th, Td, Tr, Field, SegmentedControl, StateText } from '@/ui'
import {
  aggregateSSE,
  extractContent,
  isSSEContentType,
  renderMarkdown,
  type SSEFormat,
} from '@/composables/useSSEParser'

interface ArtifactPayload {
  method?: string
  url?: string
  statusCode?: number
  headers?: Record<string, string[]>
  body?: string
  bodyEncoding?: 'utf8' | 'base64'
}

const props = defineProps<{ payload: ArtifactPayload; url?: string }>()

type SubView = 'raw' | 'aggregated' | 'rendered'
const subView = ref<SubView>('raw')

const isSSE = computed(() => isSSEContentType(props.payload.headers))
const isBinary = computed(() => props.payload.bodyEncoding === 'base64')

const subViewOptions = computed(() => {
  const opts: Array<{ value: string; label: string }> = [
    { value: 'raw', label: 'Raw' },
  ]
  if (isSSE.value && !isBinary.value) {
    opts.push({ value: 'aggregated', label: '聚合' })
  }
  if (!isBinary.value) {
    opts.push({ value: 'rendered', label: '渲染' })
  }
  return opts
})

const aggregated = computed(() => {
  if (!isSSE.value || !props.payload.body) return null
  return aggregateSSE(props.payload.body)
})

const content = computed(() => {
  if (!props.payload.body) return { thinking: null, reply: null }
  return extractContent(props.payload.body, isSSE.value)
})

const replyHtml = computed(() => {
  if (!content.value.reply) return ''
  return renderMarkdown(content.value.reply)
})

const thinkingHtml = computed(() => {
  if (!content.value.thinking) return ''
  return renderMarkdown(content.value.thinking)
})

function headerEntries(headers: Record<string, string[]> | undefined) {
  if (!headers) return []
  return Object.entries(headers).map(([k, v]) => ({ key: k, value: v.join(', ') }))
}

function bodyDisplay(body: string | undefined, encoding: string | undefined) {
  if (!body) return ''
  if (encoding === 'base64') return ''
  try {
    return JSON.stringify(JSON.parse(body), null, 2)
  } catch {
    return body
  }
}

function formatLabel(f: SSEFormat | null): string {
  switch (f) {
    case 'openai-chat': return 'OpenAI Chat'
    case 'anthropic': return 'Anthropic'
    case 'openai-responses': return 'OpenAI Responses'
    default: return ''
  }
}

// Ensure subView stays valid when options change
import { watch } from 'vue'
watch(subViewOptions, (opts) => {
  if (!opts.some(o => o.value === subView.value)) {
    subView.value = 'raw'
  }
})
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="grid grid-cols-2 gap-2.5">
      <Field v-if="payload.statusCode" label="Status" as="div">
        <span class="font-mono text-sm">{{ payload.statusCode }}</span>
      </Field>
    </div>

    <section class="flex flex-col gap-2">
      <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]">Headers</span>
      <div v-if="!headerEntries(payload.headers).length" class="text-xs text-ink-faint">—</div>
      <DataTable v-else>
        <thead>
          <Tr>
            <Th class="w-44">Header</Th>
            <Th>Value</Th>
          </Tr>
        </thead>
        <tbody>
          <Tr v-for="h in headerEntries(payload.headers)" :key="h.key">
            <Td class="font-mono text-2xs whitespace-nowrap">{{ h.key }}</Td>
            <Td class="font-mono text-2xs break-all">{{ h.value }}</Td>
          </Tr>
        </tbody>
      </DataTable>
    </section>

    <section class="flex flex-col gap-2">
      <div class="flex items-center justify-between gap-3">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]">Body</span>
        <SegmentedControl
          v-if="!isBinary"
          v-model="subView"
          :options="subViewOptions"
        />
      </div>

      <div v-if="isBinary" class="flex items-center gap-3 text-xs text-ink-faint">
        <span>[binary, {{ payload.body?.length ?? 0 }} bytes]</span>
        <a :href="url" download class="text-accent-ink underline hover:no-underline">下载原始数据</a>
      </div>

      <!-- Raw -->
      <pre
        v-else-if="subView === 'raw'"
        class="font-mono text-xs whitespace-pre-wrap break-all bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink overflow-auto max-h-[480px]"
      >{{ bodyDisplay(payload.body, payload.bodyEncoding) }}</pre>

      <!-- Aggregated -->
      <template v-else-if="subView === 'aggregated'">
        <div v-if="aggregated?.json" class="flex flex-col gap-1.5">
          <span v-if="formatLabel(aggregated.format)" class="text-2xs text-ink-muted">
            检测格式: {{ formatLabel(aggregated.format) }}
          </span>
          <pre class="font-mono text-xs whitespace-pre-wrap break-all bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink overflow-auto max-h-[480px]">{{ JSON.stringify(aggregated.json, null, 2) }}</pre>
        </div>
        <StateText v-else :dashed="false" compact>无法聚合 SSE 数据</StateText>
      </template>

      <!-- Rendered -->
      <template v-else-if="subView === 'rendered'">
        <div class="flex flex-col gap-3">
          <details v-if="content.thinking" class="group">
            <summary class="flex items-center gap-1.5 cursor-pointer text-xs font-medium text-ink-muted select-none hover:text-ink">
              <svg class="w-3 h-3 transition-transform group-open:rotate-90" viewBox="0 0 16 16" fill="currentColor"><path d="M6 3.5l5 4.5-5 4.5V3.5z"/></svg>
              思考过程
            </summary>
            <div class="mt-2 bg-surface-50 border border-line-soft rounded-md p-3 prose prose-sm max-w-none" v-html="thinkingHtml" />
          </details>
          <div v-if="content.reply" class="prose prose-sm max-w-none" v-html="replyHtml" />
          <StateText v-else-if="!content.thinking" :dashed="false" compact>无内容</StateText>
        </div>
      </template>
    </section>
  </div>
</template>
```

- [ ] **Step 2: Type-check**

```bash
pnpm --dir dashboard type-check
```

Expected: exit 0

- [ ] **Step 3: Commit**

```bash
git add dashboard/src/components/ResponseArtifactView.vue
git commit -m "feat(dashboard): add ResponseArtifactView with sub-views"
```

---

### Task 4: Wire ResponseArtifactView into RawArtifactView

**Goal:** Modify `RawArtifactView.vue` to delegate response rendering to `ResponseArtifactView` while keeping request rendering unchanged.

**Files:**
- Modify: `dashboard/src/components/RawArtifactView.vue`

**Acceptance Criteria:**
- [ ] Request artifacts render identically to before
- [ ] Response artifacts render via `ResponseArtifactView` with sub-views
- [ ] Loading/error/empty states preserved

**Verify:** `pnpm --dir dashboard type-check` → exit 0

**Steps:**

- [ ] **Step 1: Replace the response body section in RawArtifactView**

In `dashboard/src/components/RawArtifactView.vue`, add the import after line 3:

```typescript
import ResponseArtifactView from './ResponseArtifactView.vue'
```

Replace the entire `<div v-else-if="payload" ...>` template block (lines 65-108) with:

```vue
    <template v-else-if="payload">
      <template v-if="kind === 'request'">
        <div class="flex flex-col gap-3">
          <div class="grid grid-cols-2 gap-2.5">
            <Field v-if="payload.method" label="Method" as="div">
              <span class="font-mono text-sm">{{ payload.method }}</span>
            </Field>
            <Field v-if="payload.url" label="URL" as="div" class="col-span-2">
              <span class="font-mono text-xs break-all">{{ payload.url }}</span>
            </Field>
          </div>

          <section class="flex flex-col gap-2">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]">Headers</span>
            <div v-if="!headerEntries(payload.headers).length" class="text-xs text-ink-faint">—</div>
            <DataTable v-else>
              <thead>
                <Tr>
                  <Th class="w-44">Header</Th>
                  <Th>Value</Th>
                </Tr>
              </thead>
              <tbody>
                <Tr v-for="h in headerEntries(payload.headers)" :key="h.key">
                  <Td class="font-mono text-2xs whitespace-nowrap">{{ h.key }}</Td>
                  <Td class="font-mono text-2xs break-all">{{ h.value }}</Td>
                </Tr>
              </tbody>
            </DataTable>
          </section>

          <section class="flex flex-col gap-2">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]">Body</span>
            <div v-if="payload.bodyEncoding === 'base64'" class="flex items-center gap-3 text-xs text-ink-faint">
              <span>[binary, {{ payload.body?.length ?? 0 }} bytes]</span>
              <a :href="url" download class="text-accent-ink underline hover:no-underline">下载原始数据</a>
            </div>
            <pre
              v-else
              class="font-mono text-xs whitespace-pre-wrap break-all bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink overflow-auto max-h-[480px]"
            >{{ bodyDisplay(payload.body, payload.bodyEncoding) }}</pre>
          </section>
        </div>
      </template>
      <ResponseArtifactView v-else :payload="payload" :url="url" />
    </template>
```

- [ ] **Step 2: Type-check**

```bash
pnpm --dir dashboard type-check
```

Expected: exit 0

- [ ] **Step 3: Commit**

```bash
git add dashboard/src/components/RawArtifactView.vue
git commit -m "feat(dashboard): wire ResponseArtifactView into RawArtifactView"
```

---

### Task 5: Verify end-to-end and lint

**Goal:** Ensure the full feature works — build passes, type-check passes, lint is clean.

**Files:**
- No new files

**Acceptance Criteria:**
- [ ] `pnpm --dir dashboard type-check` passes
- [ ] `pnpm --dir dashboard lint` passes
- [ ] `pnpm --dir dashboard build` passes
- [ ] Dev server starts without errors

**Verify:** All three commands exit 0

**Steps:**

- [ ] **Step 1: Run type-check**

```bash
pnpm --dir dashboard type-check
```

- [ ] **Step 2: Run lint**

```bash
pnpm --dir dashboard lint
```

- [ ] **Step 3: Run build**

```bash
pnpm --dir dashboard build
```

- [ ] **Step 4: Fix any issues found**

If any command fails, fix the reported issues and re-run.

- [ ] **Step 5: Final commit if any fixes were needed**

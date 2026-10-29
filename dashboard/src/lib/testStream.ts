import { parseSSEEventsForDisplay } from '@/composables/useSSEParser'
import type { TestFormat } from '@/lib/testBody'

export interface AggregatedContent {
  thinking: string
  reply: string
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

// aggregateGeminiChunks folds an array of Gemini GenerateContent chunks (one per
// SSE event, or the full non-stream response wrapped in a single-element array)
// into thinking + reply text.
function aggregateGeminiChunks(chunks: unknown[]): AggregatedContent {
  let thinking = ''
  let reply = ''
  for (const chunk of chunks) {
    const root = asRecord(chunk)
    for (const candidateValue of asArray(root?.candidates)) {
      const candidate = asRecord(candidateValue)
      const content = asRecord(candidate?.content)
      for (const partValue of asArray(content?.parts)) {
        const part = asRecord(partValue)
        if (typeof part?.text !== 'string') continue
        if (part.thought === true) thinking += part.text
        else reply += part.text
      }
    }
  }
  return { thinking, reply }
}

// aggregateStream folds an SSE response body (the raw accumulated text) into
// readable thinking + reply content for the given format. It is recomputed from
// scratch on each chunk arrival — fine for test-sized payloads.
function aggregateStream(format: TestFormat, raw: string): AggregatedContent {
  const events = parseSSEEventsForDisplay(raw)
  let thinking = ''
  let reply = ''

  if (format === 'geminiGenerateContent' || format === 'geminiStreamGenerateContent') {
    return aggregateGeminiChunks(events.map((e) => e.json))
  }

  for (const event of events) {
    const root = asRecord(event.json)
    if (!root) continue

    switch (format) {
      case 'anthropicMessages': {
        const delta = asRecord(root.delta)
        if (!delta) break
        if (delta.type === 'text_delta' && typeof delta.text === 'string') reply += delta.text
        if (delta.type === 'thinking_delta' && typeof delta.thinking === 'string')
          thinking += delta.thinking
        break
      }
      case 'openaiChatCompletions': {
        const choice = asRecord(asArray(root.choices)[0])
        const delta = asRecord(choice?.delta)
        if (!delta) break
        if (typeof delta.content === 'string') reply += delta.content
        const reasoning = delta.reasoning_content ?? delta.reasoning
        if (typeof reasoning === 'string') thinking += reasoning
        break
      }
      case 'openaiResponses': {
        if (root.type === 'response.output_text.delta' && typeof root.delta === 'string')
          reply += root.delta
        if (root.type === 'response.reasoning_summary_text.delta' && typeof root.delta === 'string')
          thinking += root.delta
        break
      }
    }
  }
  return { thinking, reply }
}

// aggregateNonStream folds a single parsed JSON response (non-stream) into
// readable thinking + reply content for the given format.
function aggregateNonStream(format: TestFormat, body: unknown): AggregatedContent {
  const root = asRecord(body)
  if (!root) return { thinking: '', reply: '' }

  switch (format) {
    case 'anthropicMessages': {
      let thinking = ''
      let reply = ''
      for (const blockValue of asArray(root.content)) {
        const block = asRecord(blockValue)
        if (block?.type === 'text' && typeof block.text === 'string') reply += block.text
        if (block?.type === 'thinking' && typeof block.thinking === 'string')
          thinking += block.thinking
      }
      return { thinking, reply }
    }
    case 'openaiChatCompletions': {
      const choice = asRecord(asArray(root.choices)[0])
      const message = asRecord(choice?.message)
      const reply = typeof message?.content === 'string' ? message.content : ''
      const reasoning = message?.reasoning_content ?? message?.reasoning
      const thinking = typeof reasoning === 'string' ? reasoning : ''
      return { thinking, reply }
    }
    case 'openaiResponses': {
      let thinking = ''
      let reply = ''
      for (const itemValue of asArray(root.output)) {
        const item = asRecord(itemValue)
        if (item?.type === 'message') {
          for (const partValue of asArray(item.content)) {
            const part = asRecord(partValue)
            if (part?.type === 'output_text' && typeof part.text === 'string') reply += part.text
          }
        } else if (item?.type === 'reasoning') {
          for (const partValue of asArray(item.summary)) {
            const part = asRecord(partValue)
            if (typeof part?.text === 'string') thinking += part.text
          }
        }
      }
      return { thinking, reply }
    }
    case 'geminiGenerateContent':
    case 'geminiStreamGenerateContent':
      return aggregateGeminiChunks([body])
  }
}

// aggregateResponse extracts readable content from a (possibly partial) raw
// response body. `stream` selects SSE-delta aggregation; otherwise the body is
// parsed as a single JSON document (returning empty content while still partial).
export function aggregateResponse(
  format: TestFormat,
  raw: string,
  stream: boolean,
): AggregatedContent {
  if (stream) return aggregateStream(format, raw)
  try {
    return aggregateNonStream(format, JSON.parse(raw))
  } catch {
    return { thinking: '', reply: '' }
  }
}

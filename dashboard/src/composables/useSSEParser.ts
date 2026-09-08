import { marked } from 'marked'
import DOMPurify from 'dompurify'
import type { AggregatedFormat, AggregatedResponse } from '@/components/artifactTypes'

export interface ContentResult {
  thinking: string | null
  reply: string | null
}

interface SSEEvent {
  event?: string
  data: string
}

export interface ParsedSSEEvent {
  index: number
  event: string | null
  data: string
  json: unknown | null
  timeMs?: number
}

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
      }
    }
    if (dataParts.length === 0) continue
    const data = dataParts.join('\n')
    if (data === '[DONE]') continue
    events.push({ event: eventType, data })
  }
  return events
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function nonEmpty(value: string): string | null {
  return value === '' ? null : value
}

function extractOpenAIChat(body: unknown): ContentResult {
  const root = asRecord(body)
  const choice = asRecord(asArray(root?.choices)[0])
  const message = asRecord(choice?.message)
  const content = message?.content
  const reply = typeof content === 'string' ? content : null
  const reasoningContent = message?.reasoning_content
  const reasoning = message?.reasoning
  const thinking =
    typeof reasoningContent === 'string'
      ? reasoningContent
      : typeof reasoning === 'string'
        ? reasoning
        : null
  return { thinking, reply }
}

function extractOpenAIResponses(body: unknown): ContentResult {
  const root = asRecord(body)
  let thinking = ''
  let reply = ''
  for (const itemValue of asArray(root?.output)) {
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
  return { thinking: nonEmpty(thinking), reply: nonEmpty(reply) }
}

function extractAnthropic(body: unknown): ContentResult {
  const root = asRecord(body)
  let thinking = ''
  let reply = ''
  for (const blockValue of asArray(root?.content)) {
    const block = asRecord(blockValue)
    if (block?.type === 'text' && typeof block.text === 'string') reply += block.text
    if (block?.type === 'thinking' && typeof block.thinking === 'string') thinking += block.thinking
  }
  return { thinking: nonEmpty(thinking), reply: nonEmpty(reply) }
}

function extractGemini(body: unknown): ContentResult {
  const root = asRecord(body)
  let thinking = ''
  let reply = ''
  for (const candidateValue of asArray(root?.candidates)) {
    const candidate = asRecord(candidateValue)
    const content = asRecord(candidate?.content)
    for (const partValue of asArray(content?.parts)) {
      const part = asRecord(partValue)
      if (typeof part?.text !== 'string') continue
      if (part.thought === true) {
        thinking += part.text
      } else {
        reply += part.text
      }
    }
  }
  return { thinking: nonEmpty(thinking), reply: nonEmpty(reply) }
}

export function extractContentFromAggregated(
  aggregated: AggregatedResponse | undefined,
): ContentResult {
  if (!aggregated?.body || aggregated.error) return { thinking: null, reply: null }
  switch (aggregated.format) {
    case 'openaiChatCompletions':
      return extractOpenAIChat(aggregated.body)
    case 'openaiResponses':
      return extractOpenAIResponses(aggregated.body)
    case 'anthropicMessages':
      return extractAnthropic(aggregated.body)
    case 'geminiStreamGenerateContent':
      return extractGemini(aggregated.body)
    default:
      return { thinking: null, reply: null }
  }
}

export function formatAggregatedLabel(format: AggregatedFormat | undefined): string {
  switch (format) {
    case 'openaiChatCompletions':
      return 'OpenAI Chat Completions'
    case 'openaiResponses':
      return 'OpenAI Responses'
    case 'anthropicMessages':
      return 'Anthropic Messages'
    case 'geminiStreamGenerateContent':
      return 'Gemini StreamGenerateContent'
    default:
      return ''
  }
}

export function parseSSEEventsForDisplay(body: string, timings?: number[]): ParsedSSEEvent[] {
  const events = parseSSEEvents(body)
  if (!timings || timings.length === 0) {
    return events.map((event, index) => {
      let json: unknown | null = null
      try {
        json = JSON.parse(event.data)
      } catch {
        json = null
      }
      return { index, event: event.event ?? null, data: event.data, json }
    })
  }

  let newlineIndex = 0
  let pos = 0
  const chunks = body.split(/\n\n+/)
  const chunkTimings: (number | undefined)[] = []
  for (const chunk of chunks) {
    chunkTimings.push(timings[newlineIndex])
    for (let i = 0; i < chunk.length; i++) {
      if (chunk[i] === '\n') newlineIndex++
    }
    pos += chunk.length
    while (pos < body.length && body[pos] === '\n') {
      newlineIndex++
      pos++
    }
  }

  let chunkIdx = 0
  let eventIdx = 0
  const result: ParsedSSEEvent[] = []
  for (const chunk of chunks) {
    const lines = chunk.split('\n')
    const dataParts: string[] = []
    let eventType: string | undefined
    for (const line of lines) {
      if (line.startsWith('event:')) eventType = line.slice(6).trim()
      else if (line.startsWith('data:')) dataParts.push(line.slice(5).trimStart())
    }
    if (dataParts.length > 0) {
      const data = dataParts.join('\n')
      if (data !== '[DONE]') {
        let json: unknown | null = null
        try {
          json = JSON.parse(data)
        } catch {
          json = null
        }
        result.push({
          index: eventIdx,
          event: eventType ?? null,
          data,
          json,
          timeMs: chunkTimings[chunkIdx],
        })
        eventIdx++
      }
    }
    chunkIdx++
  }
  return result
}

/** 'absent' means the upstream sent no Content-Type at all — the only case
 *  where sniffing the body for SSE is allowed. 'other' is taken at face value. */
export type SSEContentTypeState = 'sse' | 'other' | 'absent'

export function sseContentTypeState(
  headers: Record<string, string[]> | undefined,
): SSEContentTypeState {
  if (!headers) return 'absent'
  for (const [name, values] of Object.entries(headers)) {
    if (name.toLowerCase() !== 'content-type') continue
    const value = values.join(', ')
    if (value === '') return 'absent'
    return value.toLowerCase().includes('text/event-stream') ? 'sse' : 'other'
  }
  return 'absent'
}

export function renderMarkdown(text: string): string {
  const html = marked.parse(text, { async: false }) as string
  return DOMPurify.sanitize(html)
}

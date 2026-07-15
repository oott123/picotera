import type { AggregatedFormat } from '@/components/artifactTypes'

export type ConversationRole = 'system' | 'user' | 'assistant' | 'tool'

export interface SearchResult {
  citation: string
  title: string
  url: string | null
  wordlim: string | null
  published: string | null
  crawled: string | null
  content: string
}

export type ConversationPart =
  | { kind: 'text'; text: string }
  | { kind: 'thinking'; text: string }
  | { kind: 'toolCall'; id: string | null; name: string; input: unknown }
  | {
      kind: 'toolResult'
      id: string | null
      name: string | null
      output: unknown
      isError: boolean
    }
  | { kind: 'media'; mediaType: string; label: string }
  | { kind: 'searchResults'; results: SearchResult[] }

export interface ConversationMessage {
  role: ConversationRole
  parts: ConversationPart[]
}

type ConversationFormat =
  | 'openaiChat'
  | 'openaiResponses'
  | 'anthropic'
  | 'gemini'
  | 'openaiSearch'

function asRecord(value: unknown): Record<string, unknown> | null {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function parseMaybeJson(value: unknown): unknown {
  if (typeof value !== 'string') return value
  try {
    return JSON.parse(value)
  } catch {
    return value
  }
}

function stringOrNull(value: unknown): string | null {
  return typeof value === 'string' && value !== '' ? value : null
}

function pushText(parts: ConversationPart[], text: unknown) {
  if (typeof text === 'string' && text !== '') parts.push({ kind: 'text', text })
}

function pushThinking(parts: ConversationPart[], text: unknown) {
  if (typeof text === 'string' && text !== '') parts.push({ kind: 'thinking', text })
}

function messageOrNull(
  role: ConversationRole,
  parts: ConversationPart[],
): ConversationMessage | null {
  return parts.length ? { role, parts } : null
}

function roleFromOpenAI(value: unknown): ConversationRole | null {
  switch (value) {
    case 'system':
    case 'developer':
      return 'system'
    case 'user':
      return 'user'
    case 'assistant':
      return 'assistant'
    case 'tool':
      return 'tool'
    default:
      return null
  }
}

function roleFromGemini(value: unknown): ConversationRole | null {
  switch (value) {
    case 'user':
      return 'user'
    case 'model':
      return 'assistant'
    default:
      return null
  }
}

function hasAnthropicContentBlocks(messages: unknown[]): boolean {
  for (const itemValue of messages) {
    const item = asRecord(itemValue)
    for (const blockValue of asArray(item?.content)) {
      const block = asRecord(blockValue)
      if (
        block?.type === 'tool_use' ||
        block?.type === 'tool_result' ||
        block?.type === 'thinking'
      ) {
        return true
      }
    }
  }
  return false
}

function hasOpenAIChatFeatures(messages: unknown[]): boolean {
  for (const itemValue of messages) {
    const item = asRecord(itemValue)
    if (item?.role === 'system' || item?.role === 'tool' || item?.role === 'developer') return true
    if (Array.isArray(item?.tool_calls)) return true
  }
  return false
}

export function detectFormat(
  json: unknown,
  kind: 'request' | 'response',
): ConversationFormat | null {
  const root = asRecord(json)
  if (!root) return null

  if (kind === 'request') {
    if (Array.isArray(root.contents)) return 'gemini'
    if ('input' in root) return 'openaiResponses'
    const messages = asArray(root.messages)
    if (messages.length) {
      if (hasOpenAIChatFeatures(messages)) return 'openaiChat'
      if ('system' in root || hasAnthropicContentBlocks(messages)) return 'anthropic'
    }
    return null
  }

  if (typeof root.output === 'string') return 'openaiSearch'
  if (Array.isArray(root.candidates)) return 'gemini'
  if (root.object === 'response' || Array.isArray(root.output)) return 'openaiResponses'
  if (Array.isArray(root.choices)) return 'openaiChat'
  if (
    Array.isArray(root.content) &&
    (root.type === 'message' || root.role === 'assistant' || root.id !== undefined)
  ) {
    return 'anthropic'
  }
  return null
}

function parseOpenAIContentParts(content: unknown): ConversationPart[] {
  const parts: ConversationPart[] = []
  if (typeof content === 'string') {
    pushText(parts, content)
    return parts
  }

  for (const partValue of asArray(content)) {
    const part = asRecord(partValue)
    if (!part) continue
    if (
      (part.type === 'text' || part.type === 'input_text' || part.type === 'output_text') &&
      typeof part.text === 'string'
    ) {
      pushText(parts, part.text)
    } else if (part.type === 'image_url') {
      parts.push({ kind: 'media', mediaType: 'image', label: '[image]' })
    } else if (part.type === 'input_image') {
      parts.push({ kind: 'media', mediaType: 'image', label: '[image]' })
    } else if (part.type === 'input_audio') {
      parts.push({ kind: 'media', mediaType: 'audio', label: '[audio]' })
    } else if (part.type === 'input_file') {
      parts.push({ kind: 'media', mediaType: 'file', label: '[file]' })
    } else if (typeof part.type === 'string') {
      parts.push({ kind: 'media', mediaType: part.type, label: `[${part.type}]` })
    }
  }
  return parts
}

function parseOpenAIChatMessage(messageValue: unknown): ConversationMessage | null {
  const message = asRecord(messageValue)
  const role = roleFromOpenAI(message?.role)
  if (!message || !role) return null

  const parts: ConversationPart[] = []
  pushThinking(parts, message.reasoning_content)
  pushThinking(parts, message.reasoning)

  if (role === 'tool') {
    const output =
      typeof message.content === 'string'
        ? parseMaybeJson(message.content)
        : (message.content ?? null)
    parts.push({
      kind: 'toolResult',
      id: stringOrNull(message.tool_call_id),
      name: null,
      output,
      isError: false,
    })
    return messageOrNull(role, parts)
  }

  parts.push(...parseOpenAIContentParts(message.content))
  for (const callValue of asArray(message.tool_calls)) {
    const call = asRecord(callValue)
    const fn = asRecord(call?.function)
    const name = stringOrNull(fn?.name)
    if (!name) continue
    parts.push({
      kind: 'toolCall',
      id: stringOrNull(call?.id),
      name,
      input: parseMaybeJson(fn?.arguments ?? null),
    })
  }
  return messageOrNull(role, parts)
}

export function parseOpenAIChatRequest(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  const messages: ConversationMessage[] = []
  for (const item of asArray(root?.messages)) {
    const message = parseOpenAIChatMessage(item)
    if (message) messages.push(message)
  }
  return messages
}

export function parseOpenAIChatResponse(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  const choice = asRecord(asArray(root?.choices)[0])
  const message = parseOpenAIChatMessage(choice?.message)
  return message ? [message] : []
}

function responseRole(value: unknown): ConversationRole {
  return value === 'system' || value === 'assistant' || value === 'tool' ? value : 'user'
}

function parseOpenAIResponseMessage(item: Record<string, unknown>): ConversationMessage | null {
  const role = responseRole(item.role)
  const parts = parseOpenAIContentParts(item.content)
  return messageOrNull(role, parts)
}

function parseOpenAIResponseItem(itemValue: unknown): ConversationMessage | null {
  const item = asRecord(itemValue)
  if (!item) return null

  if (item.type === 'message') return parseOpenAIResponseMessage(item)
  if (item.type === 'function_call') {
    const name = stringOrNull(item.name)
    if (!name) return null
    return {
      role: 'assistant',
      parts: [
        {
          kind: 'toolCall',
          id: stringOrNull(item.call_id) ?? stringOrNull(item.id),
          name,
          input: parseMaybeJson(item.arguments ?? null),
        },
      ],
    }
  }
  if (item.type === 'function_call_output') {
    return {
      role: 'tool',
      parts: [
        {
          kind: 'toolResult',
          id: stringOrNull(item.call_id),
          name: null,
          output: parseMaybeJson(item.output ?? null),
          isError: false,
        },
      ],
    }
  }
  return null
}

export function parseOpenAIResponsesRequest(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  if (!root) return []

  const messages: ConversationMessage[] = []
  const instructions = stringOrNull(root.instructions)
  if (instructions) messages.push({ role: 'system', parts: [{ kind: 'text', text: instructions }] })

  if (typeof root.input === 'string' && root.input !== '') {
    messages.push({ role: 'user', parts: [{ kind: 'text', text: root.input }] })
  } else {
    for (const item of asArray(root.input)) {
      const message = parseOpenAIResponseItem(item)
      if (message) messages.push(message)
    }
  }
  return messages
}

export function parseOpenAIResponsesResponse(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  const assistantParts: ConversationPart[] = []
  const messages: ConversationMessage[] = []

  for (const itemValue of asArray(root?.output)) {
    const item = asRecord(itemValue)
    if (!item) continue
    if (item.type === 'message') {
      assistantParts.push(...parseOpenAIContentParts(item.content))
    } else if (item.type === 'reasoning') {
      for (const partValue of asArray(item.summary)) {
        const part = asRecord(partValue)
        pushThinking(assistantParts, part?.text)
      }
    } else if (item.type === 'function_call') {
      const name = stringOrNull(item.name)
      if (!name) continue
      assistantParts.push({
        kind: 'toolCall',
        id: stringOrNull(item.call_id) ?? stringOrNull(item.id),
        name,
        input: parseMaybeJson(item.arguments ?? null),
      })
    } else {
      const message = parseOpenAIResponseItem(item)
      if (message) messages.push(message)
    }
  }

  const assistant = messageOrNull('assistant', assistantParts)
  return assistant ? [...messages, assistant] : messages
}

function parseAnthropicSystem(system: unknown): ConversationMessage | null {
  const parts: ConversationPart[] = []
  if (typeof system === 'string') {
    pushText(parts, system)
  } else {
    for (const blockValue of asArray(system)) {
      const block = asRecord(blockValue)
      if (block?.type === 'text') pushText(parts, block.text)
    }
  }
  return messageOrNull('system', parts)
}

function parseAnthropicContent(content: unknown): ConversationPart[] {
  const parts: ConversationPart[] = []
  if (typeof content === 'string') {
    pushText(parts, content)
    return parts
  }

  for (const blockValue of asArray(content)) {
    const block = asRecord(blockValue)
    if (!block) continue
    if (block.type === 'text') {
      pushText(parts, block.text)
    } else if (block.type === 'thinking') {
      pushThinking(parts, block.thinking)
    } else if (block.type === 'tool_use') {
      const name = stringOrNull(block.name)
      if (!name) continue
      parts.push({
        kind: 'toolCall',
        id: stringOrNull(block.id),
        name,
        input: block.input ?? null,
      })
    } else if (block.type === 'tool_result') {
      parts.push({
        kind: 'toolResult',
        id: stringOrNull(block.tool_use_id),
        name: null,
        output: block.content ?? null,
        isError: block.is_error === true,
      })
    } else if (block.type === 'image') {
      parts.push({ kind: 'media', mediaType: 'image', label: '[image]' })
    } else if (typeof block.type === 'string') {
      parts.push({ kind: 'media', mediaType: block.type, label: `[${block.type}]` })
    }
  }
  return parts
}

export function parseAnthropicRequest(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  if (!root) return []

  const messages: ConversationMessage[] = []
  const system = parseAnthropicSystem(root.system)
  if (system) messages.push(system)

  for (const itemValue of asArray(root.messages)) {
    const item = asRecord(itemValue)
    if (!item) continue
    const role = item?.role === 'assistant' ? 'assistant' : item?.role === 'user' ? 'user' : null
    if (!role) continue
    const message = messageOrNull(role, parseAnthropicContent(item.content))
    if (message) messages.push(message)
  }
  return messages
}

export function parseAnthropicResponse(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  const message = messageOrNull('assistant', parseAnthropicContent(root?.content))
  return message ? [message] : []
}

function parseGeminiParts(partsValue: unknown): ConversationPart[] {
  const parts: ConversationPart[] = []
  for (const partValue of asArray(partsValue)) {
    const part = asRecord(partValue)
    if (!part) continue
    if (typeof part.text === 'string') {
      if (part.thought === true) pushThinking(parts, part.text)
      else pushText(parts, part.text)
    } else if (asRecord(part.functionCall)) {
      const call = asRecord(part.functionCall)
      const name = stringOrNull(call?.name)
      if (!name) continue
      parts.push({ kind: 'toolCall', id: null, name, input: call?.args ?? null })
    } else if (asRecord(part.functionResponse)) {
      const result = asRecord(part.functionResponse)
      parts.push({
        kind: 'toolResult',
        id: null,
        name: stringOrNull(result?.name),
        output: result?.response ?? null,
        isError: false,
      })
    } else if (asRecord(part.inlineData)) {
      const inlineData = asRecord(part.inlineData)
      const mediaType = stringOrNull(inlineData?.mimeType) ?? 'media'
      parts.push({ kind: 'media', mediaType, label: `[${mediaType}]` })
    } else if (asRecord(part.fileData)) {
      const fileData = asRecord(part.fileData)
      const mediaType = stringOrNull(fileData?.mimeType) ?? 'file'
      parts.push({ kind: 'media', mediaType, label: `[${mediaType}]` })
    }
  }
  return parts
}

function parseGeminiContent(
  contentValue: unknown,
  defaultRole: ConversationRole,
): ConversationMessage | null {
  const content = asRecord(contentValue)
  if (!content) return null
  const role = roleFromGemini(content.role) ?? defaultRole
  return messageOrNull(role, parseGeminiParts(content.parts))
}

export function parseGeminiRequest(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  if (!root) return []

  const messages: ConversationMessage[] = []
  const system = parseGeminiContent(root.systemInstruction, 'system')
  if (system) messages.push({ ...system, role: 'system' })

  for (const content of asArray(root.contents)) {
    const message = parseGeminiContent(content, 'user')
    if (message) messages.push(message)
  }
  return messages
}

export function parseGeminiResponse(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  const candidate = asRecord(asArray(root?.candidates)[0])
  const message = parseGeminiContent(candidate?.content, 'assistant')
  return message ? [{ ...message, role: 'assistant' }] : []
}

// A search result is anchored by its citation marker: U+E200 "cite" U+E202 <ref> U+E201
// (a marker may carry several U+E202-separated refs). Dash separators between results are
// unreliable (sometimes absent, sometimes glued to the preceding text), so results are split
// on the markers, not the dashes.
const SEARCH_CITE = /\uE200cite\uE202([\s\S]*?)\uE201/g

function stripTrailingSeparators(text: string): string {
  return text.replace(/\s*-{20,}\s*$/, '').replace(/^\s+|\s+$/g, '')
}

// The title of a result is the line directly above its citation marker.
function searchTitleBefore(
  output: string,
  markerStart: number,
): { title: string; lineStart: number } {
  const nlBefore = output.lastIndexOf('\n', markerStart - 1)
  if (nlBefore < 0) return { title: output.slice(0, markerStart).trim(), lineStart: 0 }
  const prevNL = output.lastIndexOf('\n', nlBefore - 1)
  return { title: output.slice(prevNL + 1, nlBefore).trim(), lineStart: prevNL + 1 }
}

function parseSearchOutput(output: string): SearchResult[] {
  const markers = [...output.matchAll(SEARCH_CITE)]
  const results: SearchResult[] = []
  for (let k = 0; k < markers.length; k++) {
    const marker = markers[k]
    if (!marker || marker.index === undefined) continue
    const markerEnd = marker.index + marker[0].length

    const citation = (marker[1] ?? '')
      .split('\uE202')
      .map((ref) => ref.trim())
      .filter(Boolean)
      .join(', ')

    const titleLine = searchTitleBefore(output, marker.index).title

    // Content runs until the title line of the next result (or end of output).
    let regionEnd = output.length
    const next = markers[k + 1]
    if (next && next.index !== undefined) {
      regionEnd = searchTitleBefore(output, next.index).lineStart
    }
    const region = output.slice(markerEnd, regionEnd)

    let title = titleLine
    let url: string | null = null
    const urlMatch = titleLine.match(/^(.*?)\s*\((https?:\/\/[^\s)]+)\)\s*$/)
    if (urlMatch) {
      title = urlMatch[1]?.trim() ?? titleLine
      url = urlMatch[2] ?? null
    }

    const wordlim = region.match(/\[wordlim:\s*([^\]]+)\]/)?.[1]?.trim() ?? null
    const published = region.match(/Published:\s*([^;]+);/)?.[1]?.trim() ?? null
    const crawled = region.match(/Crawled:\s*([^;]+);/)?.[1]?.trim() ?? null

    // Strip the three metadata markers (rendered as badges) and the trailing result
    // separator; the rest is the Markdown content.
    const content = stripTrailingSeparators(
      region
        .replace(/\[wordlim:\s*[^\]]+\]/, '')
        .replace(/Published:\s*[^;]+;\s*/, '')
        .replace(/Crawled:\s*[^;]+;\s*/, ''),
    )

    if (!title && !content) continue
    results.push({ citation, title, url, wordlim, published, crawled, content })
  }
  return results
}

export function extractSearchResults(json: unknown): SearchResult[] {
  const output = asRecord(json)?.output
  return typeof output === 'string' ? parseSearchOutput(output) : []
}

export function parseSearchResponse(json: unknown): ConversationMessage[] {
  const results = extractSearchResults(json)
  return results.length
    ? [{ role: 'assistant', parts: [{ kind: 'searchResults', results }] }]
    : []
}

function formatFromAggregated(format: AggregatedFormat | undefined): ConversationFormat | null {
  switch (format) {
    case 'openaiChatCompletions':
      return 'openaiChat'
    case 'openaiResponses':
      return 'openaiResponses'
    case 'anthropicMessages':
      return 'anthropic'
    case 'geminiStreamGenerateContent':
      return 'gemini'
    default:
      return null
  }
}

export function parseRequestConversation(json: unknown): ConversationMessage[] | null {
  const format = detectFormat(json, 'request')
  if (!format) return null
  switch (format) {
    case 'openaiChat':
      return parseOpenAIChatRequest(json)
    case 'openaiResponses':
      return parseOpenAIResponsesRequest(json)
    case 'anthropic':
      return parseAnthropicRequest(json)
    case 'gemini':
      return parseGeminiRequest(json)
    case 'openaiSearch':
      return []
  }
}

export function parseResponseConversation(
  json: unknown,
  format?: AggregatedFormat,
): ConversationMessage[] | null {
  const detected = formatFromAggregated(format) ?? detectFormat(json, 'response')
  if (!detected) return null
  switch (detected) {
    case 'openaiChat':
      return parseOpenAIChatResponse(json)
    case 'openaiResponses':
      return parseOpenAIResponsesResponse(json)
    case 'anthropic':
      return parseAnthropicResponse(json)
    case 'gemini':
      return parseGeminiResponse(json)
    case 'openaiSearch':
      return parseSearchResponse(json)
  }
}

export function hasConversationMessages(messages: ConversationMessage[] | null): boolean {
  return !!messages?.some((message) => message.parts.length > 0)
}

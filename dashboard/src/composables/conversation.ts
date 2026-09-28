import type { AggregatedFormat } from '@/components/artifactTypes'
import { toolResultSegments } from '@/lib/toolFormat'
import { imageFromBase64, imageFromUrl, type ImageSource } from './images'

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

export type ToolResultContent =
  | { kind: 'text'; text: string }
  | { kind: 'media'; mediaType: string; label: string; image: ImageSource | null }
  | { kind: 'json'; value: unknown }

export type ConversationPart =
  | { kind: 'text'; text: string }
  // `text: null` is a thinking block that carried no plaintext (signature only / redacted).
  | { kind: 'thinking'; text: string | null }
  | { kind: 'toolCall'; id: string | null; name: string; input: unknown }
  | {
      kind: 'toolResult'
      id: string | null
      name: string | null
      output: unknown
      // Segmented view of `output` when it is a string or an array of typed blocks.
      content: ToolResultContent[] | null
      isError: boolean
    }
  | { kind: 'media'; mediaType: string; label: string; image: ImageSource | null }
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
  | 'openaiImages'

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

function pushHiddenThinking(parts: ConversationPart[]) {
  parts.push({ kind: 'thinking', text: null })
}

function pushMedia(parts: ConversationPart[], mediaType: string, image: ImageSource | null = null) {
  parts.push({ kind: 'media', mediaType, label: `[${mediaType}]`, image })
}

function anthropicImage(block: Record<string, unknown>): ImageSource | null {
  const source = asRecord(block.source)
  if (source?.type === 'base64') return imageFromBase64(source.data, source.media_type)
  if (source?.type === 'url') return imageFromUrl(source.url)
  return null
}

function imageContent(image: ImageSource | null): ToolResultContent {
  return { kind: 'media', mediaType: 'image', label: '[image]', image }
}

function toolResultContent(output: unknown): ToolResultContent[] | null {
  const segments = toolResultSegments(output)
  if (!segments) return null
  return segments.map((segment): ToolResultContent => {
    if (segment.kind === 'text') return { kind: 'text', text: segment.text }
    const block = segment.block
    if (block.type === 'image') return imageContent(anthropicImage(block))
    if (block.type === 'input_image') return imageContent(imageFromUrl(block.image_url))
    return { kind: 'json', value: block }
  })
}

function toolResultPart(
  id: string | null,
  name: string | null,
  output: unknown,
  isError: boolean,
): ConversationPart {
  return { kind: 'toolResult', id, name, output, content: toolResultContent(output), isError }
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

const ANTHROPIC_BLOCK_TYPES = new Set([
  'tool_use',
  'tool_result',
  'thinking',
  'redacted_thinking',
  'server_tool_use',
  'mcp_tool_use',
  'image',
  'document',
])

// Server-side tool results: web_search_tool_result, web_fetch_tool_result,
// code_execution_tool_result, mcp_tool_result …
function isAnthropicServerToolResult(type: unknown): boolean {
  return typeof type === 'string' && type.endsWith('_tool_result')
}

function isAnthropicToolCall(type: unknown): boolean {
  return type === 'tool_use' || type === 'server_tool_use' || type === 'mcp_tool_use'
}

// Only signals OpenAI Chat never carries count: `role: "system"` messages are sent
// by both formats (Claude Code inserts them into Anthropic `messages`).
function hasAnthropicFeatures(root: Record<string, unknown>, messages: unknown[]): boolean {
  if ('system' in root) return true
  for (const itemValue of messages) {
    const item = asRecord(itemValue)
    for (const blockValue of asArray(item?.content)) {
      const type = asRecord(blockValue)?.type
      if (typeof type === 'string' && ANTHROPIC_BLOCK_TYPES.has(type)) return true
      if (isAnthropicServerToolResult(type)) return true
    }
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
    if (!messages.length) return null
    // Anthropic wins when both formats' signals are present. Everything else is
    // OpenAI Chat — including messages with neither side's own signals (plain
    // system / user / assistant text), which both parsers read identically.
    if (hasAnthropicFeatures(root, messages)) return 'anthropic'
    return 'openaiChat'
  }

  if (typeof root.output === 'string') return 'openaiSearch'
  if (Array.isArray(root.candidates)) return 'gemini'
  // Shape-matched on the first element so an embeddings response (`data: [{embedding}]`)
  // doesn't land here.
  if (Array.isArray(root.data)) {
    const first = asRecord(root.data[0])
    if (typeof first?.b64_json === 'string' || typeof first?.url === 'string') return 'openaiImages'
  }
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
      // Chat Completions wraps the URL in an object; Responses' input_image
      // carries it as a bare string.
      pushMedia(parts, 'image', imageFromUrl(asRecord(part.image_url)?.url))
    } else if (part.type === 'input_image') {
      pushMedia(parts, 'image', imageFromUrl(part.image_url))
    } else if (part.type === 'input_audio') {
      pushMedia(parts, 'audio')
    } else if (part.type === 'input_file') {
      pushMedia(parts, 'file')
    } else if (typeof part.type === 'string') {
      pushMedia(parts, part.type)
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
    parts.push(toolResultPart(stringOrNull(message.tool_call_id), null, output, false))
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

// `result` is bare base64 with no accompanying MIME field, so the media type
// comes from sniffing the bytes.
function imageGenerationParts(item: Record<string, unknown>): ConversationPart[] {
  const image = imageFromBase64(item.result)
  if (!image) return []
  const parts: ConversationPart[] = []
  pushMedia(parts, 'image', image)
  return parts
}

function responseCallId(item: Record<string, unknown>): string | null {
  return stringOrNull(item.call_id) ?? stringOrNull(item.id)
}

function responseToolCall(
  item: Record<string, unknown>,
  name: string | null,
  input: unknown,
): ConversationMessage | null {
  if (!name) return null
  return { role: 'assistant', parts: [{ kind: 'toolCall', id: responseCallId(item), name, input }] }
}

function responseToolResult(item: Record<string, unknown>, output: unknown): ConversationMessage {
  return {
    role: 'tool',
    parts: [toolResultPart(stringOrNull(item.call_id), null, output, false)],
  }
}

// Shared by request `input[]` and response `output[]`: a multi-turn conversation
// replays the previous turns' reasoning / tool calls / image_generation_call in `input`.
function responseItemParts(itemValue: unknown): ConversationMessage | null {
  const item = asRecord(itemValue)
  if (!item) return null

  switch (item.type) {
    case 'message':
      return messageOrNull(responseRole(item.role), parseOpenAIContentParts(item.content))
    case 'reasoning': {
      const parts: ConversationPart[] = []
      for (const partValue of asArray(item.summary)) pushThinking(parts, asRecord(partValue)?.text)
      // Encrypted reasoning carries no summary text.
      if (!parts.length) pushHiddenThinking(parts)
      return { role: 'assistant', parts }
    }
    case 'function_call':
      return responseToolCall(item, stringOrNull(item.name), parseMaybeJson(item.arguments ?? null))
    // A custom tool's input is free-form text, not JSON arguments.
    case 'custom_tool_call':
      return responseToolCall(item, stringOrNull(item.name), item.input ?? null)
    case 'web_search_call':
      return responseToolCall(item, 'web_search', item.action ?? null)
    case 'function_call_output':
      return responseToolResult(item, parseMaybeJson(item.output ?? null))
    case 'custom_tool_call_output':
      return responseToolResult(item, item.output ?? null)
    case 'image_generation_call': {
      const parts = imageGenerationParts(item)
      return parts.length ? { role: 'assistant', parts } : null
    }
    default:
      return null
  }
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
      const message = responseItemParts(item)
      if (message) messages.push(message)
    }
  }
  return messages
}

export function parseOpenAIResponsesResponse(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  const assistantParts: ConversationPart[] = []
  const messages: ConversationMessage[] = []

  for (const item of asArray(root?.output)) {
    const message = responseItemParts(item)
    if (!message) continue
    if (message.role === 'assistant') assistantParts.push(...message.parts)
    else messages.push(message)
  }

  const assistant = messageOrNull('assistant', assistantParts)
  return assistant ? [...messages, assistant] : messages
}

export function parseOpenAIImagesResponse(json: unknown): ConversationMessage[] {
  const root = asRecord(json)
  const parts: ConversationPart[] = []
  for (const itemValue of asArray(root?.data)) {
    const item = asRecord(itemValue)
    if (!item) continue
    // `output_format: "png"` sits on the root and is a bare format word, not a
    // MIME type — imageFromBase64 sniffs the bytes instead.
    const image = imageFromBase64(item.b64_json) ?? imageFromUrl(item.url)
    if (image) pushMedia(parts, 'image', image)
  }
  const message = messageOrNull('assistant', parts)
  return message ? [message] : []
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
      // Signature-only thinking (`thinking: ""`) still shows that the model thought.
      if (typeof block.thinking === 'string' && block.thinking !== '') {
        pushThinking(parts, block.thinking)
      } else {
        pushHiddenThinking(parts)
      }
    } else if (block.type === 'redacted_thinking') {
      pushHiddenThinking(parts)
    } else if (isAnthropicToolCall(block.type)) {
      const name = stringOrNull(block.name)
      if (!name) continue
      parts.push({
        kind: 'toolCall',
        id: stringOrNull(block.id),
        name,
        input: block.input ?? null,
      })
    } else if (block.type === 'tool_result' || isAnthropicServerToolResult(block.type)) {
      parts.push(
        toolResultPart(
          stringOrNull(block.tool_use_id),
          null,
          block.content ?? null,
          block.is_error === true,
        ),
      )
    } else if (block.type === 'image') {
      pushMedia(parts, 'image', anthropicImage(block))
    } else if (typeof block.type === 'string') {
      pushMedia(parts, block.type)
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
    const role =
      item.role === 'assistant' || item.role === 'user' || item.role === 'system' ? item.role : null
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
      parts.push(toolResultPart(null, stringOrNull(result?.name), result?.response ?? null, false))
    } else if (asRecord(part.inlineData)) {
      const inlineData = asRecord(part.inlineData)
      const mediaType = stringOrNull(inlineData?.mimeType) ?? 'media'
      pushMedia(parts, mediaType, imageFromBase64(inlineData?.data, inlineData?.mimeType))
    } else if (asRecord(part.fileData)) {
      const fileData = asRecord(part.fileData)
      const mediaType = stringOrNull(fileData?.mimeType) ?? 'file'
      // A `gs://` fileUri fails the scheme whitelist and stays a chip.
      pushMedia(parts, mediaType, imageFromUrl(fileData?.fileUri))
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
  return results.length ? [{ role: 'assistant', parts: [{ kind: 'searchResults', results }] }] : []
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
    case 'openaiImages':
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
    case 'openaiImages':
      return parseOpenAIImagesResponse(json)
  }
}

// Tool results mostly carry only the call id; fill in the name from the matching call
// anywhere in the conversation (request history + response).
export function linkToolResultNames(messages: ConversationMessage[]): ConversationMessage[] {
  const names = new Map<string, string>()
  for (const message of messages) {
    for (const part of message.parts) {
      if (part.kind === 'toolCall' && part.id !== null) names.set(part.id, part.name)
    }
  }
  return messages.map((message) => {
    if (!message.parts.some((part) => part.kind === 'toolResult' && part.name === null)) {
      return message
    }
    return {
      ...message,
      parts: message.parts.map((part) => {
        if (part.kind !== 'toolResult' || part.name !== null || part.id === null) return part
        const name = names.get(part.id)
        return name === undefined ? part : { ...part, name }
      }),
    }
  })
}

export function hasConversationMessages(messages: ConversationMessage[] | null): boolean {
  return !!messages?.some((message) => message.parts.length > 0)
}

export function collectConversationImages(messages: ConversationMessage[]): ImageSource[] {
  const images: ImageSource[] = []
  for (const message of messages) {
    for (const part of message.parts) {
      if (part.kind === 'media' && part.image) images.push(part.image)
    }
  }
  return images
}

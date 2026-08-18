import type { EndpointView } from '@/api'

// The set of request formats the test view can construct a body for. These are
// the source formats supported by the unified gateway plus the upstream
// endpoint types that map onto them.
export type TestFormat =
  | 'anthropicMessages'
  | 'openaiChatCompletions'
  | 'openaiResponses'
  | 'geminiGenerateContent'
  | 'geminiStreamGenerateContent'

export interface TestFields {
  model: string
  system: string
  maxTokens: number
  userMessage: string
  stream: boolean
}

// endpointTypeToFormat maps an endpoint's endpointType onto a TestFormat. Types
// that the test view cannot build a body for (general, exaSearch, modelList,
// anthropicCountTokens, codexCompact, codexSearchV1Alpha, unknown) return null
// so the caller can disable sending.
export function endpointTypeToFormat(endpointType: EndpointView['endpointType']): TestFormat | null {
  switch (endpointType) {
    case 'anthropicMessages':
      return 'anthropicMessages'
    case 'openaiChatCompletions':
      return 'openaiChatCompletions'
    case 'openaiResponses':
      return 'openaiResponses'
    case 'geminiGenerateContent':
      return 'geminiGenerateContent'
    case 'geminiStreamGenerateContent':
      return 'geminiStreamGenerateContent'
    default:
      return null
  }
}

// isGeminiFormat is true for the two Gemini formats, where streaming is encoded
// in the URL path (`:streamGenerateContent`) rather than a body field, and the
// model lives in the URL rather than the body.
export function isGeminiFormat(format: TestFormat): boolean {
  return format === 'geminiGenerateContent' || format === 'geminiStreamGenerateContent'
}

// buildTestBody produces the request body for the given format from the shared
// structured fields. An empty `system` is omitted entirely. The returned value
// is a plain object; the caller is responsible for JSON-serializing it.
export function buildTestBody(format: TestFormat, fields: TestFields): Record<string, unknown> {
  const { model, system, maxTokens, userMessage, stream } = fields
  switch (format) {
    case 'anthropicMessages': {
      const body: Record<string, unknown> = {
        model,
        max_tokens: maxTokens,
        messages: [{ role: 'user', content: userMessage }],
        stream,
      }
      if (system) body.system = system
      return body
    }
    case 'openaiChatCompletions': {
      const messages: Record<string, unknown>[] = []
      if (system) messages.push({ role: 'system', content: system })
      messages.push({ role: 'user', content: userMessage })
      return {
        model,
        max_tokens: maxTokens,
        messages,
        stream,
      }
    }
    case 'openaiResponses': {
      const body: Record<string, unknown> = {
        model,
        max_output_tokens: maxTokens,
        input: userMessage,
        stream,
      }
      if (system) body.instructions = system
      return body
    }
    case 'geminiGenerateContent':
    case 'geminiStreamGenerateContent': {
      // Streaming is determined by the URL path; the model lives in the URL.
      // No `stream` or `model` field goes in the body.
      const body: Record<string, unknown> = {
        contents: [{ role: 'user', parts: [{ text: userMessage }] }],
        generationConfig: { maxOutputTokens: maxTokens },
      }
      if (system) body.systemInstruction = { parts: [{ text: system }] }
      return body
    }
  }
}

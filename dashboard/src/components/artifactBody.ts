import type { ArtifactPayload } from './artifactTypes'

export type ArtifactHeaders = Record<string, string[]>

export interface ParsedJsonBody {
  ok: boolean
  value: unknown
  error: string
}

export function contentTypeHeaderValue(headers: ArtifactHeaders | undefined): string {
  if (!headers) return ''
  for (const [name, values] of Object.entries(headers)) {
    if (name.toLowerCase() === 'content-type') return values.join(', ')
  }
  return ''
}

export function isJsonContentType(headers: ArtifactHeaders | undefined): boolean {
  const value = contentTypeHeaderValue(headers)
  if (!value) return false
  const mediaType = value.split(';', 1)[0]?.toLowerCase() ?? ''
  return mediaType === 'application/json' || mediaType.endsWith('+json')
}

export function parseJsonBody(
  body: string | undefined,
  bodyEncoding: string | undefined,
): ParsedJsonBody {
  if (bodyEncoding === 'base64') {
    return { ok: false, value: null, error: '二进制内容不能解析为 JSON' }
  }
  // An empty body counts as absent: JSON.parse('') only ever yields a bare
  // "unexpected end of data" that says less than the plain fact.
  if (!body) {
    return { ok: false, value: null, error: '没有 body 内容' }
  }
  try {
    return { ok: true, value: JSON.parse(body), error: '' }
  } catch (e) {
    return {
      ok: false,
      value: null,
      error: e instanceof Error ? `JSON 解析失败: ${e.message}` : 'JSON 解析失败',
    }
  }
}

export function rawBodyText(body: string | undefined, bodyEncoding: string | undefined): string {
  if (bodyEncoding === 'base64') return ''
  return body ?? ''
}

/**
 * Escape a string for safe use inside a single-quoted bash literal.
 * Single quotes cannot be escaped inside `'...'`, so we close the quote,
 * add an escaped single quote, and reopen: `'` -> `'\''`.
 */
function bashEscape(value: string): string {
  return value.replace(/'/g, "'\\''")
}

/**
 * Build a bash-formatted cURL command from an artifact payload.
 * Returns an empty string if the payload has no URL.
 */
export function buildCurlCommand(payload: ArtifactPayload): string {
  if (!payload.url) return ''

  const method = (payload.method || 'GET').toUpperCase()
  const parts: string[] = ['curl']

  if (method !== 'GET') {
    parts.push(`-X ${method}`)
  }

  parts.push(`'${bashEscape(payload.url)}'`)

  if (payload.headers) {
    for (const [name, values] of Object.entries(payload.headers)) {
      parts.push(`-H '${bashEscape(name)}: ${bashEscape(values.join(', '))}'`)
    }
  }

  const body = rawBodyText(payload.body, payload.bodyEncoding)
  if (body) {
    parts.push(`-d '${bashEscape(body)}'`)
  }

  // Join with line continuations (4-space indent) for readability
  return parts.join(' \\\n    ')
}

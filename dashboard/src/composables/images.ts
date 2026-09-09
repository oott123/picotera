// Turning an arbitrary payload field into something an <img> can display.
// This module knows nothing about LLM formats — callers (conversation.ts) map
// their own shapes onto the two entry points below.

export interface ImageSource {
  /** Ready for `<img :src>`: a data: URL or an http(s) URL. */
  src: string
  /** 'image/png' etc.; empty when a remote URL gives us nothing to go on. */
  mediaType: string
  /** Decoded byte count; null when the bytes are not in hand (remote URL). */
  byteLength: number | null
}

const DATA_URL_HEAD = /^data:(image\/[a-zA-Z0-9.+-]+)(;base64)?,/

function decodeBase64Prefix(data: string): Uint8Array | null {
  // atob needs whole 4-char groups, so the probe is truncated down to one.
  const usable = Math.min(24, data.length - (data.length % 4))
  if (usable < 4) return null
  try {
    const binary = atob(data.slice(0, usable))
    const bytes = new Uint8Array(binary.length)
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
    return bytes
  } catch {
    return null
  }
}

function matches(bytes: Uint8Array, offset: number, signature: number[]): boolean {
  if (bytes.length < offset + signature.length) return false
  return signature.every((byte, i) => bytes[offset + i] === byte)
}

function sniffImageMediaType(bytes: Uint8Array): string | null {
  if (matches(bytes, 0, [0x89, 0x50, 0x4e, 0x47])) return 'image/png'
  if (matches(bytes, 0, [0xff, 0xd8, 0xff])) return 'image/jpeg'
  if (matches(bytes, 0, [0x47, 0x49, 0x46, 0x38])) return 'image/gif'
  if (matches(bytes, 0, [0x52, 0x49, 0x46, 0x46]) && matches(bytes, 8, [0x57, 0x45, 0x42, 0x50])) {
    return 'image/webp'
  }
  return null
}

function base64ByteLength(data: string): number {
  let padding = 0
  if (data.endsWith('==')) padding = 2
  else if (data.endsWith('=')) padding = 1
  return Math.max(0, Math.floor(data.length / 4) * 3 - padding)
}

/**
 * `declaredMediaType` is only trusted when it is a real MIME type (Anthropic's
 * `source.media_type`, Gemini's `inlineData.mimeType`). Bare format words such
 * as the Images API's `output_format: "png"` are not — the bytes decide, and a
 * signature we don't recognize yields null rather than a guess.
 */
export function imageFromBase64(data: unknown, declaredMediaType?: unknown): ImageSource | null {
  if (typeof data !== 'string' || data === '') return null

  let mediaType: string | null = null
  if (typeof declaredMediaType === 'string' && declaredMediaType.startsWith('image/')) {
    mediaType = declaredMediaType
  } else {
    const bytes = decodeBase64Prefix(data)
    mediaType = bytes ? sniffImageMediaType(bytes) : null
  }
  if (!mediaType) return null

  return {
    src: `data:${mediaType};base64,${data}`,
    mediaType,
    byteLength: base64ByteLength(data),
  }
}

/**
 * Scheme whitelist: `data:image/…` and http(s) only. These URLs come from
 * untrusted upstream / client payloads and go straight into `<img :src>`
 * without passing through DOMPurify, so anything else (`gs://`, `file:`,
 * `javascript:`) is rejected here and falls back to a text chip.
 */
export function imageFromUrl(url: unknown): ImageSource | null {
  if (typeof url !== 'string' || url === '') return null

  if (url.startsWith('data:')) {
    const head = DATA_URL_HEAD.exec(url)
    if (!head) return null
    const mediaType = head[1] ?? ''
    const base64 = head[2] !== undefined
    return {
      src: url,
      mediaType,
      byteLength: base64 ? base64ByteLength(url.slice(head[0].length)) : null,
    }
  }

  if (url.startsWith('http://') || url.startsWith('https://')) {
    return { src: url, mediaType: '', byteLength: null }
  }
  return null
}

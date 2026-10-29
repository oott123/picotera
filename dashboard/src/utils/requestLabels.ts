// Built-in readable names for unified gateway routes, keyed by the route
// pattern recorded on request rows. Unified routes are runtime constants on the
// server, not endpoint-table rows, so the dashboard carries its own names and
// renders a "统一网关" tag alongside them.
//
// The codex mount serves open-ended sub-paths, so its entries name the ones we
// know about plus the prefix itself as a fallback for the rest.
const UNIFIED_ENDPOINT_NAMES: Record<string, string> = {
  '/api/unified/v1/messages': 'Anthropic Messages',
  '/api/unified/v1/responses': 'OpenAI Responses',
  '/api/unified/v1/chat/completions': 'OpenAI Chat Completions',
  '/api/unified/v1beta/models/{model}:generateContent': 'Gemini 生成内容',
  '/api/unified/v1beta/models/{model}:streamGenerateContent': 'Gemini 流式生成',
  '/api/unified/codex': 'Codex',
  '/api/unified/codex/responses': 'Codex Responses',
  '/api/unified/codex/responses/compact': 'Codex 压缩',
  '/api/unified/codex/alpha/search': 'Codex 搜索 v1alpha',
  '/api/unified/v1/embeddings': 'OpenAI 特征提取',
  '/api/unified/v1/messages/count_tokens': 'Anthropic Tokens 计数',
}

export function isUnifiedEndpoint(path: string | undefined | null): boolean {
  return !!path && path.startsWith('/api/unified/')
}

export function unifiedEndpointName(path: string): string {
  return (
    UNIFIED_ENDPOINT_NAMES[path] ??
    longestPrefixName(Object.entries(UNIFIED_ENDPOINT_NAMES), path) ??
    path
  )
}

/**
 * Resolve a path against (prefix, name) entries by longest matching prefix,
 * where a prefix only matches on a `/` boundary. Prefix endpoints record their
 * concrete sub-path on request rows while the name table only knows the prefix,
 * so an exact lookup alone would leave those rows showing a raw path.
 */
export function longestPrefixName(
  names: Iterable<readonly [string, string]>,
  path: string,
): string | undefined {
  let best: string | undefined
  let bestLen = -1
  for (const [prefix, name] of names) {
    if (path.startsWith(prefix + '/') && prefix.length > bestLen) {
      best = name
      bestLen = prefix.length
    }
  }
  return best
}

// Where a request's inferred model came from — mirrors the
// InferredModelSource* constants in pkg/db/request_constants.go. Source 0
// ("nothing inferred") has no label.
export function inferredModelSourceLabel(source: number | undefined | null): string {
  switch (source) {
    case 1:
      return '思维链'
    case 2:
      return '响应'
    default:
      return ''
  }
}

export function finishReasonLabel(reason: number | undefined | null): string {
  switch (reason) {
    case 1:
      return '内部错误'
    case 2:
      return '已取消'
    case 3:
      return '正常结束'
    case 4:
      return '请求头超时'
    case 5:
      return '读取超时'
    case 6:
      return '流式错误'
    case 7:
      return '控制台打断'
    default:
      return reason === undefined || reason === null ? '—' : String(reason)
  }
}

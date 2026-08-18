// Built-in readable names for unified gateway routes, keyed by the route
// pattern recorded on request rows. Unified routes are runtime constants on the
// server, not endpoint-table rows, so the dashboard carries its own names and
// renders a "统一网关" tag alongside them.
const UNIFIED_ENDPOINT_NAMES: Record<string, string> = {
  '/api/unified/v1/messages': 'Anthropic Messages',
  '/api/unified/v1/responses': 'OpenAI Responses',
  '/api/unified/v1/chat/completions': 'OpenAI Chat Completions',
  '/api/unified/v1beta/models/{model}:generateContent': 'Gemini 生成内容',
  '/api/unified/v1beta/models/{model}:streamGenerateContent': 'Gemini 流式生成',
  '/api/unified/codex/responses': 'Codex Responses',
  '/api/unified/codex/responses/compact': 'Codex 压缩',
  '/api/unified/v1/alpha/search': 'Codex 搜索 v1alpha',
  '/api/unified/v1/embeddings': 'OpenAI 特征提取',
}

export function isUnifiedEndpoint(path: string | undefined | null): boolean {
  return !!path && path.startsWith('/api/unified/')
}

export function unifiedEndpointName(path: string): string {
  return UNIFIED_ENDPOINT_NAMES[path] ?? path
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

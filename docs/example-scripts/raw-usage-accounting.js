// 按上游原始用量计费与记账
//
// 归一化后的 toolUsage 会丢掉全零条目、五个 token 列也会把缓存从输入里扣掉，
// 而 usageRaw / toolUsageRaw 是上游报的原文。适用于 OpenAI Responses / Codex
// 上游：工具用量按 tool_usage 原始计数器计价，推理 token 数打成请求注解。

const CURRENCY = 'USD'
const PER_SEARCH = 0.01
const PER_IMAGE = 0.04

picotera.hooks.getToolUsageCost.tap('price-from-raw', function (ctx, input) {
  const raw = input.toolUsageRaw
  if (!raw) return

  const searches = raw.web_search?.num_requests ?? 0
  const images = raw.image_gen?.num_images ?? 0
  const cost = searches * PER_SEARCH + images * PER_IMAGE
  if (!cost) return

  console.log(`搜索 ${searches} 次、出图 ${images} 张，工具费用 ${cost.toFixed(6)} ${CURRENCY}`)

  return { toolUsage: input.toolUsage, toolCost: cost, toolCostCurrency: CURRENCY }
})

picotera.hooks.requestFinished.tap('record-reasoning-tokens', function (ctx, input) {
  const n = input.usageRaw?.output_tokens_details?.reasoning_tokens
  if (!n) return
  picotera.request.setAnnotation(input.requestId, 'reasoningTokens', String(n))
})

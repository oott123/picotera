// 工具用量计费

const CURRENCY = 'USD'

// 按次计价
const PER_REQUEST_RATES = {
  web_search: 0.01,
}

// 按 token 计价，每百万 token
const PER_MILLION_TOKEN_RATES = {
  'gpt-image-2': { input: 8, output: 30 },
  'gpt-image-2.5-flare': { input: 8, output: 30 },
  'gpt-image-2.5-sunburst': { input: 8, output: 30 },
}

picotera.hooks.getToolUsageCost.tap('tool-usage-cost', function (ctx, input) {
  let cost = 0

  for (const tool of input.toolUsage) {
    const perRequest = PER_REQUEST_RATES[tool.name]
    if (perRequest !== undefined) {
      cost += perRequest * (tool.numRequests ?? 0)
      continue
    }

    const rate = PER_MILLION_TOKEN_RATES[tool.model]
    if (!rate) {
      console.warn(`工具 ${tool.name}${tool.model ? ' 模型 ' + tool.model : ''} 没有配置费率`)
      continue
    }
    cost += (rate.input * (tool.inputTokens ?? 0) + rate.output * (tool.outputTokens ?? 0)) / 1e6
  }

  if (!cost) return

  console.log(`工具费用 ${cost.toFixed(6)} ${CURRENCY}`)

  return { toolUsage: input.toolUsage, toolCost: cost, toolCostCurrency: CURRENCY }
})

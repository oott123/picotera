import type { ExchangeRateView, Pricing, PricingTier } from '@/api'

export interface PricingExpressionResult {
  expression: string
  warnings: string[]
  error: string | null
}

function unitsPerUsdFor(byCode: Map<string, ExchangeRateView>, code: string): number | null {
  const rate = byCode.get(code)
  if (!rate) return null
  const u = rate.unitsPerUsd
  if (typeof u !== 'number' || !isFinite(u) || u <= 0) return null
  return u
}

/**
 * 将换算后的系数格式化为定点小数:最多 6 位小数、去除尾随零、整数不带小数点。
 * 6 位小数对 $/1M tokens 单价足够精确。
 */
function formatCoefficient(value: number): string {
  const fixed = value.toFixed(6)
  return fixed.replace(/\.?0+$/, '')
}

/**
 * 生成单个 tier 的成本表达式,项之间用 ` + ` 连接。
 * fmt 已包含币种换算。返回 { cost, warning }。
 */
function buildTierCost(
  tier: PricingTier,
  fmt: (v: number) => string,
): { cost: string; warning: string | null } {
  const terms: string[] = []

  // 1. 基础输入,始终包含。
  terms.push(`p * ${fmt(tier.input)}`)
  // 2. 基础输出,始终包含。
  terms.push(`c * ${fmt(tier.output)}`)

  // 3. 缓存读取:相等则合并单项,否则取较贵值并警告。
  let warning: string | null = null
  let crValue: number
  if (tier.cacheRead === tier.implicitCacheRead) {
    crValue = tier.cacheRead
  } else {
    crValue = Math.max(tier.cacheRead, tier.implicitCacheRead)
    warning = `缓存读取 ${tier.cacheRead} 与隐式缓存读取 ${tier.implicitCacheRead} 不一致,已按较贵值 ${crValue} 导出为 cr`
  }
  if (crValue > 0) terms.push(`cr * ${fmt(crValue)}`)

  // 4. 缓存写入:相等则合并为 cc(省略 cc1h),否则分别输出。
  if (tier.cacheWrite === tier.cacheWrite1h) {
    if (tier.cacheWrite > 0) terms.push(`cc * ${fmt(tier.cacheWrite)}`)
  } else {
    if (tier.cacheWrite > 0) terms.push(`cc * ${fmt(tier.cacheWrite)}`)
    if (tier.cacheWrite1h > 0) terms.push(`cc1h * ${fmt(tier.cacheWrite1h)}`)
  }

  return { cost: terms.join(' + '), warning }
}

/**
 * 将结构化价格翻译成 billing expression 表达式字符串。纯前端,不做求值。
 *
 * - 单一 tier → `tier("base", <cost>)`。
 * - 多 tier → 嵌套三元,条件用 `len < {nextMin}`,命名 `tier_{i}`,多行缩进。
 * - 系数按目标币种换算并格式化。
 * - 币种缺失 / unitsPerUsd 非正时 fail fast,返回 error 且不生成表达式。
 */
export function buildPricingExpression(
  pricing: Pricing,
  targetCurrency: string,
  byCode: Map<string, ExchangeRateView>,
): PricingExpressionResult {
  const tiers = pricing.tiers ?? []
  if (tiers.length === 0) {
    return { expression: '', warnings: [], error: '该模型未定价' }
  }

  const source = pricing.currency
  let factor: number
  if (source === targetCurrency) {
    factor = 1
  } else {
    const fromUnits = unitsPerUsdFor(byCode, source)
    const toUnits = unitsPerUsdFor(byCode, targetCurrency)
    if (fromUnits == null) {
      return { expression: '', warnings: [], error: `缺少币种 ${source} 的汇率,无法换算系数` }
    }
    if (toUnits == null) {
      return {
        expression: '',
        warnings: [],
        error: `缺少币种 ${targetCurrency} 的汇率,无法换算系数`,
      }
    }
    factor = toUnits / fromUnits
  }

  const fmt = (v: number) => formatCoefficient(v * factor)

  const warnings: string[] = []
  const costs = tiers.map((tier, i) => {
    const { cost, warning } = buildTierCost(tier, fmt)
    if (warning) warnings.push(`阶梯 ${i}(minInputTokens=${tier.minInputTokens}):${warning}`)
    return cost
  })

  let expression: string
  if (tiers.length === 1) {
    expression = `tier("base", ${costs[0]})`
  } else {
    // 多档:嵌套三元。第 i 档适用于 len ∈ [tiers[i].minInputTokens, tiers[i+1].minInputTokens),
    // 故条件用 len < tiers[i+1].minInputTokens;缩进随嵌套深度递增,便于人工核对。
    const buildBranch = (i: number, indent: string): string => {
      if (i === tiers.length - 1) return `tier("tier_${i}", ${costs[i]})`
      const threshold = tiers[i + 1]!.minInputTokens
      const inner = buildBranch(i + 1, indent + '  ')
      // 仅在 else 分支本身是嵌套三元时加括号;叶子 tier 不额外包裹。
      const elseBranch = i + 1 === tiers.length - 1 ? inner : `(${inner})`
      return (
        `len < ${threshold}\n` +
        `${indent}  ? tier("tier_${i}", ${costs[i]})\n` +
        `${indent}  : ${elseBranch}`
      )
    }
    expression = buildBranch(0, '')
  }

  return { expression, warnings, error: null }
}

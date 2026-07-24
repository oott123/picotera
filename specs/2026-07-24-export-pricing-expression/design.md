# Design

## 概述

在模型列表(`ModelsView.vue`)每行为已定价模型新增一个「导出价格表达式」入口,点击弹出 `PricingExportPanel.vue`(SidePanel)。面板中用户选择目标币种,实时生成符合 billing expression 语言的表达式文本,只读展示并可一键复制。

这是**纯前端**功能:模型的结构化价格(`ModelView.pricing`)与汇率(`useExchangeRates`)已在前端可用;导出即把结构化价格翻译成表达式字符串。无后端、OpenAPI、DB 改动。

## 数据来源

- 价格:`ModelView.pricing` = `{ currency: string, tiers: PricingTier[] }`。每个 `PricingTier` 含 `minInputTokens`、`input`、`output`、`cacheRead`、`cacheWrite`、`cacheWrite1h`、`implicitCacheRead`(单位:目标币种 $/1M tokens,数值即 `Pricing.Currency` 下的单价)。`tiers` 已由后端 `Validate()` 保证:首档 `minInputTokens===0`、按 `minInputTokens` 严格升序、值非负。
- 汇率:`useExchangeRates()` 提供 `rates`(下拉选项)与 `byCode`(`code → ExchangeRateView{ unitsPerUsd, symbol, ... }`)。

## 币种换算

系数换算沿用 `useCurrencyContext.convertTo` 的公式(此处直接内联,不依赖被 provide 的 context):

```
factor = unitsPerUsd(target) / unitsPerUsd(source)
coefTarget = coefSource * factor
```

- `source` = `pricing.currency`,`target` = 用户所选币种,默认等于 `source`(此时 `factor === 1`,系数即原值)。
- 若 `source === target`,不换算。
- **Fail fast**:若所选目标币种或模型币种在汇率表中缺失 / `unitsPerUsd` 非正,面板显示错误并禁用复制,不做静默回退。

### 系数格式化

换算后的系数以定点小数输出,最多保留 6 位小数并去除尾随零(6 位小数对 $/1M 单价足够精确)。整数系数不带小数点(如 `15` 而非 `15.0`)。

## 表达式生成算法

输入:`pricing`、`targetCurrency`;输出:`{ expression: string, warnings: string[], error: string | null }`。

### 单个 tier 的成本表达式

按固定顺序拼接加法项,项之间用 ` + ` 连接:

1. `p * {input}` — 始终包含(基础输入,即使系数为 0,显式声明该阶梯对输入计价)。
2. `c * {output}` — 始终包含(基础输出)。
3. **缓存读取 `cr`**:
   - 若 `cacheRead === implicitCacheRead`:取该值为 `crValue`(规则:相等则合并单项)。
   - 否则:取 `crValue = max(cacheRead, implicitCacheRead)`(规则:取较贵),并记录一条 warning。
   - 仅当 `crValue > 0` 时追加 `cr * {crValue}`。
4. **缓存写入 `cc` / `cc1h`**:
   - 若 `cacheWrite === cacheWrite1h`:仅在 `cacheWrite > 0` 时追加 `cc * {cacheWrite}`(规则:相等则不区分,省略 `cc1h`)。
   - 否则:`cacheWrite > 0` 时追加 `cc * {cacheWrite}`;`cacheWrite1h > 0` 时追加 `cc1h * {cacheWrite1h}`。

所有系数在拼接前经过币种换算 + 格式化。`img`/`ai`/`ao`/`img_o` 不建模,不输出(其 token 自动落入 `p`/`c`)。

### tier 包裹与命名

- 单一 tier → `tier("base", <cost>)`。
- 多 tier → 第 i 个(0-based)命名 `tier("tier_{i}", <cost>)`。

### 多阶梯嵌套三元

tiers 已按 `minInputTokens` 升序。第 i 档的上界阈值 `threshold_i = tiers[i+1].minInputTokens`(即下一档的下界)。条件用 `len < {threshold_i}`(语义精确:第 i 档适用于 `len ∈ [tiers[i].minInputTokens, tiers[i+1].minInputTokens)`)。

- 2 档:
  ```
  len < {t1} ? tier("tier_0", E0) : tier("tier_1", E1)
  ```
- 3 档:
  ```
  len < {t1} ? tier("tier_0", E0) : (len < {t2} ? tier("tier_1", E1) : tier("tier_2", E2))
  ```

### 输出格式

- 单一 tier:单行 `tier("base", <cost>)`。
- 多 tier:多行缩进,风格对齐 proposal 中的示例(条件行 + `  ? tier(...)` / `  : (...)`),便于阅读与人工核对。

## 警告

当**任一** tier 满足 `cacheRead !== implicitCacheRead` 时,面板显示一条警告(`StateText` 提示样式),说明"隐式缓存读取价与缓存读取价不一致,已按较贵值导出为 `cr`",并列出涉及的档位与取值。警告**不阻断**复制(用户已确认取较贵值)。

## 面板 UI(`PricingExportPanel.vue`)

- 基于 `SidePanel`,`title = model.name`,`kicker = "导出价格"`。
- 主体:
  - 目标币种 `Select`(选项来自 `useExchangeRates().rates`,`label` 形如 `USD $`),默认 `pricing.currency`。
  - 换算失败时的错误提示(经 `SidePanel` 的 `#error` 插槽)。
  - 缓存读取不一致警告(如有)。
  - 只读表达式代码块:`<pre>` + 等宽字体(参照 `RawArtifactView.vue` 的代码块样式),`white-space: pre-wrap`。
- 底部(`#footer`):
  - 「复制表达式」按钮 → `navigator.clipboard.writeText(expression)`,复制后短暂反馈"已复制"(参照 `ApiKeysView.vue` / `RawArtifactView.vue` 的 clipboard 用法,clipboard 不可用时静默忽略)。复制在 `error` 存在时禁用。
  - 「关闭」按钮。

## 触发入口(`ModelsView.vue`)

在每行操作区(`Td actions`),于「编辑」按钮前新增一个 `IconButton`(图标 `braces`),仅当模型已定价(`m.pricing?.tiers?.length`)时渲染;未定价的行不显示。点击 `panel.open(PricingExportPanel, { model: m }, { key: \`model-pricing-export:${m.name}\` })`,并给按钮加 `:active="panel.isActive(...)"`(与现有按钮一致)。

## 复用与新增

- 复用:`SidePanel`、`Select`、`Button`、`IconButton`、`Icon`、`StateText`(均来自 `@/ui`)、`useExchangeRates`、图标 `braces` / `copy`(已存在于 `src/ui/icons/paths.ts`)。
- 新增:`dashboard/src/components/PricingExportPanel.vue`;导出算法可内联在该组件,或抽到 `dashboard/src/utils/`(见 plan)。

## 不做的事

- 不新增后端 / OpenAPI / DB / sqlc 内容。
- 不引入第三方库(遵循本地 UI 原语约定)。
- 不对未定价模型显示导出入口。

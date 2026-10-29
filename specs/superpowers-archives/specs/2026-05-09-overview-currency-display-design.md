# 概览页币种展示重构

## 背景

`OverviewView.vue` 当前自维护一个 `costCurrency` 选择器（单选），与全局
偏好 `usePreferencesStore.displayCurrency`（PreferencesMenu 中的"主要货币"）
逻辑重复且互不联动。本次改造让概览页费用展示统一遵守全局"主要货币"设置：

- 主要货币 = 某具体货币：所有费用换算为该货币，单值/单图展示。
- 主要货币 = "原始货币"（`displayCurrency === null`）：保留每种币种原值，
  Bento 用 `+` 拼接为一行；分布饼图、堆叠面积图按币种拆为多张卡片，
  在已有 2 列网格中自然铺开。

汇率配置由运营方负责保证完整；本设计假设主要货币模式下所有币种均可换算
（`useCurrency.convert` 返回 `converted: true`），不增加缺失汇率的兜底分支。

## 改造范围

仅修改 `dashboard/src/views/OverviewView.vue` 与（如必要）`dashboard/src/composables/useCurrency.ts`。
后端、`OverviewDonut.vue`、`OverviewAreaStack.vue` 不变。

## 详细设计

### 1. 移除页面本地币种选择器

- 删除 `OverviewView.vue` 中：
  - `const costCurrency = ref<string>('')`
  - `distributionCurrencies` / `seriesCurrencies` 计算属性
  - 两段 `watch(...currencies, ...)` 自动选择币种逻辑
  - 模板中两处 `<Select v-model="costCurrency">`（"分布统计"和"用量统计"
    控件栏内的"币种"/"费用币种"下拉）
  - 模板中费用卡片标题 `· {{ costCurrency }}` 后缀
- 引入 `useCurrency()`：在 `<script setup>` 中
  `const ccy = useCurrency()`，从中取 `targetCurrency`、`convert`、`format`。
- 主要货币标识：
  ```ts
  const isOriginalMode = computed(() => ccy.targetCurrency.value == null)
  ```

### 2. Bento「总费用」卡

`summaryQuery.data.value?.costs ?? []` 是 `{ amount, currency }[]`。

**主要货币模式**（`!isOriginalMode`）：

- 计算 `summaryConvertedTotal`：将 `costs` 每项 `convert` 后求和，
  目标货币恒为 `targetCurrency.value`。
- 渲染单个 `<MoneyDisplay :amount="summaryConvertedTotal" :currency="targetCurrency" />`，
  保持 `text-xl font-semibold mono tabular text-ink`。

**原始货币模式**：

- 渲染容器改为 `flex flex-row flex-wrap items-baseline gap-x-1 gap-y-0.5`。
- 对 `costs` 数组：
  ```html
  <template v-for="(c, i) in costs" :key="c.currency">
    <span v-if="i > 0" class="text-ink-faint">+</span>
    <MoneyDisplay class="text-xl font-semibold mono tabular text-ink"
                  :amount="c.amount" :currency="c.currency" />
  </template>
  ```
- `MoneyDisplay` 在原始货币模式下不会换算，输出形如 `$1.23` / `¥3.40`。
- `costs` 为空时仍显示 `—`。

### 3. 费用分布饼图

#### 3.1 `costDonutData` 重构

抽出按"行 + 币种过滤"的纯函数：

```ts
function buildCostDonutDataForCurrency(currency: string) {
  return buildItemsAndTopN(
    distributionRows.value
      .map((r) => {
        const cost = (r.costs ?? []).find((c) => c.currency === currency)
        return { key: r.key, label: dimensionLabel(distributionDimension.value, r.key),
                 value: cost?.amount ?? 0 }
      })
      .filter((d) => d.value > 0),
  )
}
```

主要货币模式下另一条路径（按行换算求和）：

```ts
const costDonutDataConverted = computed(() => {
  const target = ccy.targetCurrency.value
  if (!target) return []
  return buildItemsAndTopN(
    distributionRows.value
      .map((r) => {
        const sum = (r.costs ?? []).reduce(
          (acc, c) => acc + ccy.convert(c.amount, c.currency).amount, 0)
        return { key: r.key, label: dimensionLabel(distributionDimension.value, r.key),
                 value: sum }
      })
      .filter((d) => d.value > 0),
  )
})
```

`buildItemsAndTopN` 提取自当前 `buildDonutData` 的排序 + Top-N + "其他"
合并逻辑。

#### 3.2 渲染

把当前外层 `<div class="grid grid-cols-1 lg:grid-cols-2 gap-3">` 内的两张
卡片（Token / Cost）改成：

- Token 卡片不变。
- Cost 卡片由 `v-for` 生成（用 `<template>` 占位避免多余 div）：
  - **主要货币模式**：单卡，标题 `费用分布 · {{ targetCurrency }}`。
  - **原始货币模式**：对 `distributionCurrenciesPresent` 中每个 `currency`
    渲染一卡，标题 `费用分布 · {{ currency }}`，数据来自
    `buildCostDonutDataForCurrency(currency)`。
  - **原始模式无费用数据**：渲染单卡占位 `暂无数据`，标题 `费用分布`。

`distributionCurrenciesPresent` 计算属性（保留原 `distributionCurrencies`
的实现，只改名字）扫描所有行的 `costs[]` 收集币种。

值格式化：所有 cost donut `:value-format` 改为
`(v) => formatCurrencyCompact(v, currency)`，其中 `currency` 是该卡使用的
币种（主要货币模式下为 `targetCurrency.value`）。`formatCurrencyCompact`
见 §5。

### 4. 费用堆叠面积图

#### 4.1 `metricPoints` 重构

当前实现按 `costCurrency` 单选过滤。新策略：

- `metricPointsTokens` / `metricPointsRequests` / `metricPointsTraces`
  保持现状（直接 filter by metric）。
- 费用拆为两个 helper：
  ```ts
  function costPointsForCurrency(currency: string) {
    return (seriesData.value?.points ?? [])
      .filter((p) => p.metric === 'cost' && p.currency === currency)
      .map((p) => ({ groupKey: p.groupKey, bucketAt: p.bucketAt, value: p.value }))
  }

  const costPointsConverted = computed(() => {
    const target = ccy.targetCurrency.value
    if (!target) return []
    // 嵌套 Map 避免 groupKey/bucketAt 含分隔符导致冲突。
    const byGroup = new Map<string, Map<string, number>>()
    for (const p of seriesData.value?.points ?? []) {
      if (p.metric !== 'cost' || !p.currency) continue
      let m = byGroup.get(p.groupKey)
      if (!m) { m = new Map(); byGroup.set(p.groupKey, m) }
      const v = ccy.convert(p.value, p.currency).amount
      m.set(p.bucketAt, (m.get(p.bucketAt) ?? 0) + v)
    }
    const out: { groupKey: string; bucketAt: string; value: number }[] = []
    for (const [groupKey, m] of byGroup) {
      for (const [bucketAt, value] of m) out.push({ groupKey, bucketAt, value })
    }
    return out
  })
  ```

#### 4.2 渲染

当前 `<div class="grid grid-cols-1 lg:grid-cols-2 gap-3">` 内是 4 张卡：
Token / Cost / Requests / Traces。

新顺序：Token → 费用卡（1..N 张） → Requests → Traces。

费用卡片渲染：

- **主要货币模式**：单卡，标题 `费用 · {{ targetCurrency }}`，
  `:points="costPointsConverted"`，
  `:value-format="(v) => formatCurrencyCompact(v, targetCurrency)"`。
- **原始货币模式**：基于 `seriesCurrenciesPresent`（计算属性，从
  `seriesData.points` 中收集 `p.metric === 'cost'` 的 `currency`）渲染
  N 张卡。每张：
  - 标题 `费用 · {{ currency }}`。
  - `:points="costPointsForCurrency(currency)"`。
  - `:value-format="(v) => formatCurrencyCompact(v, currency)"`。
- **原始模式无费用数据**：单张占位卡，标题 `费用`，区域图渲染空 points
  数组（保持与之前相同行为）。

`seriesGroups` / `seriesBuckets` 在所有费用卡间共享，不变。

### 5. 数值格式化

新增本地 helper（放 `<script setup>` 内，不进 `useCurrency`，因为是页面
专用的"紧凑 + 货币符号"组合）：

```ts
function formatCurrencyCompact(v: number, code: string): string {
  if (!Number.isFinite(v)) return ''
  const abs = Math.abs(v)
  let scaled = v
  let suffix = ''
  if (abs >= 1e9) { scaled = v / 1e9; suffix = 'B' }
  else if (abs >= 1e6) { scaled = v / 1e6; suffix = 'M' }
  else if (abs >= 1e3) { scaled = v / 1e3; suffix = 'k' }
  // 对缩写值用 1 位小数；非缩写用 2 位小数（保持当前 toFixed(2) 行为）。
  const minDigits = suffix ? 1 : 2
  const maxDigits = suffix ? 1 : 2
  return ccy.format(scaled, code, { minDigits, maxDigits }) + suffix
}
```

> `ccy.format` 输出 `${symbol}${number}`（无符号时 `${number} ${code}`），
> 因此 `1.2k` 形式会渲染为 `$1.2k` / `¥1.2k`。

替换原 `compactCurrency`。`compactNumber`（不含货币的）保持不变，仍用于
Token / 请求数 / 追踪数。

### 6. 控件栏调整

"分布统计"控件栏移除"币种"下拉；只剩 `分布统计` SegmentedControl。
"用量统计"控件栏移除"费用币种"下拉；只剩 `用量统计` SegmentedControl。

## 验证

- 主要货币 = "原始货币"：
  - Bento 总费用呈 `$1.23 + ¥3.40` 单行（多币种时换行）。
  - 分布栏看到 Token 饼 + 多个币种各一张费用饼，自然 2 列流式排列。
  - 用量栏看到 Token / 多张费用 / Requests / Traces。
  - 无任何费用记录时，费用区显示一张「暂无数据」占位卡。
- 主要货币 = "USD"（且汇率齐全）：
  - Bento 总费用单行单值（USD）。
  - 分布栏只有 1 张费用饼，标题 `费用分布 · USD`。
  - 用量栏只有 1 张费用面积图，标题 `费用 · USD`。
- 切换全局主要货币（PreferencesMenu）：概览页费用区即刻随之切换。

## 不做范围

- 不改后端 `OverviewSummary` / `OverviewDistribution` / `OverviewSeries`
  返回结构。
- 不改 `MoneyDisplay`、`OverviewDonut`、`OverviewAreaStack` 三个组件。
- 不为缺失汇率添加兜底 UI；如出现未换算项，依赖 `MoneyDisplay`/
  `useCurrency` 当前的"保留原值"行为，不在概览页增加额外提示。
- 不调整 PreferencesMenu 的"主要货币"控件本身。

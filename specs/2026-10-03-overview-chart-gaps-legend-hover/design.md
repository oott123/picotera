# 设计：总览图表空数据断线、折线点与图例 hover 高亮

纯前端改动，只涉及 `dashboard/src/components/charts/` 下的三个组件。`OverviewView.vue` 和 `AdminOverviewView.vue` 共用这些组件，两个页面不需要改动；后端和 OpenAPI 不变。

## 空数据的判定

后端已经区分了「空」和「0」：

- 比率类指标（`cacheHitRate`、`upstreamSuccessRate`、`downstreamSuccessRate`、`emptyResponseRate`、`finishReasonShare`）在分母为 0 的桶里**不返回点**（`handle_overview.go` 的 `cacheInputByBG[bg] > 0` 判断、`handle_overview_outcome.go` 的 `appendRatio` / `reasonBucketTotal[bucket] == 0`）。
- 计数类指标（`tokens`、`requests`、`traces`、`cost`）每个桶都返回点，没有流量就是 0。

所以组件统一采用一条规则：**某个 (分组, 桶) 没有点就是空数据，填 ECharts 的 `'-'`；有点就画它的值，0 也照画。** 计数类图表因为每个桶都有点，表现不变；比率类图表在没有流量的桶里断开。

## OverviewLineChart

- `dataset`：去掉现在 `point.value == 0` 时跳过的分支，0 作为真实值写入；没有点的位置保持 `undefined`，series 的 `data` 映射成 `'-'`。
- 去掉 `connectNulls: true`（默认 `false`），让折线在 `'-'` 处断开。
- 画折线点：`symbol: 'circle'`、`symbolSize: 3`、`showSymbol: true`、`showAllSymbol: true`。`showAllSymbol: true` 是必须的：类目轴上默认的 `'auto'` 会按标签间隔抽掉部分点，前后都是空数据的孤立点会因此完全不可见。
- `blur` 增加 `itemStyle: { opacity: 0.3 }`，让被淡化系列的折线点和折线一起变淡。
- `emphasis.scale: 4 / 3`：ECharts 默认会把 3px 的点在 hover 时放大到 6px，这里限制为 4px。
- tooltip：过滤条件从「非 null 且非 0」改为「值是有限数字」，于是 0 会显示（例如成功率 0%），`'-'` 不显示。

## OverviewAreaStack

- `dataset`：每格初始化为 `undefined`，遇到点时再累加（`(row.values[key] ?? 0) + value`），series 的 `data` 把 `undefined` 映射成 `'-'`。完成原因图在没有流量的桶里所有类别同时缺点，整列断开，不再掉到 0；Token / 费用 / 请求数 / 追踪数每个桶都有点，不受影响。`costPointsConverted` 按后端返回的点逐桶累加，同样每个桶都有值。
- 不画点，`symbol: 'none'` 保持不变。
- tooltip：过滤条件改为「值是有限数字且不为 0」。堆叠图里 0 份额的行仍然隐藏（与现状一致），`'-'` 不显示。

## OverviewBoxplotSeries 的中位数折线

组件现在有两种模式（`boxMode`）：只有一个分组可见时画箱体（`boxplot` series）并叠加中位数折线；多个分组可见时只画各分组的中位数折线。中位数折线是普通的 `line` series（`medianSeries`），每个可见分组一条，排在箱体 series 之后。

- 空数据：`medianSeries` 的 `data` 把没有点的桶从 `null` 改为 `'-'`，删除 `connectNulls: true`，折线在空桶处断开。tooltip 已经只保留对象形式的 datum（`typeof d === 'object' && d !== null`），`'-'` 会被过滤掉，不用改。
- 折线点：只画折线的模式下与 `OverviewLineChart` 相同（`symbol: 'circle'`、`symbolSize: 3`、`showSymbol: true`、`showAllSymbol: true`），保证两侧都是空桶的单个中位数可见；箱体模式下保持 `symbol: 'none'`，箱体的中位线已经标出位置。
- `blur` 增加 `itemStyle: { opacity: 0.3 }`，让被淡化系列的折线点和折线一起变淡。
- `emphasis.scale: 4 / 3`：ECharts 默认会把 3px 的点在 hover 时放大到 6px，这里限制为 4px。

## 图例 hover 高亮

三个组件的图例都是自绘的 `<ul>`（不是 ECharts legend 组件），所以在每个 `<li>` 上加 `@mouseenter` / `@mouseleave`。三个组件的折线 / 面积 series 都已经配置了 `emphasis.focus: 'series'` 和 `blur` 样式，统一复用：通过 `useTemplateRef` 拿到 `VChart` 实例，调用 `dispatchAction`，效果与鼠标直接悬停在该系列上一致。被隐藏的分组（`hiddenKeys` 中）不会被高亮。

- `hoveredKey = ref<string | null>(null)` 记录当前 hover 的图例。
- `mouseenter(key)`：记下 `hoveredKey`，调用 `applyLegendHover()`。
- `mouseleave`：`hoveredKey = null`，调用 `applyLegendHover()`。
- `applyLegendHover()`：先 `dispatchAction({ type: 'downplay' })` 清掉所有系列的高亮；若 `hoveredKey` 对应的分组可见，再 `dispatchAction({ type: 'highlight', seriesIndex })`。用下标而不是 `seriesName`，因为不同分组的标签可能相同（例如都为空时都是 `'-'`）。
  - `OverviewLineChart` / `OverviewAreaStack`：`seriesIndex` 是该分组在 `visibleGroups` 中的下标（series 数组正是按 `visibleGroups` 生成的）。
  - `OverviewBoxplotSeries`：只在只画折线的模式下高亮，此时箱体 series 为空，`seriesIndex` 同样是 `visibleGroups` 中的下标。箱体模式下只有一个可见分组，没有可对比的其他系列；而且高亮中位数折线会按 `focus: 'series'` 把同一分组的箱体淡化，所以箱体模式下只做 `downplay`。
- 在图例上点击 / 右键（`toggleSeries` / `isolateSeries`）会改变 `visibleGroups`（盒须图还可能切换模式），series 下标随之变化，旧的高亮状态会落到错误的系列上。所以在这两个函数改完 `hiddenKeys` 后，`nextTick`（等 vue-echarts 把新 option 设进去）再调用一次 `applyLegendHover()`。

## 文档

更新 `dashboard/CLAUDE.md` 的 Charts 小节：

- 说明「无点即空、0 照画」的规则，以及三个组件的图例 hover 会高亮对应系列。
- `OverviewLineChart` 条目补充画折线点。
- `OverviewBoxplotSeries` 条目目前描述的还是旧的绘制方式（每个桶里多组箱体并排、`custom` series 画中位数折线、`boxOffset`、组件级 `focusedKey`），按现在的实现重写：单个分组可见时画箱体并叠加中位数折线，多个分组可见时只画中位数折线（带折线点）；中位数折线在空桶处断开；hover 使用 `emphasis.focus: 'series'`。

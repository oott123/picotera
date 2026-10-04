# 执行计划

设计见 `design.md`。所有路径相对 `dashboard/`。

## 1. OverviewLineChart：空数据断线 + 折线点

文件：`src/components/charts/OverviewLineChart.vue`

1. `dataset` 中删除 `if (point.value == null || point.value == 0) continue`，直接写入 `row.values[point.groupKey] = point.value`。
2. series：
   - `data: dataset.value.map((d) => d.values[g.key] ?? '-')`。
   - 删除 `connectNulls: true`。
   - `symbol: 'none'` 改为 `symbol: 'circle'`、`symbolSize: 3`、`showSymbol: true`、`showAllSymbol: true`。
   - `blur` 改为 `{ lineStyle: { opacity: 0.3 }, itemStyle: { opacity: 0.3 } }`。
3. tooltip formatter 的过滤改为 `typeof p.value === 'number' && Number.isFinite(p.value)`。

## 2. OverviewAreaStack：空数据断开

文件：`src/components/charts/OverviewAreaStack.vue`

1. `Datum.values` 类型改为 `Record<string, number | undefined>`，初始化为 `undefined`；累加保持 `(row.values[point.groupKey] ?? 0) + point.value`（去掉对 `point.value` 的 `?? 0`，`SeriesPoint.value` 是 `number`）。
2. series `data: dataset.value.map((d) => d.values[g.key] ?? '-')`。
3. tooltip formatter 的过滤改为 `typeof p.value === 'number' && Number.isFinite(p.value) && p.value !== 0`。

## 3. OverviewBoxplotSeries：中位数折线断线 + 折线点

文件：`src/components/charts/OverviewBoxplotSeries.vue`

1. `medianSeries` 的 `data` 改为 `row.map((p): StatDatum<number> | '-' => p ? { ... } : '-')`，删除 `connectNulls: true`。
2. `symbol: 'none'` 改为按模式区分：箱体模式 `symbol: 'none'`；只画折线的模式 `symbol: 'circle'`、`symbolSize: 3`、`showSymbol: true`、`showAllSymbol: true`。
3. `blur` 改为 `{ lineStyle: { opacity: 0.3 }, itemStyle: { opacity: 0.3 } }`。

## 4. 图例 hover 高亮

三个组件做相同的改动：

1. 从 `vue` 引入 `nextTick`、`useTemplateRef`；`const chartRef = useTemplateRef<InstanceType<typeof VChart>>('chart')`，模板 `<VChart ref="chart" ...>`。
2. 新增 `const hoveredKey = ref<string | null>(null)` 和：

   ```ts
   function applyLegendHover() {
     const chart = chartRef.value
     if (!chart) return
     chart.dispatchAction({ type: 'downplay' })
     if (hoveredKey.value === null) return
     const seriesIndex = visibleGroups.value.findIndex((g) => g.key === hoveredKey.value)
     if (seriesIndex === -1) return
     chart.dispatchAction({ type: 'highlight', seriesIndex })
   }

   function onLegendEnter(key: string) {
     hoveredKey.value = key
     applyLegendHover()
   }

   function onLegendLeave() {
     hoveredKey.value = null
     applyLegendHover()
   }
   ```

   `OverviewBoxplotSeries` 中在 `downplay` 之后增加 `if (boxMode.value) return`，并加一行注释说明原因（单个分组没有可对比的系列，且高亮折线会把同组箱体淡化）。

3. `toggleSeries` / `isolateSeries` 在每个改动 `hiddenKeys` 的分支之后执行 `void nextTick(applyLegendHover)`（两个函数都有提前 `return` 的分支，都要覆盖）。
4. 图例 `<li>` 加 `@mouseenter="onLegendEnter(g.key)"`、`@mouseleave="onLegendLeave"`。

## 5. 文档

更新 `dashboard/CLAUDE.md` 的 Charts 小节：

- 在列表前加一段：折线图 / 堆叠面积图 / 盒须图中，后端没有返回点的 (分组, 桶) 视为空数据并填 `'-'`（折线、面积、中位数折线在此断开），返回了的点即使是 0 也照画；计数类指标每个桶都有点，所以不会断开。三个组件的自绘图例 hover 时高亮对应系列。
- `OverviewLineChart` 条目补充：画折线点（`showAllSymbol: true`，保证空数据之间的孤立点可见）。
- `OverviewBoxplotSeries` 条目按现在的实现重写：单个分组可见时画箱体并叠加中位数折线（不画点），多个分组可见时只画中位数折线（画点）；中位数折线在空桶处断开；hover 高亮通过 `emphasis.focus: 'series'`。删去 `custom` series、`boxOffset`、`focusedKey` 的描述。

## 6. 验证

1. `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`、`pnpm --dir dashboard format` 全部通过。
2. 启动后端与 dashboard（`mise run server`、`mise run web`），在「总览」和「全览」页面检查：
   - 选一个有空闲时段的时间范围：缓存命中率、成功率、空回比例的折线在没有流量的桶处断开，孤立的点以圆点显示；某桶成功率为 0% 时画在 0 线上，tooltip 显示 0%。
   - 完成原因图在没有流量的桶处整列留空；Token / 费用 / 请求数 / 追踪数图表与改动前一致。
   - 速度盒须图：多个分组可见时，中位数折线在空桶处断开，孤立的中位数以圆点显示；隔离成单个分组时画箱体，折线不画点、同样在空桶处断开。
   - 鼠标移到三类图表的图例项上，对应系列高亮、其他系列变淡；移开后恢复；悬停在图例上点击 / 右键切换显示后，高亮落在正确的系列上（或因该系列被隐藏而全部恢复）。盒须图箱体模式下 hover 图例不改变显示。

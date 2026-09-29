# 设计：总览速度箱线图突出中位数

## 现状

`dashboard/src/components/charts/OverviewSpeedTimeline.vue` 用 ECharts `boxplot` 系列画速度分布（用户总览和管理员总览共用）。
每个箱子的 `itemStyle.color` 与 `borderColor` 都是 `groupColor(index)`，箱体填充和边框同色。
`boxplot` 的中位线、箱体轮廓、须线都用 `borderColor` 绘制，所以中位线画在同色箱体上，看不出来。

## 方案

只改边框色：`borderColor` 取填充色的深浅变体，与填充色形成对比，中位线随之显现。

- 用项目已有的 `colorjs.io` 在 oklch 空间调整明度，不引入新依赖。
- 亮色主题下边框比填充色更深（明度 -0.15），暗色主题下边框比填充色更浅（明度 +0.15），保证在两种主题下都有对比。
- 变体色由 `colors.ts` 新增的 `groupBorderColor(index)` 提供，内部根据当前主题决定方向，主题切换沿用组件里现有的 `themeVersion` 触发重算。
- 须线和箱体轮廓同样使用该边框色，这是可接受的副作用。
- 不改动后端、API 与 `openapi.yaml`。

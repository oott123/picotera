# 执行计划

1. 修改 `dashboard/src/components/charts/colors.ts`：新增 `groupBorderColor(index)`，用 `colorjs.io` 在 oklch 空间调整 `groupColor(index)` 的明度，亮色主题 -0.15，暗色主题 +0.15（主题取自 `document.documentElement.dataset.dark`）。
2. 修改 `dashboard/src/components/charts/OverviewSpeedTimeline.vue`：`boxplot` 数据项的 `itemStyle.borderColor` 改为 `groupBorderColor(s.colorIndex)`，`color` 保持 `groupColor(s.colorIndex)`。
3. 更新 `dashboard/CLAUDE.md` 中 `OverviewSpeedTimeline` 条目，补一句“边框取填充色的深浅变体，中位线借此突出”。
4. 验证：
   - `pnpm --dir dashboard type-check`
   - `pnpm --dir dashboard lint`
   - `mise run web` 打开总览与管理员总览，在亮色、暗色主题下确认每个箱体的中位线清晰可辨，位置与 tooltip 中的 med 值一致，悬停聚焦效果正常。

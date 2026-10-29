# 执行计划

1. 更新 `dashboard/src/ui/AutoDataTable.vue`
   - 在 props 中新增可选 `rowHref?: (row: Row) => string`。
   - 数据行存在 `rowHref` 时增加相对定位和链接光标样式。
   - 在行内渲染覆盖整行的 `<a>`，设置 `href`、`aria-label` 和 `tabindex="-1"`。
   - 保留 `onRowClick`，让普通左键点击继续走当前页面内行为。
   - 调整单元格内容层级，确保内容可见且已有单元格控件能继续接收点击。

2. 更新 `dashboard/src/views/TracesView.vue`
   - 删除 columns 里的 `id` / `Trace ID` 列。
   - 删除 `#cell-id` slot。
   - 新增 `traceHref(row)`，使用 `router.resolve({ name: 'requests', query: { traceId: row.id } }).href`。
   - 给 `AutoDataTable` 传入 `:row-href="traceHref"`，保留 `:on-row-click="openTrace"`。

3. 更新 `dashboard/src/views/RequestsView.vue`
   - 新增 `requestHref(row)`，使用 `router.resolve({ name: 'requestDetail', params: { requestId: row.id } }).href`。
   - 给 `AutoDataTable` 传入 `:row-href="requestHref"`，保留 `:on-row-click="(r) => openDetails(r)"`。

4. 验证
   - 运行 `pnpm --dir dashboard type-check`。
   - 运行 `pnpm --dir dashboard lint`。
   - 目视检查“追踪”页面不再显示 Trace ID 列。
   - 目视检查“追踪”和“请求”页面行 hover、普通点击、右键复制链接、在新标签页打开行为。

# 设计

## 范围

这是 dashboard 的前端交互调整，涉及：

- `dashboard/src/views/TracesView.vue`
- `dashboard/src/views/RequestsView.vue`
- `dashboard/src/ui/AutoDataTable.vue`

不修改后端、数据库、OpenAPI 合约或生成类型。

## 表格行链接

`AutoDataTable` 增加可选的 `rowHref(row) => string` 属性。存在 `rowHref` 时，组件在每个数据行内渲染一个覆盖整行的真实 `<a>` 元素，链接目标由调用方提供。

这个设计满足：

- 普通左键点击仍触发行内已有的页面行为。
- 中键、右键菜单、Cmd/Ctrl 点击、复制链接地址和“在新标签页打开”由浏览器原生处理。
- 表格仍保持当前 `DataTable` / `Tr` / `Td` 结构和视觉密度。

覆盖链接使用绝对定位铺满行区域。为避免遮挡单元格内容，数据单元格内容层级高于链接层；单元格内已有可交互控件通过 `stopPropagation` 保持当前行为。

## 追踪页面

`TracesView.vue` 从 columns 中移除 `id` 列和对应的 `cell-id` slot。

每一行的链接目标为：

```text
/requests?traceId=<trace id>
```

链接由 `router.resolve({ name: 'requests', query: { traceId: row.id } }).href` 生成，确保匹配当前 Vue Router base 配置。

普通左键点击仍调用现有 `openTrace`，在当前标签进入带 `traceId` 过滤的请求页面。

## 请求页面

`RequestsView.vue` 为每一行生成请求详情链接：

```text
/requests/<request id>
```

链接由 `router.resolve({ name: 'requestDetail', params: { requestId: row.id } }).href` 生成，确保匹配当前 Vue Router base 配置。

普通左键点击仍调用现有 `openDetails`，在当前页面打开请求详情侧边栏并同步地址栏。新标签打开该链接时进入现有 `RequestDetailView` 独立详情页。

## API

不需要 API 设计。

## 第三方依赖

不引入第三方依赖。

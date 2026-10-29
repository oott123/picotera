# 设计

## 范围

本需求只改 dashboard 前端。渠道列表已经通过 `listProviders()` 取得完整 `ProviderView[]`，每个渠道的模型来自 `providerModels` 字段，因此筛选在 `dashboard/src/views/ProvidersView.vue` 内本地完成，不新增后端接口、OpenAPI 类型或 sqlc 查询。

## UI 组件

新增 `dashboard/src/ui/MultiColumnFilter.vue`，并从 `dashboard/src/ui/index.ts` 导出。组件外观沿用请求页 `ColumnFilter.vue` 的表头触发器样式、激活态、清除按钮、搜索输入和 `SelectMenu.vue` 的浮层行为。

组件支持字符串或数字选项：

- `modelValue: V[]`：当前选中的多个值。
- `options: ColumnFilterOption<V>[]`：候选项，复用 `ColumnFilterOption` 类型。
- `label: string`：表头显示名。
- `placeholder?: string`：搜索框占位文案。
- `allLabel?: string`：清空状态文案，默认「全部」。
- `align?: 'left' | 'right'`：浮层对齐，默认 `left`。
- `searchable?: boolean`：是否启用搜索，默认启用。
- `formatActive?: (values: V[]) => string`：自定义激活态摘要。

组件行为：

- 未选择任何值时显示 `label`，浮层顶部显示「全部」选项。
- 选择一个值后，触发器摘要显示该选项 label。
- 选择多个值后，触发器摘要默认显示「N 项」。
- 点击候选项切换选中状态，浮层保持打开，便于连续多选。
- 点击「全部」或触发器内清除按钮时发出空数组。
- 搜索规则与 `SelectMenu.vue` 一致，对 `label`、`hint`、`value` 做大小写不敏感子串匹配。
- 每个候选项右侧用选中标记展示当前状态，避免新增第三方组件或图标库。

## 渠道页筛选

`ProvidersView.vue` 增加本地筛选状态：

- `selectedModels: string[]`
- `modelMatchMode: 'or' | 'and'`

模型候选项从当前渠道列表派生：

- 对每个 `ProviderView` 调用现有 `modelNames(p)`。
- 汇总、去重并按名称排序。
- 生成 `ColumnFilterOption<string>[]`，`value` 与 `label` 都为模型名。

新增 `filteredProviders` 计算属性：

- 当 `selectedModels` 为空时返回原排序后的 `providers`。
- `modelMatchMode === 'or'` 时，渠道只要包含任一选中模型即匹配。
- `modelMatchMode === 'and'` 时，渠道必须包含全部选中模型才匹配。

表格渲染改用 `filteredProviders`。计数区域显示过滤后的数量；存在筛选时显示总数上下文，例如「3 / 12 个渠道」。无匹配结果时显示「暂无匹配渠道」，未筛选且列表为空时保留现有「暂无渠道，点击右上角按钮新增」。

“模型”表头替换为一个组合表头：

- 使用 `MultiColumnFilter` 作为主要表头筛选入口。
- 当 `selectedModels.length > 1` 时，在表头旁显示 `SegmentedControl`，选项为「或」「与」。
- 只有多选时展示匹配模式切换；单选时两种模式结果一致，不展示切换控件。

## 状态与数据一致性

筛选状态不写入 URL。渠道页现有筛选需求只作用于当前管理列表，且请求页当前只有 trace/project 同步 URL；这里保持本地状态，避免引入新的路由语义。

筛选不改变渠道排序、行操作、侧栏 key 或复制逻辑。`duplicateProvider()` 的重名检测继续基于完整 `providers` 列表，而不是 `filteredProviders`，确保筛选状态下复制名称仍全局不冲突。

## API

不新增或修改 API。

## 依赖

不引入第三方库。复用 Vue、现有 `SelectMenu.vue`、`SegmentedControl.vue`、`ColumnFilterOption` 类型和本地 UI primitives。

# 动态筛选栏设计

## 组件架构

新增一个通用 Vue 组件 `DynamicFilterBar`，封装“添加筛选”与“已添加筛选项显隐、清除”的交互。

### DynamicFilterBar

- **职责**：管理已激活筛选项的 key 列表，渲染添加按钮（`filter-plus`）和已添加项的 label + 清除按钮。
- **Props**：
  - `available: { key: string; label: string }[]` — 所有可添加的筛选项定义。
  - `modelValue: string[]` — 当前已激活（可见）的筛选项 key 列表。
- **Emits**：
  - `update:modelValue` — 已激活列表变更。
  - `remove(key: string)` — 某筛选项被用户通过 X 按钮移除，供父组件重置对应数据值。
- **Slots**：具名插槽，slot name = filter key。父组件在每个插槽中放置实际的筛选控件（Select、SegmentedControl、input 等）。
- **行为**：
  1. 根据 `modelValue` 渲染对应插槽。
  2. 每个已激活项的标题行显示 label 和 close 图标（`close`，11px），点击后触发 `update:modelValue`（移除该 key）和 `remove` 事件。
  3. 当存在未激活筛选项时，渲染 `filter-plus` 按钮；点击弹出 `SelectMenu` 下拉菜单，列出未激活项；选择后将其 key 追加到 `modelValue`。

### 图标

`dashboard/src/ui/icons/paths.ts` 新增 `filter-plus: IconFilterPlus` 映射。

### 子组件复用

- `SelectMenu`：用于“添加筛选”下拉菜单。
- `Icon`：现有的图标组件，新增 `filter-plus` 支持。

## 各视图适配

### OverviewView

顶部 `Controls bar` 区域从硬编码的 `<div class="flex flex-col gap-1">…</div>` 列表切换为 `DynamicFilterBar`。

- `available` 包含：时间范围、统计粒度、货币、密钥、请求模型、上游模型、渠道、项目。
- 新建 `visibleFilters: string[]` 状态，初始为空数组（所有筛选项默认隐藏）。
- `@remove` 事件处理中，将被移除筛选项的数据值重置为各自默认值（如 `range` → `'1d'`，`apiKeyId` → `0` 等）。
- 原有的 `overviewFilters` computed 逻辑保持不变，只需确保即使筛选项不可见，其默认值仍然参与查询。

### AdminOverviewView

与 OverviewView 结构相同，差异仅在 `available` 列表中“密钥”替换为“用户”。

### RequestsView

顶部左侧筛选区（原 `<div class="flex items-end gap-3 flex-wrap">`）切换为 `DynamicFilterBar`。

- `available` 包含：类型、时间范围、ID、标注。
- `@remove` 事件处理中重置 `filters.type='meta'`、`filters.requestId=''`、`filters.annotationKey=''`、`filters.annotationValue=''`、`filters.startAt=''`、`filters.endAt=''`。
- 右侧的“清除筛选”按钮、计数、刷新按钮保持原位不动。
- 已有的 `activeFilterCount` 和 `clearAllFilters` 函数不受 `visibleFilters` 影响：隐藏的筛选项如果值非默认，仍然计入 `activeFilterCount`；“清除筛选”仍然重置所有值（无论可见与否）。

## 样式约束

- 已添加筛选项容器保持 `flex flex-col gap-1 min-w-0`（与原 `Field` 组件一致）。
- Label 样式：`text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]`。
- X 按钮样式：与现有 `ColumnFilter` 的 clear 按钮保持一致（`inline-flex items-center p-0 bg-transparent border-0 cursor-pointer text-ink-faint hover:text-ink`）。
- 添加按钮样式：ghost 风格，`inline-flex items-center gap-1.5 px-2 py-1.5 bg-surface-0 border border-line rounded-md text-xs text-ink-muted hover:bg-surface-50 hover:text-ink hover:border-surface-300`。

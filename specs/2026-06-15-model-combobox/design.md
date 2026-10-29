# 设计

## 概览

新增本地 UI 原语 `src/ui/ComboBox.vue`，并在 `TestView.vue` 中用它替换现有的“模型”文本输入框。纯前端改动，无后端 / API / OpenAPI 变更——所需数据（渠道的 `providerModels`、系统全部 `ModelView`）均已可用。

不引入任何第三方库。组件复用项目既有依赖 `@floating-ui/vue`（定位下拉层，与 `ColumnFilter.vue` 一致），样式用 Tailwind v4 工具类直接书写，与 `Input.vue` / `Select.vue` / `ColumnFilter.vue` 保持一致。

## ComboBox 组件

文件：`dashboard/src/ui/ComboBox.vue`，经 `src/ui/index.ts` 导出。

### Props

- `modelValue: string` — 当前选中值（`v-model`）。值类型固定为 `string`（模型名）。
- `options: ComboBoxOption[]` — 候选项。`ComboBoxOption = { value: string; label?: string; hint?: string }`；`label` 缺省时显示 `value`。
- `allowCustom?: boolean`（默认 `false`）— 是否允许选中下拉列表中不存在的值。
- `placeholder?: string` — 收起态与输入态占位文案。
- `disabled?: boolean`
- `size?: 'sm' | 'md'`（默认 `md`）— 与 `Input` / `Select` 尺寸 token 对齐。

### Emits

- `update:modelValue: [string]`

### 交互与状态

组件有“收起”和“展开”两种视觉状态，由内部 `open: boolean` 驱动。

**收起态**：渲染成一个与 `Select` 外观一致的按钮——显示当前 `modelValue`（或 placeholder），右侧 chevron 图标。点击 / 聚焦进入展开态。

**展开态**：
- 原框体替换为一个 `<input type="text">`，自动聚焦；其值绑定到内部 `query`，初始为当前 `modelValue`，并全选文本以便直接覆盖输入。
- 下方通过 `Teleport` + `useFloating`（`bottom-start`、`offset(4)`、`flip`、`shift`、`size` 约束最大高度）渲染下拉列表。
- 列表项 = 过滤后的 `options`；过滤规则与 `ColumnFilter` 一致：按 `label`/`hint`/`value` 做大小写不敏感子串匹配。
- 当 `allowCustom` 为真且 `query` 去除首尾空白后非空、且不与任何 option 的 `value` 完全相等时，在列表顶部额外插入一个“自定义项”，其 `value` 即 `query` 文本，带视觉标记（如“使用 \"<文本>\"”）。点击即选中该自定义值。
- 选中任一项（含自定义项）：emit `update:modelValue`，关闭下拉，回到收起态。

### 键盘与失焦

- `ArrowDown` / `ArrowUp` 在列表（含自定义项）中移动高亮 `activeIndex`。
- `Enter`：选中当前高亮项；若无高亮项但 `allowCustom` 且 `query` 非空，则选中 `query`。
- `Esc`：关闭下拉，恢复为原 `modelValue`，不提交。
- 点击组件外部（`document` `mousedown` 捕获，参照 `ColumnFilter`）：关闭下拉。关闭时：`allowCustom` 为真则把当前 `query`（trim 后非空时）作为值提交；为假则丢弃 `query`，保留原 `modelValue`。

### 失败快速 / 不做宽松处理

自定义值仅做首尾空白判空，不做其它规范化（不改大小写、不猜测）。符合项目“严格校验、不宽松”的约定。

## TestView 接入

将 `TestView.vue` 中“模型”字段的 `<Input v-model="model">` 替换为 `<ComboBox>`，options 与 `allowCustom` 随测试模式切换：

- **短路测试（`mode === 'direct'`）**：
  - `options` = 所选渠道 `directProviderId` 对应 provider 的 `providerModels`，映射为 `{ value: entry.model, hint: entry.upstreamModelName }`（`upstreamModelName` 存在且不同于 `model` 时作为 hint）。
  - `allowCustom = true`。
  - provider 数据已由现有 `providersQuery` 提供，按 `directProviderId` 在内存中查找，无需新增请求。
- **网关测试（`mode === 'gateway'`）**：
  - `options` = 系统全部模型，来自新增的 `listModels()` 查询，映射为 `{ value: m.name }`（`disabled` 模型可加 hint 标注或过滤，默认保留并不特殊处理）。
  - `allowCustom = false`。

`model` ref 的读写语义不变（`fields` 仍取 `model.value.trim()`），下游 body 构造、路径变量 `model` 默认值等逻辑全部沿用。

新增数据获取：在 `src/api/client.ts` 中已存在 `listModels()`；`TestView.vue` 增加一个 `useQuery({ queryKey: queryKeys.models.all, queryFn: listModels })`，仅在网关测试需要时使用（始终拉取即可，开销可忽略；遵循现有 vue-query 约定）。

切换 `mode` 或 `directProviderId` 时，现有 watch 已重置部分状态；模型值是否需要清空——保持现状（不主动清空 `model`），与当前行为一致，避免在切模式时丢失用户已输入的模型名。

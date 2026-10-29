# 执行计划

## 1. 新增 ComboBox 组件

- 新建 `dashboard/src/ui/ComboBox.vue`：
  - 定义 `ComboBoxOption` 接口（`value: string; label?: string; hint?: string`）并 `export`。
  - Props：`modelValue`、`options`、`allowCustom`（默认 false）、`placeholder`、`disabled`、`size`（默认 md）。Emits：`update:modelValue`。
  - 内部状态：`open`、`query`、`activeIndex`，模板 ref：`triggerRef` / `inputRef` / `floatingRef`。
  - 用 `useFloating`（`bottom-start` + `offset/flip/shift/size`）定位 `Teleport` 到 body 的下拉层，参照 `ColumnFilter.vue`。
  - 收起态：渲染按钮，外观对齐 `Select.vue`（边框、圆角、hover/focus、`sizeClass`），显示 `modelValue` 或 placeholder + chevron 图标。
  - 展开态：渲染 `<input>` 自动聚焦并全选，绑定 `query`；下方列表渲染 `filtered` 选项，`allowCustom` 时按规则插入顶部自定义项。
  - 实现过滤、选中（`pick`）、键盘导航（上/下/Enter/Esc）、外部点击关闭、关闭时按 `allowCustom` 决定是否提交 `query`。
  - 复用 `Icon`（`chevron-down`、可选 `search`）。
- 在 `dashboard/src/ui/index.ts` 导出 `ComboBox` 与 `ComboBoxOption` 类型。

## 2. TestView 接入

- 编辑 `dashboard/src/views/TestView.vue`：
  - import `ComboBox`（及 `ComboBoxOption` 类型）；import `listModels`。
  - 新增 `modelsQuery = useQuery({ queryKey: queryKeys.models.all, queryFn: listModels })`，`models = computed(() => modelsQuery.data.value ?? [])`。
  - 新增 `selectedProvider = computed(() => providers.value.find(p => p.id === directProviderId.value))`。
  - 新增 `modelOptions = computed<ComboBoxOption[]>()`：direct 模式映射所选 provider 的 `providerModels`（`value: model`，`hint: upstreamModelName`）；gateway 模式映射 `models`（`value: name`）。
  - 新增 `modelAllowCustom = computed(() => mode.value === 'direct')`。
  - 模板：把“模型”`Field` 内的 `<Input v-model="model" mono .../>` 替换为
    `<ComboBox v-model="model" :options="modelOptions" :allow-custom="modelAllowCustom" placeholder="例如 claude-sonnet-4-6" />`。

## 3. 校验

- `pnpm --dir dashboard type-check` — 通过 vue-tsc。
- `pnpm --dir dashboard lint` — oxlint + eslint 通过。
- 手动核对：
  - 收起态外观与 Select 一致；点击展开为输入框 + 下拉。
  - 短路测试：选不同渠道后列表为该渠道模型；输入不存在的值出现自定义项并可选中。
  - 网关测试：列表为系统全部模型；输入只过滤，无自定义项。

## 范围说明

- 无后端 / sqlc / OpenAPI 改动，无需跑 `mise run openapi` 或 `generate-openapi`。
- 不新增第三方依赖。

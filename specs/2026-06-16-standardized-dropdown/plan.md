# 标准化下拉框执行计划

## 阶段一：抽出底层 `SelectMenu`

1. 在 `dashboard/src/ui/` 新建 `SelectMenu.vue`。
   - Props：`modelValue`、`options`、`searchable`、`placement`、`disabled`。
   - 内部使用 `@floating-ui/vue` 的 `useFloating`、`offset`、`flip`、`shift`、`autoUpdate`、`size`。
   - 维护 `open`、`query`、`activeIndex`。
   - 提供过滤后的 `filteredOptions` computed。
   - 处理全局 `mousedown` 与 `keydown` 事件，在 `onBeforeUnmount` 中清理。
   - 通过 `trigger` slot 暴露绑定对象 `{ open, isActive, toggle, show, close }`。
   - 内部渲染搜索框与选项列表区域；对外只暴露 `update:modelValue` 事件。
2. 从现有 `ColumnFilter.vue` 与 `ComboBox.vue` 中复制经过验证的键盘/浮层/过滤逻辑到 `SelectMenu.vue`，并剔除与各自外观强相关的代码。
3. 在 `dashboard/src/ui/index.ts` 导出 `SelectMenu` 与类型 `SelectOption`。

## 阶段二：重构应用层组件

### 2.1 重写 `Select.vue`

- 改为基于 `SelectMenu`。
- Props 调整为：
  - `modelValue?: string | number`
  - `options: SelectOption[]`
  - `placeholder?: string`
  - `disabled?: boolean`
  - `size?: 'sm' | 'md'`
  - `searchable?: boolean`（默认 `true`）
- 触发器使用类按钮样式，与当前 `Select.vue` 视觉一致。
- 不支持 `modelModifiers.number`；调用侧对数字值使用 `:options` 中已有的 `value: number`。
- 移除原生 `<select>` 与 `<option>` 支持。

### 2.2 重写 `ColumnFilter.vue`

- 改为基于 `SelectMenu`。
- 保留现有 props：`label`、`modelValue`、`options`、`emptyValue`、`allLabel`、`placeholder`、`align`、`searchable`、`formatActive`。
- 触发器保持表头内联按钮样式。
- 浮层内保留 “全部” 选项与搜索框。
- 确保 `:empty-value` 为 `0` 时仍能正确识别为未激活状态。

### 2.3 重写 `ComboBox.vue`

- 改为基于 `SelectMenu`。
- 保留 `allowCustom`、`placeholder`、`disabled`、`size`。
- 在 `SelectMenu` 外部维护输入框状态：展开时触发器区域变为 `<input>`。
- 当 `allowCustom` 为 true 且输入内容不在 options 中时，向列表顶部插入 “使用 \"xxx\"” 自定义项。
- 关闭时若输入非空且非选项，则提交为 `modelValue`。

## 阶段三：迁移所有 `Select` 调用

逐个检查并替换以下文件中的 `<Select>` 用法，将 `<option>` 子节点改为 `options` 数组：

1. `dashboard/src/views/SimulateView.vue`
   - `selectedPath`：`endpoints` 映射为 `{ value: e.path, label: e.path + (e.name ? ' — ' + e.name : '') }`。
   - `selectedFormat`：`UNIFIED_FORMATS` 已有 `value`/`label`，直接使用。
   - `apiKeyId`：`apiKeys` 映射为 `{ value: k.id, label: k.name + ' (#' + k.id + ')' + (k.disabled ? ' — 已禁用' : '') }`。
2. `dashboard/src/views/OverviewView.vue`
   - 货币、apiKeyId、model、upstreamModel、providerId、projectId 等全部改为 `:options`。
   - 注意保留 `size="sm"`。
3. `dashboard/src/views/TestView.vue`
   - directProviderId、directEndpointPath、gatewayApiKeyId、gatewayUnifiedFormat、gatewayEndpointPath。
4. `dashboard/src/components/PricingEditor.vue`
   - 检查当前 `<Select>` 用法并迁移。
5. `dashboard/src/components/ProviderEndpointsPanel.vue`
   - 三处 `<Select>` 调用。
6. `dashboard/src/components/EndpointForm.vue`
   - endpointType、credentialsResolver。
7. `dashboard/src/components/ProviderForm.vue`
   - modelsEndpointResolver。
8. `dashboard/src/components/MergeProjectForm.vue`
   - targetId（注意当前使用 `model-modifiers.number`，新 Select 通过数字 options 处理）。
9. `dashboard/src/components/PreferencesMenu.vue`
   - currencyValue。

迁移原则：
- 每个 `<Select>` 之前构建 options 数组（可在 `<script setup>` 中用 `computed` 或内联 `map`）。
- 移除 `model-modifiers` 的 `.number` 用法，改为数字 `value`。
- 移除 `required` 等直接透传到 `<select>` 的属性；如需校验由调用侧表单处理。
- 对于空值选项（如 “请选择”/“全部”），在 options 数组首项加入 `{ value: '', label: '...' }` 或 `{ value: 0, label: '...' }`。

## 阶段四：验证与清理

1. 运行 `pnpm --dir dashboard type-check`，修复 TS 类型错误。
2. 运行 `pnpm --dir dashboard lint`，修复 oxlint/eslint 问题。
3. 手动检查以下关键交互：
   - RequestsView 的四个 `ColumnFilter` 能正常过滤、搜索、清除。
   - SimulateView、OverviewView、EndpointForm 等处的 `Select` 能正常选择。
   - ComboBox 自定义输入行为保持不变。
4. 确认 `dashboard/src/ui/index.ts` 导出的类型和组件正确。
5. 确认无遗漏的 `<option>` 子节点用法：
   ```bash
   grep -R "<Select" dashboard/src --include='*.vue' -A 5
   ```

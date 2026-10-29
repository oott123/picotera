# 标准化下拉框设计

## 总体架构

将下拉框交互拆分为三层：

1. **底层 `SelectMenu`**：负责浮层、搜索过滤、键盘导航、选项渲染。它不决定触发器外观，只暴露状态与事件。
2. **应用层组件**：
   - `Select`：表单字段风格的单选下拉框。
   - `ColumnFilter`：表格头筛选按钮风格。
   - `ComboBox`：在 `SelectMenu` 之上增加允许自定义输入的能力。
3. **使用侧**：所有现有 `<Select>` 调用从 `<option>` 子节点迁移到 `:options` 数组。

## 组件职责

### `SelectMenu`

- 使用 `@floating-ui/vue` 定位浮层（已有依赖，不引入新库）。
- 接收 `options: SelectOption[]`、`modelValue`、`searchable`、`placement`、`disabled` 等 props。
- 内部维护 `open`、`query`、`activeIndex`。
- 根据 `query` 过滤 options。
- 处理键盘：Esc 关闭、↑/↓ 移动高亮、Enter 选中。
- 点击外部关闭。
- 通过 scoped slot 把触发器渲染交给上层：`trigger` slot 接收 `{ open, isActive, toggle, show, close }` 绑定。
- 内部渲染搜索框与选项列表区域。
- 事件：`update:modelValue`。

### `Select`

- 基于 `SelectMenu`，使用类按钮触发器（与当前 `Select.vue` 外观一致）。
- Props：`modelValue`、`options`、`placeholder`、`disabled`、`size`、`searchable`。
- 触发器显示当前选中 label 或 placeholder。
- 点击触发器展开浮层，浮层内显示搜索框（当 `searchable` 为 true）和选项列表。
- 不支持自定义值。

### `ColumnFilter`

- 基于 `SelectMenu`，触发器为表格头内的内联按钮（保留现有样式）。
- 支持 `emptyValue`、`allLabel`、`align` 等表格筛选专有属性。
- 触发器显示 `label` 及当前激活项标签。
- 浮层内搜索框 + “全部” 选项 + 选项列表。

### `ComboBox`

- 基于 `SelectMenu`，保留 `allowCustom` 行为。
- 触发器区域在展开时显示输入框，支持直接输入并提交未列出的值。
- 自定义值逻辑由 `ComboBox` 自己维护，底层只负责列表渲染与事件通知。

## 类型设计

```ts
export interface SelectOption<T extends string | number = string | number> {
  value: T
  label: string
  hint?: string
  disabled?: boolean
}
```

`SelectMenu` 使用 generic `V extends string | number` 以兼容数字 ID 与字符串路径。

## 样式

- 触发器与浮层复用现有 Tailwind 设计 token（`border-line`、`bg-surface-0`、`text-ink`、`accent` 等）。
- `Select` 保持当前 `Select.vue` 的表单高度与 padding（`size: sm | md`）。
- `ColumnFilter` 保持当前表头内联样式。
- `ComboBox` 保持当前输入框+按钮组合样式。

## 依赖

- 继续使用 `@floating-ui/vue` 处理浮层定位。
- 不引入新的第三方组件库。

# Select 组件键盘交互补全

## 原始需求

为 Select 组件增加完整的键盘交互：当焦点在过滤器时，按上或者下，可以在过滤结果中选择结果，再按回车即可提交。

## 澄清结论

- 现状：键盘逻辑（ArrowUp / ArrowDown / Enter）其实**已经在 `SelectMenu.vue` 中实现并生效**，但 active 高亮项的样式（`bg-surface-50`）与 hover 态完全相同，且当 active 项恰好是当前已选项时高亮被抑制，导致用户**看不出回车将选中哪一项**。
- 因此本需求的核心是：让键盘 active 项有清晰、与 hover/已选态可区分的视觉高亮；并顺带补齐"完整键盘交互"应有的细节。
- 范围：键盘逻辑集中在 `SelectMenu.vue`，统一在此处修复，使 `Select`、`ComboBox`、`ColumnFilter` 三个消费方一并受益。

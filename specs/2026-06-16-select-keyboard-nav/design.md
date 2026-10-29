# 设计

所有改动集中在 `dashboard/src/ui/SelectMenu.vue`，无需触碰 `Select.vue` / `ComboBox.vue` / `ColumnFilter.vue` / 后端 / OpenAPI。无第三方库引入。

## 1. active 高亮可见（核心）

当前选项按钮的 class 逻辑：

```
opt.value === modelValue ? 'bg-accent-faint text-accent-ink font-medium' : 'text-ink hover:bg-surface-50'
activeIndex === i && opt.value !== modelValue ? 'bg-surface-50' : ''
```

问题有两层：

1. **同属性双声明导致条件背景失效（根因）**：静态 `class` 里恒含 `bg-transparent`，`:class` 里又有条件 `bg-surface-50`，二者都生成 `background-color` 工具类、特异性相同，胜负只由 Tailwind 生成 CSS 的**源顺序**决定（与 class 属性书写顺序无关）。`bg-transparent` 作为内置工具类排在后面时会恒定覆盖条件背景，于是 active 高亮根本不显示。
2. **语义抑制**：`&& opt.value !== modelValue` 使 active 项恰为已选项时高亮被抑制；且 active 与 hover 同用 `bg-surface-50` 无法区分。

新方案——背景色由**单一条件表达式独占**（消除双声明冲突），并把"已选(selected)"与"键盘 active"拆成两个**正交**维度：

- **移除静态 `class` 中的 `bg-transparent`**：背景色不再有任何静态声明，全部由下面的条件表达式决定，transparent 作为 else 分支。
- 背景条件（单一表达式，按优先级）：`activeIndex === i`（无条件，不再排除已选项）→ `bg-surface-100 ring-1 ring-inset ring-accent`；否则 `opt.value === modelValue` → `bg-accent-faint`；否则 `bg-transparent`。`bg-surface-100` 比 hover 旧值 `surface-50` 更重，叠加 accent 内描边后与"仅已选"一眼可分。
- **selected** 仅保留正交的文本维度：`text-accent-ink font-medium` + 右侧圆点。
- 移除独立的 `hover:bg-surface-50`：`@mouseenter` 已把 `activeIndex` 同步为鼠标所指项，hover 项天然成为 active 项，由 active 样式统一接管，做到"指针所指 = 回车将选中"一致。

`disabled` 项保留 `disabled:opacity-50 disabled:cursor-not-allowed`，且不会成为 active（见第 3 点）。

## 2. active 项滚动进可视区

`activeIndex` 变化时，让对应选项滚入滚动容器可视区，避免长列表中高亮滚出视野。

- 给每个选项按钮加 `:data-index="i"`；`watch(activeIndex)`（`flush: 'post'`）中通过 `floatingRef.value?.querySelector('[data-index="…"]')?.scrollIntoView({ block: 'nearest' })` 滚动。
- 用 `block: 'nearest'` 避免不必要的跳动。

## 3. 箭头跳过 disabled 项

新增 `nextEnabledIndex(from, dir)`：从 `from` 起按 `dir`（±1）在 `filteredOptions` 中寻找首个非 `disabled` 项，越界则停在边界（不回绕）。`ArrowDown` / `ArrowUp` 改用它推进 `activeIndex`。

## 4. 打开时 active 落在已选项

`show()` 与 query 重置时，`activeIndex` 初始化为：当前 `modelValue` 在 `filteredOptions` 中的下标；不存在则取首个非 disabled 项；无可选项则 `-1`。抽出 `initialActiveIndex()` 复用于 `show()` 和 query 的 `watch`。

## 5. 无障碍

- 列表容器 `role="listbox"` 已有；为输入框/触发器关联 `aria-activedescendant` 指向当前 active 选项的 id（选项加稳定 `:id`），active 选项 `aria-selected` 仍表示"已选"语义不变，active 仅作视觉与 activedescendant 指示。

## 不在本次范围

- `ColumnFilter` 的"全部"项位于 `#header` slot、不属于 `filteredOptions`，键盘无法到达——本次不改其结构。

# 执行计划

全部改动在 `dashboard/src/ui/SelectMenu.vue`。

## 步骤 1：active 视觉高亮（核心）

根因是静态 `class` 的 `bg-transparent` 与条件 `bg-surface-50` 同属性双声明、源顺序覆盖。修复要点：背景色全部交给单一条件表达式，静态类不再声明背景。

- **从静态 `class` 中删除 `bg-transparent`**（保留 `border-0 text-left text-sm cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed` 等其余类）。
- 同时删除旧 `:class` 里的 `text-ink hover:bg-surface-50` 与 `activeIndex === i && opt.value !== modelValue ? 'bg-surface-50' : ''` 两段。
- 新 `:class` 数组：
  - 文本维度：`opt.value === modelValue ? 'text-accent-ink font-medium' : 'text-ink'`
  - 背景维度（单一表达式，独占 `background-color`）：`activeIndex === i ? 'bg-surface-100 ring-1 ring-inset ring-accent' : (opt.value === modelValue ? 'bg-accent-faint' : 'bg-transparent')`

保留 `@mouseenter="activeIndex = i"`、右侧已选圆点、`disabled:*` 类不变。

## 步骤 2：跳过 disabled 的箭头导航

新增：

```ts
function nextEnabledIndex(from: number, dir: 1 | -1): number {
  const opts = filteredOptions.value
  let i = from
  while (true) {
    const n = i + dir
    if (n < 0 || n >= opts.length) return from  // 边界不回绕
    if (!opts[n].disabled) return n
    i = n
  }
}
```

`onKeydown` 中 `ArrowDown` → `activeIndex.value = nextEnabledIndex(activeIndex.value, 1)`，`ArrowUp` → `nextEnabledIndex(activeIndex.value, -1)`。`Enter` 逻辑不变（已判 `!opt.disabled`）。

## 步骤 3：打开时 active 落在已选项

新增：

```ts
function initialActiveIndex(): number {
  const opts = filteredOptions.value
  const sel = opts.findIndex((o) => o.value === props.modelValue && !o.disabled)
  if (sel >= 0) return sel
  return opts.findIndex((o) => !o.disabled)  // 无则首个可用，全不可用返回 -1
}
```

`show()` 与 `watch(query)` 中的 `activeIndex` 赋值改为调用 `initialActiveIndex()`。

## 步骤 4：active 滚动进可视区

- 选项按钮加 `:data-index="i"`。
- 新增 watch：

```ts
watch(activeIndex, (i) => {
  if (i < 0) return
  nextTick(() => {
    floatingRef.value
      ?.querySelector(`[data-index="${i}"]`)
      ?.scrollIntoView({ block: 'nearest' })
  })
})
```

## 步骤 5：aria-activedescendant

- 选项按钮加稳定 id：`:id="`${listboxId}-opt-${i}`"`，`listboxId` 用 Vue `useId()` 生成。
- 列表容器 `role="listbox"` 加 `:aria-activedescendant`（active 时指向对应 id，否则 undefined）。
- searchable 输入框加 `role="combobox"` 与同一 `:aria-activedescendant`。

## 步骤 6：验证

```bash
pnpm --dir dashboard type-check
pnpm --dir dashboard lint
```

手动验证（`mise run web`）：
- Select（searchable）：聚焦过滤框，↑/↓ 高亮明显移动且自动滚动可见，Enter 提交高亮项。
- 含 disabled 选项时 ↑/↓ 跳过、不停留。
- 打开已有选中值的下拉：高亮初始落在已选项。
- ComboBox / ColumnFilter：同样行为，高亮清晰。

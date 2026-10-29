# 执行计划

路径均相对 `dashboard/src/components/json-viewer/`。

1. 新建 `jsonSummary.ts`，按 design.md 实现：导出 `TagKey`、`SummaryTag`、`ObjectSummary`（含 `tags: SummaryTag[]`、`args: ToolToken[] | null`）、`TAG_MAX_CHARS`、
   `NAME_LIMIT`、`TEXT_LIMIT`、`objectSummary(record)`；模块内部定义 `TEXT_KEYS`、`ARRAY_SCAN`、`TEXT_DEPTH`、
   `ARG_PATHS`、`ARG_TEXT_PATHS`、`ARGS_PARSE_LIMIT`、`ARGS_SCAN`，以及 `nameOf`、`argsOf`、`argTokens`（基于
   `@/lib/toolFormat` 的 `toolCallTokens`）、`blockOf` / `innerOf` / `valueBlock`、片段截断函数（从第一个非空白字符起
   有界切片，再压缩空白，再按 `TEXT_LIMIT` 截断，不拆代理对，截断时加 `…`）。只 import `@/lib/toolFormat`。
2. 修改 `jsonTree.ts`：
   1. import `objectSummary` 与 `ObjectSummary`。
   2. `JsonRow` 新增 `summary: ObjectSummary | null`，注释说明只有折叠的非空对象才有。
   3. `flatten` 推入行时：`kind === 'object' && expandable && !expandedHere` 时
      `summary = objectSummary(frame.value as Record<string, unknown>)`，否则 `null`。
3. 修改 `JsonViewer.vue` 模板：容器分支 `{{ row.preview }}` 之后，`row.summary` 非空时按 design.md「渲染」依次渲染
   tags（`tagClass`：role 用 `text-ink`，其余 `text-ink-muted`）、name、args（`ARG_TOKEN_CLASS` 分色）、text。软换行模式下，容器行外层的 inline run 用 `min-w-0 flex-1 truncate` 代替
   `break-all whitespace-pre-wrap`，超出一行就截断；外层 span 的颜色由 `containerTailClass(row)` 取最后一段的颜色，让省略号和被截断的文字同色。
4. 在 `dashboard/` 下运行 `pnpm format`、`pnpm lint`、`pnpm type-check`，修掉报错。
5. 浏览器验证由用户完成，不在执行计划内。

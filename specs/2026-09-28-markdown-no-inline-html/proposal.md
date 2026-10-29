# 渲染 markdown 时不允许内联 HTML

在渲染 markdown 的时候（包括对话页面、渲染页面等），应该不允许内联 html，现在有时候遇到 html 的话，会变成标签，这样不好

## 补充

- markdown 渲染库从 marked 换成 TanStack Markdown（https://tanstack.com/markdown/latest/docs/overview），不再用 marked。
- 接受 TanStack Markdown 相对 marked 的语法退化：裸 URL / `<https://...>` 不自动变链接、HTML 实体按原文显示、行尾双空格硬换行 / 缩进代码块 / setext 标题不支持。
- DOMPurify 不再需要，一并移除。

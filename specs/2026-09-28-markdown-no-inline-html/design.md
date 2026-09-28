# 设计：换用 TanStack Markdown，原始 HTML 一律按文本显示

## 现状

Dashboard 的 markdown 渲染只有一个入口：`dashboard/src/composables/useSSEParser.ts` 的 `renderMarkdown(text)`，
实现是 `marked.parse` 之后再过一遍 `DOMPurify.sanitize`。调用方都通过 `v-html` 插入结果：

- `ConversationView.vue`：对话页面的 text / thinking 块
- `ResponseArtifactView.vue`：响应的「渲染」视图（reply / thinking）
- `SearchResultsView.vue`：搜索结果正文
- `TestView.vue`：网关测试页的回复

marked 默认把原始 HTML 原样输出，DOMPurify 只删掉危险部分，`<b>`、`<div>`、`<details>` 等标签会被浏览器当成元素渲染。

## 方案

`renderMarkdown` 改为调用 `@tanstack/markdown/html` 的 `renderHtml`，移除 `marked` 与 `dompurify`（含 `@types/dompurify`）依赖。
四个调用方的签名和用法不变。

```ts
import { renderHtml } from '@tanstack/markdown/html'

export function renderMarkdown(text: string): string {
  return renderHtml(text, { frontmatter: false, headingIds: false })
}
```

选项：

- `allowHtml` 保持默认的关闭：块级 HTML 和行内标签都按文本转义输出，HTML 标签所在行里的 markdown 照常渲染。
- `frontmatter: false`：库默认会把开头的 `---` 块解析成元数据，模型回复以 `---` 开头时正文会被吞掉。
- `headingIds: false`：对话页同一页渲染多条消息，自动生成的标题 id 会重复，这里也用不到标题锚点。

不使用 `streamingMarkdownExtension`、`docsMarkdownExtensions`、`highlighter`。

## 为什么去掉 DOMPurify

`allowHtml` 关闭且不启用任何扩展、高亮器时，`renderHtml` 的输出只包含库自己生成的元素：文本、属性、代码、
链接 title、图片 alt 全部转义，链接和图片的 URL 只保留相对地址、锚点、http(s)、mailto、tel，
`javascript:`、`vbscript:`、`file:`、`data:` 等协议会被清空。`v-html` 需要防的内容在这一层已经处理掉了，
不再套一层 sanitizer。以后若要启用 `allowHtml`、扩展或高亮器，需要重新评估这一点。

## 与 marked 的行为差异（接受）

TanStack Markdown 只支持文档中列出的语法子集，相对 marked 的可见变化：

- 裸 URL、邮箱、`<https://...>` 不会变成链接；尖括号形式按原文显示。
- HTML 实体不解码，`&amp;`、`&nbsp;` 按原文显示。
- 行尾双空格的硬换行不生效（反斜杠换行生效）。
- 缩进代码块按普通段落显示，`===` / `---` 下划线式标题不识别。
- markdown 图片里的 `data:` URL 被清空，渲染为空 `src` 的 `<img>`。
  dashboard 中结构化的图片内容走 `composables/images.ts`，不受影响。

同时获得的改进：中文标点紧邻的加粗（如 `**“引号”**后面`）能正确渲染；脚注可用。

代码块输出为 `<pre class="tm-code" data-lang="…"><code class="language-…">`，现有 `.prose` 样式只依赖 `pre` / `code` 元素，
表格对齐从 `align` 属性变为 `style="text-align:…"`，两者对现有样式没有影响。

## 依赖

- `@tanstack/markdown`：MIT，无运行时依赖，`react` / `octane` 为可选 peer 依赖，按 `0.0.15` 精确锁定版本
  （0.0.x 阶段每个版本都可能有破坏性变更，升级时手动评估）。
- 依赖变化会改动 `pnpm-lock.yaml`，根目录 `flake.nix` 中 `pnpmDeps.hash` 需要随之更新。

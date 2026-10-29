# 设计

## 目标

在「对话」Tab 中，把 search alpha 搜索 endpoint 的响应体 `{"output": "<string>"}` 渲染成一组带来源链接的搜索结果卡片。

## 集成点：conversation 渲染管线

复用现有的规范化对话管线（`src/composables/conversation.ts` → `ConversationView.vue`），不动 `ConversationArtifactView.vue`。当响应体不是已知的聚合格式时，它已经会走 `parseJsonBody → parseResponseConversation(parsed.value)`，因此只要让 `detectFormat` / `parseResponseConversation` 认识这个新格式即可。

理由：conversation 管线是唯一的结构化消息渲染层，已内建 Markdown 渲染（`renderMarkdown`）、折叠/展开、角色气泡与「无法解析」兜底。新增一个 `ConversationPart` 类型即可复用全部基础设施。

## 新增数据模型（`conversation.ts`）

### 格式枚举

`ConversationFormat` 增加 `'openaiSearch'`。

### 检测（`detectFormat`，response 分支）

在 response 分支最前面增加：

```
if (typeof root.output === 'string') return 'openaiSearch'
```

判据严格且无歧义：其它已知格式的 `output` 要么不存在，要么是**数组**（OpenAI Responses `output[]`）；只有 search alpha 的 `output` 是**字符串**。放在最前面，先于 `Array.isArray(root.output)` 的 Responses 判定，二者互斥（数组 vs 字符串），不会误判。

request 分支不改（该 endpoint 的请求体不是对话格式，无需渲染，也不会带字符串 `output`）。

### 新的 ConversationPart 类型

```ts
export interface SearchResult {
  citation: string          // 徽章 "Cite: turn0search15"
  title: string
  url: string | null
  wordlim: string | null    // 徽章
  published: string | null  // 徽章
  crawled: string | null    // 徽章
  content: string           // Markdown 正文
}

ConversationPart 增加：
  | { kind: 'searchResults'; results: SearchResult[] }
```

## 解析器（`conversation.ts` 新增纯函数）

### 引用标记的真实格式（切分锚点）

引用标记不是纯文本 `citeturn0search15`，而是用私有区 Unicode 定界符包裹：`U+E200` `cite` `U+E202` `turn0search15` `U+E201`（一条引用可含多个 `U+E202turn…` 段）。源码里用 `` / `` / `` 转义书写，避免不可见字符。

**结果之间不能用横线切分**：真实数据里 18 条结果只有 12 条横线分隔符，且横线常直接粘在上一条正文末尾（如 `CallbackResources----…----`）不独立成行。可靠的锚点是每条结果的引用标记——每条结果恰有一个，其**上一行就是该结果的标题行**（`<title> (<url>)`），标记之后是 ` [wordlim: …] Published/Crawled…; 正文`。

### `parseSearchOutput(output: string): SearchResult[]`

1. `const SEARCH_CITE = /cite([\s\S]*?)/g`；`markers = [...output.matchAll(SEARCH_CITE)]`。
2. 对每个标记 `k`：
   - **citation 徽章值**：捕获组按 `` 拆分、trim、过滤空、以 `, ` 连接（多来源引用 → `turn0search1, turn0search3`）。
   - **标题**：`searchTitleBefore(output, marker.index)` —— 取标记所在行的**上一行**文本。再用 `/^(.*?)\s*\((https?:\/\/[^\s)]+)\)\s*$/` 从标题行尾部拆出 `url`；命中则 `title` = 括号前文本、`url` = 链接，否则 `title` = 整行、`url = null`。
   - **正文区间**：从本标记结尾到**下一条结果标题行的行首**（末条到 output 结尾）。`searchTitleBefore` 顺带返回该行首偏移，用来界定区间上界，从而排除下一条的标题行。
   - 在区间文本上提取徽章：`wordlim` = `/\[wordlim:\s*([^\]]+)\]/`、`published` = `/Published:\s*([^;]+);/`、`crawled` = `/Crawled:\s*([^;]+);/`。
   - **正文** = 区间文本删除上述三个已提取片段后，用 `stripTrailingSeparators` 去掉尾部粘连的 `-{20,}` 分隔符并 trim。
3. 过滤掉标题、正文均为空的结果。

`stripTrailingSeparators(text)` = `text.replace(/\s*-{20,}\s*$/,'').replace(/^\s+|\s+$/g,'')`——按「尾部连续 ≥20 横线」剥离（覆盖横线粘在词尾与独立成行两种情况），阈值 20 避开正文 Markdown 的 `---` hr。

徽章/URL 提取按「存在即取、缺失则该徽章不显示」，是对固定展示格式的解析（非用户输入校验），缺字段跳过；无 URL 的标题以纯文本呈现。私有区定界符全部落在被消费的标记里，不进入正文。

### `parseSearchResponse(json): ConversationMessage[]`

```
const output = asRecord(json)?.output
if (typeof output !== 'string') return []
const results = parseSearchOutput(output)
return results.length
  ? [{ role: 'assistant', parts: [{ kind: 'searchResults', results }] }]
  : []
```

角色用 `assistant`（该 endpoint 的响应）。`parseResponseConversation` 的 switch 增加 `case 'openaiSearch': return parseSearchResponse(json)`。另导出 `extractSearchResults(json): SearchResult[]`（从 `root.output` 字符串解析出结果数组），供「渲染」子视图直接调用；`parseSearchResponse` 复用它。

## 渲染

来源卡片抽成独立组件 `SearchResultsView.vue`（props `results: SearchResult[]`），两个入口共用，避免重复。每张卡片：

- **标题行**：`url` 存在时为 `<a :href="url" target="_blank" rel="noopener noreferrer">`（accent 色、hover 下划线）；否则纯文本标题。URL 存在时在标题下用 `text-2xs text-ink-muted` 显示 host（`new URL(url).host`，try/catch 兜底）。
- **徽章行**：`citation` 用 `Tag`（`accent` variant）渲染为 `Cite: turn0search15`；`published` / `crawled` / `wordlim` 各存在时用 `Tag`（`muted` variant）渲染。
- **正文**：`<div class="prose prose-sm max-w-none text-ink" v-html="renderMarkdown(content)">`。
- 卡片用 `rounded-md border border-line-soft bg-surface-0 p-2.5`，列间距 `gap-2`。

两个接入点：

1. **「对话」Tab**（`ConversationView.vue`）：part 类型 switch 增加 `v-else-if="part.kind === 'searchResults'"` → `<SearchResultsView :results="part.results" />`。
2. **「原始响应 → 渲染」子视图**（`ResponseArtifactView.vue`）：新增 `searchResults` computed（`jsonBody.ok ? extractSearchResults(jsonBody.value) : []`），在 rendered 分支渲染 `<SearchResultsView v-if="searchResults.length" :results="searchResults" />`；「无可渲染内容」空态条件补上 `&& !searchResults.length`（同时改为显式否定 `!content.thinking && !content.reply && !openAIImageGeneration && !searchResults.length`）。

## 不涉及的部分

- 后端、`pkg/` 任意代码、`openapi.yaml`、TS 类型生成：均不改动。
- `ConversationArtifactView.vue`：无需改动，已有 JSON 兜底路径会调用更新后的 `parseResponseConversation`。

## 第三方库

无新增。Markdown 用现有 `marked` + `dompurify`（`renderMarkdown`），徽章用现有 `Tag` 基础组件。

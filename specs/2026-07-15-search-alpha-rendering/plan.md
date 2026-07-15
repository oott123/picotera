# 执行计划

全部改动在 `dashboard/` 前端，共 4 个文件（1 个新建）。

## 1. `dashboard/src/composables/conversation.ts`

1. 导出 `SearchResult` 接口（`citation` / `title` / `url` / `wordlim` / `published` / `crawled` / `content`）。
2. `ConversationPart` 联合类型增加 `| { kind: 'searchResults'; results: SearchResult[] }`。
3. `ConversationFormat` 联合类型增加 `'openaiSearch'`。
4. `detectFormat` 的 response 分支最前面增加 `if (typeof root.output === 'string') return 'openaiSearch'`。
5. 新增 `parseSearchOutput(output)`：按引用标记（`cite…`）`matchAll` 切分，取标记上一行为标题、拆 URL，提取 Cite/wordlim/Published/Crawled 徽章，正文用 `stripTrailingSeparators` 去尾部粘连横线（按 design.md 算法）。
6. 新增导出 `extractSearchResults(json): SearchResult[]`（从 `root.output` 字符串解析）；`parseSearchResponse(json): ConversationMessage[]` 复用它，返回单条 `assistant` 消息含一个 `searchResults` part。
7. `parseRequestConversation` / `parseResponseConversation` 的 switch 各补 `openaiSearch` 分支（request 返回 `[]`，response 返回 `parseSearchResponse`）。

## 2. `dashboard/src/components/SearchResultsView.vue`（新建）

来源卡片列表组件，props `results: SearchResult[]`。标题链接 + host（`new URL().host` try/catch）、Cite/Published/Crawled/wordlim 徽章（`Tag`）、`renderMarkdown` 正文。两个入口共用，避免重复。

## 3. `dashboard/src/components/ConversationView.vue`

part 类型 switch 增加 `v-else-if="part.kind === 'searchResults'"` → `<SearchResultsView :results="part.results" />`；引入组件；移除内联卡片标记与 `hostOf`。

## 4. `dashboard/src/components/ResponseArtifactView.vue`

引入 `extractSearchResults` 与 `SearchResultsView`；新增 `searchResults` computed（`jsonBody.ok ? extractSearchResults(jsonBody.value) : []`）；rendered 分支渲染 `<SearchResultsView v-if="searchResults.length">`；空态条件改为 `!content.thinking && !content.reply && !openAIImageGeneration && !searchResults.length`。

## 5. 验证

- `pnpm --dir dashboard type-check` 通过；改动/新建文件 `eslint` 干净（exit 0）。
- 用真实 artifact 的 `output`（18 条）离线验证解析：18 条全部正确切分、标题/URL/四类徽章无误、尾部横线与私有区字符干净剥离。
- 手动：打开该搜索请求详情，确认「对话」Tab 与「原始响应 → 渲染」子视图均渲染出来源卡片；其它已有格式（Chat / Responses / Anthropic / Gemini）不受影响。

## 影响面

- 不改后端、`openapi.yaml`、TS 类型生成、数据层。
- `detectFormat` 新增判据（字符串 `output`）与既有格式（数组 `output`）互斥，无回归风险。
- 既有的 `pnpm lint` 会因无关文件 `src/ui/TimeRangeFilter.vue`（既有未使用 import，提交 `57e1a48`）报错，非本次改动引入。

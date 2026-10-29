# Plan: DeepSeek 搜索端点与搜索模拟支持

设计见 `design.md`。按顺序执行，每步完成后 `go build ./...` 通过再进入下一步。

## Step 1 — 端点类型 `deepseekSearch`

1. `pkg/contract/endpoint.go`：
   - 新增常量 `EndpointType_DeepSeekSearch int32 = 15`，放在 `EndpointType_Codex` 之后。
   - `ToEndpointType` 增加 `case "deepseekSearch"`，`FromEndpointType` 增加 `case EndpointType_DeepSeekSearch: return "deepseekSearch"`。
   - `EndpointView.EndpointType` 的 `enum` tag 在 `exaSearch` 之后插入 `deepseekSearch`。
2. `pkg/contract/label.go`：`EndpointType` 字段 `enum` tag 同样插入 `deepseekSearch`。
3. `pkg/server/handle_endpoint.go` `handleUpsertEndpoint`：在 `exaSearch` 校验之后新增 `deepseekSearch` 的空 `modelPath` 校验，返回 `huma.Error400BadRequest("deepseekSearch endpoint must have empty modelPath")`。
4. `pkg/server/user_message_preview.go`：`case contract.EndpointType_ExaSearch:` 改为 `case contract.EndpointType_ExaSearch, contract.EndpointType_DeepSeekSearch:`。
5. `pkg/server/user_message_preview_test.go`：在 `TestExtractUserMessagePreviewSupportedFormats` 中加一条 `deepseekSearch` 用例（`{"query":"...","count":5}` → 预览为 query）。

## Step 2 — 搜索后端描述与选择

`pkg/server/web_search.go`：

1. 新增 `webSearchBackend`、`webSearchHit` 两个类型（字段见 design）。
2. `webSearchContext` 新增字段 `backend webSearchBackend`。
3. 新增 `pickWebSearchBackend(rows []db.GetProvidersByEndpointTypesRow) (webSearchBackend, bool)`：过滤无效行 → `slices.SortFunc`（`compareCandidateOrder(a.ProviderID, 0, a.ProviderPriority, b.ProviderID, 0, b.ProviderPriority)`，为 0 时 `strings.Compare(a.EndpointPath, b.EndpointPath)`）→ 取首行。
4. 新增 `resolveWebSearchBackend(ctx context.Context, q *db.Queries) (webSearchBackend, error)`：调用 `q.GetProvidersByEndpointTypes(ctx, []int32{contract.EndpointType_ExaSearch, contract.EndpointType_DeepSeekSearch})`，查询出错时包装返回；`pickWebSearchBackend` 返回 false 时返回 `errors.New("web search emulation: no exaSearch or deepseekSearch provider available")`。
5. 新文件 `pkg/server/web_search_backend_test.go`，测试 `pickWebSearchBackend`：
   - 空输入 → false；
   - 只有无效行（缺 `UpstreamUrl` 或 `ProviderCredentials`）→ false；
   - Exa 渠道优先级高于 DeepSeek 渠道 → 选 Exa，反之选 DeepSeek；
   - 同优先级 → provider id 大者胜；
   - 同一 provider 绑定两种搜索端点 → `EndpointPath` 字典序小者胜。

## Step 3 — 按后端生成工具 schema

`pkg/server/web_search.go` / `pkg/server/web_search_loop.go`：

1. `webSearchFunctionToolJSON` 更名为 `exaWebSearchToolJSON`；新增 `deepseekWebSearchToolJSON`（内容见 design）。
2. 新增 `func (b webSearchBackend) toolJSON() json.RawMessage`：按 `endpointType` 返回两者之一；其他值不会出现（`pickWebSearchBackend` 只产出这两种），`default` 分支 `panic` 并带上类型值。
3. `rewriteWebSearchTools(body []byte, backend webSearchBackend)`：替换时写入 `backend.toolJSON()`。
4. `buildWebSearchSubBody(originalBody []byte, accumulatedContent []json.RawMessage, backend webSearchBackend)`：把 `backend` 透传给 `rewriteWebSearchTools`。
5. 调用点：`loopWebSearchNonStream` 与 `web_search_stream_loop.go` 中的 `buildWebSearchSubBody` 调用传 `wsCtx.backend` / `d.wsCtx.backend`。
6. `pkg/server/web_search_loop_test.go`：现有两个测试改为传入 Exa backend；新增一条 DeepSeek backend 用例，断言 `tools[0].input_schema.properties` 含 `query` / `count` / `ori_question` 且不含 `numResults`。

## Step 4 — 接入 attempt 准备阶段

`pkg/server/gateway_flow_attempts.go`：

1. `prepareUnifiedWebSearch` 签名改为 `(ctx context.Context, f *gatewayFlow, input attemptInput, reqBody []byte)`；`prepareUnifiedAttempt` 传入其 `ctx`。
2. 触发条件判断之后、改写之前调用 `resolveWebSearchBackend(ctx, f.h.queries)`，出错直接返回；成功时 `rewriteWebSearchTools(reqBody, backend)`，并在返回的 `webSearchContext` 上设置 `backend`。

## Step 5 — 后端无关的搜索调用

`pkg/server/web_search.go`：

1. 新增 `DeepSeekSearchRequest`、`DeepSeekSearchResponse`、`DeepSeekResult` 类型（字段与 JSON tag 见 design）。
2. 新增 `webSearchToolError` 类型（实现 `error`）与 `webSearchErrorCode(err error) string`。
3. 拆出两组按后端的纯函数：
   - `buildExaSearchBody(toolInput json.RawMessage) ([]byte, error)` / `decodeExaSearchHits(body []byte) ([]webSearchHit, error)`：沿用现有 `callExa` 中的解码、`query` 非空校验、`contents.highlights = true`，hits 的 `Text` 为 highlights 以 `"\n\n"` 拼接。
   - `buildDeepSeekSearchBody(toolInput json.RawMessage) ([]byte, error)` / `decodeDeepSeekSearchHits(body []byte) ([]webSearchHit, error)`：`query` 为空返回普通错误；`utf8.RuneCountInString(query) > 128` 返回 `&webSearchToolError{code: "query_too_long"}`；hits 的 `Text` 为 `summary`，`PublishedDate` 为 `date`。
4. `callExa` 替换为 `callWebSearch(ctx, toolInput, wsCtx) ([]webSearchHit, error)`：按 `wsCtx.backend.endpointType` 选择上述两组函数，请求发往 `wsCtx.backend.endpointPath`，删除对 `GetFirstEndpointByType` 的调用；`httptest` 请求构造、headers、非 200 处理沿用原实现，错误信息前缀改为 `callWebSearch:`。
5. `buildWebSearchToolResult(toolUseID string, hits []webSearchHit)`：改为遍历 hits。
6. 调用点：
   - `transformWebSearchResponse`：`h.callWebSearch(...)`，出错时 `buildWebSearchToolResultError(serverToolUseID, webSearchErrorCode(err))`。
   - `web_search_stream.go` `flushBufferedWebSearch`：同上。
7. `GetFirstEndpointByType` 不再有调用方：从 `db/queries/endpoint.sql` 删除该查询并运行 `sqlc generate`。
8. 在 `web_search_backend_test.go` 中追加测试：
   - `buildDeepSeekSearchBody`：完整输入 → 输出只含 `query` / `count` / `ori_question`；省略可选字段时不输出对应 key；129 个汉字的 query → `webSearchErrorCode` 为 `query_too_long`；128 个汉字通过；空 query → 错误且错误码为 `unavailable`。
   - `decodeDeepSeekSearchHits`：按 proposal “参考”一节列出的全部字段手写一份响应，断言 `Text == summary`、`PublishedDate` 与 `date` 一致；`date: null` → `PublishedDate == ""`；`{"results": []}` → 空切片。
   - `buildExaSearchBody` / `decodeExaSearchHits`：断言与原 `callExa` 行为一致（`contents.highlights` 为 true、highlights 拼接）。
   - `buildWebSearchToolResult`：DeepSeek 的 RFC 3339 日期产出 `page_age`（如 `June 1, 2026`），空日期不带 `page_age`。

## Step 6 — 注释更新

按 design “注释”一节，把 `web_search.go`、`web_search_loop.go`、`web_search_stream.go`、`web_search_stream_loop.go` 中把 Exa 写成唯一后端的注释改为后端无关措辞。`web_search_stream_test.go` 中提到 `callExa` 的注释改为 `callWebSearch`。

## Step 7 — OpenAPI 与 Dashboard

1. `mise run openapi`，然后 `pnpm --dir dashboard generate-openapi`。
2. `dashboard/src/api/index.ts`：`ENDPOINT_TYPE_LABELS` 在 `exaSearch` 之后加 `deepseekSearch: 'DeepSeek 搜索'`。
3. `dashboard/src/components/EndpointForm.vue`：
   - `isModelPathLocked` 与 `watch` 中的类型判断都加入 `'deepseekSearch'`；
   - `modelPath` 占位符三元链在 `exaSearch` 分支后加 `form.endpointType === 'deepseekSearch' ? 'DeepSeek 搜索端点不解析模型'`。
4. `dashboard/src/lib/testBody.ts`：注释的类型列表补 `deepseekSearch`。
5. `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`、`pnpm --dir dashboard format`。

## Step 8 — 文档

`CLAUDE.md`：

1. `completion_endpoint_path` 段落中“endpoints outside it (model lists, `count_tokens`, Exa search, …)”补上 `DeepSeek search`。
2. 在 “Unified generation routes” 一节末尾（“Passthrough routes” 段之后）新增 “**Web search emulation.**” 段落，内容按 design 第 2 节：触发条件；`resolveWebSearchBackend` 按渠道优先级在 `exaSearch` / `deepseekSearch` 间选端点；后端决定 `web_search` function tool 的 schema、请求体与结果解析，存于 `webSearchContext.backend`，服务端循环各轮复用；无可用搜索渠道时非原生候选的 attempt 失败并继续下一个候选；DeepSeek 后端超过 128 字符的 query 产出 `query_too_long` 错误块。

## Step 9 — 验证

1. `go build ./...`、`go test ./pkg/server/... ./pkg/contract/...`。
2. 手动联调（`docker compose up -d` + `mise run server`）：
   - 建一个 `deepseekSearch` 端点（如 `/deepseek/v1/tools/search`）并绑定 DeepSeek provider，直接 POST 搜索请求，确认请求行的 preview 为 query。
   - 一个不支持原生 web search 的 Anthropic 渠道，经 `/api/unified/v1/messages` 带 `web_search_20250305` 工具请求（流式和非流式各一次）：只配 DeepSeek 时走 DeepSeek；同时配 Exa 时，调换两个搜索渠道的优先级，确认后端随之切换。
   - 删掉所有搜索渠道后重复请求：非原生候选的 upstream 行记录 `no exaSearch or deepseekSearch provider available`。

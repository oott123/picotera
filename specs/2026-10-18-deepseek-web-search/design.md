# Design: DeepSeek 搜索端点与搜索模拟支持

## 总览

两部分改动：

1. **新端点类型 `deepseekSearch`（= 15）**：DeepSeek Search 兼容格式的搜索端点，与 `exaSearch` 同样是 no-model 端点，在 path-based gateway 上没有专属运行时分支。
2. **搜索模拟多后端**：Anthropic web search 模拟（`pkg/server/web_search*.go`）从“写死 Exa”改为“Exa / DeepSeek 两种后端”。后端在每次 attempt 的 outbound 改写阶段按渠道优先级选定，选定结果决定暴露给 LLM 的 function tool schema、实际搜索请求的构造方式和响应解析方式，并在本次请求的整个服务端循环内保持不变。

## 1. 端点类型 `deepseekSearch`

### Contract

`pkg/contract/endpoint.go`：

- 新增 `EndpointType_DeepSeekSearch int32 = 15`（14 = `codex` 之后顺延；11/12 是退役占位，不复用）。
- `ToEndpointType("deepseekSearch")` → 15，`FromEndpointType(15)` → `"deepseekSearch"`。
- `EndpointView.EndpointType` 与 `pkg/contract/label.go` 的 `EndpointLabelView.EndpointType` 的 `enum` tag 都追加 `deepseekSearch`。

数据库 `endpoint.endpoint_type` 是整型列，无需迁移。

### 校验

`handleUpsertEndpoint`：`deepseekSearch` 的 `modelPath` 必须为空，否则 400 `deepseekSearch endpoint must have empty modelPath`（与 `exaSearch` 同一条规则）。

### 运行时

- 走现有 no-model 路径：`model_path = ""` → 跳过模型抽取，所有绑定的 provider 都是候选，请求/响应字节原样转发。
- `extractUserMessage`：`deepseekSearch` 与 `exaSearch` 共用 `extractQueryUserMessage`（两者请求体的 `query` 字段同名同义）。
- `responseAggregationFormat` 不加分支，落入 default（`FormatUnknown`）。
- `completion_endpoint_path` 视图是显式白名单，新类型默认不在其中，成功率 / 空回复统计不受影响，无需迁移。
- 凭证：DeepSeek 用 `Authorization: Bearer`，与 `generalApiKey` 默认值一致，表单不做特殊默认。

## 2. 搜索模拟多后端

### 后端描述

`pkg/server/web_search.go` 新增：

```go
// webSearchBackend is the search endpoint web search emulation calls for one
// request: which wire format to speak and which gateway path to send it to.
type webSearchBackend struct {
	endpointType int32  // contract.EndpointType_ExaSearch or EndpointType_DeepSeekSearch
	endpointPath string // endpoint.path the sub-call is routed to
}

// webSearchHit is one search result normalized across backends.
type webSearchHit struct {
	URL           string
	Title         string
	Text          string // plaintext stored in encrypted_content and fed back to the LLM
	PublishedDate string // raw date string; "" when the backend reported none
}
```

`webSearchContext` 新增字段 `backend webSearchBackend`。

### 后端选择（按渠道优先级）

```go
func pickWebSearchBackend(rows []db.GetProvidersByEndpointTypesRow) (webSearchBackend, bool)
func resolveWebSearchBackend(ctx context.Context, q *db.Queries) (webSearchBackend, error)
```

- `resolveWebSearchBackend` 用现有查询 `GetProvidersByEndpointTypes(ctx, []int32{EndpointType_ExaSearch, EndpointType_DeepSeekSearch})` 取所有未禁用、绑定了这两类端点的 provider 行（该查询已返回 `endpoint_type`、`endpoint_path`、`provider_priority`）。
- `pickWebSearchBackend` 是纯函数：
  1. 过滤掉 `UpstreamUrl == ""` 或 `ProviderCredentials == ""` 的行（与 `resolveProvidersByTypes` 的有效性过滤一致）。
  2. 排序：`compareCandidateOrder(a.ProviderID, 0, a.ProviderPriority, b.ProviderID, 0, b.ProviderPriority)`（provider 优先级降序，同优先级 provider id 大者在前，与网关候选排序同一规则）；同一 provider 绑定了多个搜索端点时再按 `EndpointPath` 字典序升序，保证结果确定。
  3. 取第一行的 `(EndpointType, EndpointPath)`；无行返回 `false`。
- 无可用后端时 `resolveWebSearchBackend` 返回错误 `web search emulation: no exaSearch or deepseekSearch provider available`。

选定的是“端点”，不是“provider”：搜索子请求打到该端点 path 后，仍由 path-based gateway 在该端点的所有 provider 间按优先级解析、重试，并运行 JS hooks（与现状一致）。

### 集成点

`prepareUnifiedWebSearch(ctx, f, input, reqBody)`（新增 `ctx` 参数，由 `prepareUnifiedAttempt` 传入）：触发条件不变（源格式为 Anthropic Messages、候选 provider 不支持原生 web search、请求含 web search 工具）。满足条件时先调用 `resolveWebSearchBackend(ctx, f.h.queries)`：

- 出错 → 直接返回该错误。它沿 `buildRewrittenUpstreamRequest` 的普通错误分支走：本次 attempt 记为失败（`FinishReasonInternal`），运行 `afterUpstreamError`，然后继续尝试下一个候选。支持原生 web search 的候选不经过这一步，照常服务。
- 成功 → `rewriteWebSearchTools(reqBody, backend)`、`rewriteWebSearchHistory`，并把 `backend` 存入 `webSearchContext`。

**行为变化**：此前没有 `exaSearch` 端点时请求照常发往上游，每次搜索都得到 `unavailable` 错误块。现在需要模拟却没有搜索渠道时，非原生候选的 attempt 直接失败。

服务端循环（`loopWebSearchNonStream`、`webSearchSSELoopDriver`）通过 `buildWebSearchSubBody(originalBody, accumulated, backend)` 构造下一轮请求体，`backend` 取自 `wsCtx.backend`，所以一次请求内各轮的工具 schema 和搜索后端保持一致。内层子请求的 tools 已是 function 形式，`hasWebSearchTool` 为 false，不会重新选择后端。

### 工具 schema

`rewriteWebSearchTools(body []byte, backend webSearchBackend)` 按 `backend.endpointType` 选用 function tool 定义，工具名统一为 `web_search`：

- `exaSearch`：现有 `webSearchFunctionToolJSON`，更名为 `exaWebSearchToolJSON`，内容不变。
- `deepseekSearch`：新增 `deepseekWebSearchToolJSON`：

```json
{
  "name": "web_search",
  "description": "Search the web for current information. Returns relevant summaries from web pages.",
  "input_schema": {
    "type": "object",
    "properties": {
      "query": {"type": "string", "description": "The search query, at most 128 characters. Supports operators: site:example.com (join several with OR), -site:example.com, -keyword, -\"phrase\", \"exact phrase\", lang:zh,en, after:2025-01-01, before:2025"},
      "count": {"type": "integer", "description": "Number of results to return (default 10, max 50)"},
      "ori_question": {"type": "string", "description": "The user's original question that prompted this search, at most 128 characters; helps the search engine understand intent"}
    },
    "required": ["query"]
  }
}
```

取值范围只写在 description 里，不用 `maxLength` / `minimum` 等关键字，和 Exa schema 保持同一风格，也避免经 llmbridge 转成 Gemini / OpenAI 格式时遇到关键字兼容问题。`need_content` 与 `localization` 不暴露：`need_content` 固定不发送（默认 false），喂给模型的正文用 `summary`。

历史消息转换（`rewriteWebSearchHistory`）不依赖后端：`server_tool_use.input` 原样转为 `tool_use.input`，`web_search_tool_result` 渲染为纯文本。如果历史轮次用的是另一种后端的参数，旧 `tool_use.input` 与当前 schema 字段不一致，上游不会按当前 schema 校验历史 tool_use 的 input，因此不需要处理。

### 搜索调用

`callExa` 替换为后端无关的：

```go
func (h *gatewayHandler) callWebSearch(ctx context.Context, toolInput json.RawMessage, wsCtx *webSearchContext) ([]webSearchHit, error)
```

公共部分不变：构造到 `wsCtx.backend.endpointPath` 的 `httptest` 请求，带原始 API key 的 `Authorization: Bearer`、`X-Session-Affinity`，经 `h.ServeHTTP` 走完整 path-based gateway（每次搜索仍记为独立 request 行）；非 200 返回错误。按后端分派的是请求体构造和响应解析两步：

| | Exa | DeepSeek |
|---|---|---|
| 工具输入解码 | `ExaSearchRequest`（现状） | `DeepSeekSearchRequest{Query, Count, OriQuestion}`，JSON 字段 `query` / `count,omitempty` / `ori_question,omitempty` |
| 本地校验 | `query == ""` → 错误 | `query == ""` → 错误；`utf8.RuneCountInString(query) > 128` → `query_too_long` 工具错误，不发请求 |
| 固定字段 | `contents.highlights = true` | 无（`need_content`、`localization` 不发送） |
| 响应结构 | `ExaSearchResponse`（现状） | `DeepSeekSearchResponse{Results []DeepSeekResult}`，`DeepSeekResult` 只解码 `url`、`title`、`summary`、`date`（`date` 为 null 时解码为 `""`） |
| `webSearchHit.Text` | highlights 以 `"\n\n"` 拼接 | `summary` |
| `webSearchHit.PublishedDate` | `publishedDate` | `date` |

DeepSeek 的 `count`、`ori_question` 由 DeepSeek 自己校验，越界时它返回 400，这里按普通非 200 错误处理。

### 工具错误码

新增：

```go
// webSearchToolError carries an Anthropic web_search_tool_result_error code
// for failures that have a more specific code than "unavailable".
type webSearchToolError struct{ code string }

func webSearchErrorCode(err error) string // errors.As → code；其余一律 "unavailable"
```

非流式（`transformWebSearchResponse`）和流式（`flushBufferedWebSearch`）都改为 `buildWebSearchToolResultError(id, webSearchErrorCode(err))`。目前只有 DeepSeek 的 `query_too_long` 会产生非 `unavailable` 的错误码，Exa 的错误映射不变。

### 结果块

`buildWebSearchToolResult(toolUseID string, hits []webSearchHit)`：每个 hit 映射为 `{type: "web_search_result", url, title, encrypted_content: Text}`，`PublishedDate` 经 `formatPageAge` 解析成功时附加 `page_age`。DeepSeek 的 `date` 是 RFC 3339（如 `2026-06-01T00:00:00+00:00`），`formatPageAge` 现有的 `time.RFC3339` layout 就能解析。

### 注释

`web_search*.go` 中把 Exa 写成唯一搜索后端的注释（`webSearchContext`、`rewriteWebSearchTools`、`rewriteWebSearchHistory`、`renderWebSearchResultsAsText`、`transformWebSearchResponse`、`buildServerToolUseFromToolUse`、`webSearchSSETransformer`、`formatPageAge` 等）改为后端无关的措辞；`ExaSearchRequest` 等 Exa 专属类型的注释保留。

## 3. Dashboard

- `pnpm --dir dashboard generate-openapi` 后 `EndpointType` 联合类型包含 `deepseekSearch`。
- `src/api/index.ts` `ENDPOINT_TYPE_LABELS`：`deepseekSearch: 'DeepSeek 搜索'`（放在 `exaSearch` 之后）。类型选择器由该表生成，自动出现新选项。
- `src/components/EndpointForm.vue`：`isModelPathLocked` 与切换类型时清空 `modelPath` 的 `watch` 都纳入 `deepseekSearch`；`modelPath` 占位文案新增 `'DeepSeek 搜索端点不解析模型'`。
- `src/views/EndpointsView.vue`：`endpointTypeVariant` 的 default 分支已返回 `'more'`，不改。
- `src/lib/testBody.ts`：注释中“不能构造请求体的类型”列表补上 `deepseekSearch`；`switch` 不加分支（落入 null）。

## 4. 文档

`CLAUDE.md`：

- `completion_endpoint_path` 段落里列举的“不在白名单内的端点”加上 DeepSeek 搜索。
- 在 “Unified generation routes” 节末尾新增一小段 “Web search emulation”，说明触发条件、按渠道优先级在 `exaSearch` / `deepseekSearch` 间选后端、后端决定 tool schema 并在服务端循环中保持一致，以及没有搜索渠道时非原生候选的 attempt 直接失败。

## 不在范围内

- DeepSeek Fetch（`/v1/tools/fetch`）端点类型与 `web_fetch` 工具模拟。
- 搜索请求的计费 / `tool_usage` 记录：DeepSeek 响应不含用量字段，按次计费可由 `getToolUsageCost` 脚本自行处理。

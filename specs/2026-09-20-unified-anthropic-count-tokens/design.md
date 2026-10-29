# 设计：unified 网关 Anthropic count_tokens 端点

## 结论先行

`/api/unified/v1/messages/count_tokens` 是一条**纯透传**路由，机制与 OpenAI Embeddings 完全同构：

| Path | Name | Format | SourceType |
| --- | --- | --- | --- |
| `/api/unified/v1/messages/count_tokens` | Unified Anthropic Count Tokens | `FormatUnknown` | `contract.EndpointType_AnthropicCountTokens`（5） |

`passthrough()` 由 `Format == FormatUnknown` 派生为 true，于是候选集合、尝试准备、成功路径、记录字段全部落进既有透传分支。后端改动只有 `unifiedRoutes` 一行（外加两处注释与测试用例），不引入 llmbridge 格式、数据库迁移、抽取器改动与前端逻辑。

## 为什么是透传

上游候选类型只有 `anthropicCountTokens`（5）：`candidateEndpointTypes` 的透传分支返回单元素集合 `{route.SourceType}`。

- **没有可用的转换器。** llmbridge 的五个格式都是生成格式；`third_party/axonhub/llm` 中唯一与 count_tokens 相关的代码是 vertex executor 把 `/v1/messages/count_tokens` 映射到 `count-tokens:rawPredict`，不存在让 count_tokens 请求/响应在格式间互转的 transformer。硬走跨格式转换，等于用生成语义去解释 `{"input_tokens": N}`，结果是错的。
- **不做 URL 后缀推断。** 「`anthropicMessages` 端点的 upstream_url 末尾追加 `/count_tokens`」能省一次配置，但把「该 upstream_url 是 messages 接口的完整 URL」升级成按端点类型推断的隐含约定。计数端点行由运营者显式配置：`endpoint.path` 是入口路径（同时供路径网关使用），`provider_endpoint.upstream_url` 是该上游计数接口的完整 URL。这与 embeddings 完全一致，也是 `anthropicCountTokens` 这个类型当初存在的理由。

## 复用的既有机制（逐项确认，均无需改动）

1. **候选解析。** `resolveProvidersByTypes(ctx, mode, {5}, 5)` → `GetProvidersByEndpointTypesAndModel`，要求模型未禁用、渠道未禁用、`provider_models` 条目命中该模型（条目若配了 `endpoints` 白名单，需把计数端点 path 列进去）。`dedupeUnifiedRows(rows, 5)` 与 `betterRow` 照常工作：候选集合里只有一种类型，`srcType` 匹配与 Anthropic 两条规则对所有行同样成立，每个渠道的幸存者落回 `endpoint.path` 字典序升序。
2. **尝试准备。** `newUnifiedGatewayFlowConfig` 因 `route.passthrough()` 选 `identityPrepareAttempt`：跳过 web search 改写、`beforeTransform` 钩子与 llmbridge 转换，请求体按构造好的字节往上游发。其余钩子（`sortProviders`、`beforeMetaRequest`、`rewriteModel`、`beforeRequest`、`rewriteRequest`、`afterUpstreamError`、`requestFinished`）照常执行。
3. **模型路由。** 固定路由不是前缀挂载，`extractUnifiedModel` 走 `modelFromBody(body, "model", false)`：`model` 缺失或为空串 → 400 `model_not_found`。`setUnifiedModel` 用 `sjson` 回写，因此 `rewriteModel` 与 `beforeRequest` 的 `upstreamModel` 覆写照常生效。
4. **上游格式。** `newUnifiedGatewayFlowConfig` 的 `upstreamFormat` 闭包对非 codex 类型调 `upstreamFormatFor(5)` → `FormatUnknown`，与 `route.Format` 相同 ⇒ `bridging = false`，`unifiedStreamSuccess` 退化为纯字节转发（写明响应体与响应头，除固定剥离的凭据类头）。
5. **记录字段。** meta 行的 `endpoint_path` = `route.Path`（`/api/unified/v1/messages/count_tokens`），upstream 行的 `endpoint_path` = 所选端点自身的 path（非前缀端点不追加后缀）。非流式 JSON 走读到 EOF，`finish_reason = 3`（正常结束）。
6. **工件聚合。** `defaultAggregationProfile(FormatUnknown)` 为 false，meta 侧不生成聚合视图；上游侧 `responseAggregationFormat(5, "")` 落到 default，同样不聚合。响应体 `{"input_tokens": N}` 没有对话可渲染，请求详情落回原始 JSON。
7. **脚本可见面。** `ctx.endpointType = "unified"`；`ctx.endpoint.endpointType = 5`（`anthropicCountTokens`）；`ctx.format` / `ctx.sourceFormat = "unknown"` —— 与 embeddings、codex 透传子路径一致，`docs/scripting.md` 的格式枚举表已把 countTokens 归入 `unknown`，无需改文档。
8. **查询串。** `buildUpstreamRequest` 的 `mergeClientQuery` 会把客户端查询串合并到上游 URL，因此 Anthropic 客户端的 `?beta=true` 原样透传，无需特殊处理。
9. **请求预览。** `extractUserMessage` 对 5 走 default 启发式链。计数请求体与 Messages 请求体同形，链中第一个 `extractOpenAIChatUserMessage` 与 `extractAnthropicUserMessage` 是同一个实现（都读 `messages[].content` 的 `extractRoleMessage`），因此预览取到最后一条 user 文本 —— 无需新增分支。
10. **计数不入库。** 响应没有 `usage` 对象，`extractJSONMetrics` 的四个格式分支依次落空 ⇒ 五个 token 字段（input / output / 三个 cache）全为 nil。`computeCost` 在 `inputTokens == nil` 时直接返回 false，所以 `model_cost` 保持 NULL —— 免费计数接口不会凭空产生费用。这与路径网关上 `anthropicCountTokens` 端点的现有行为一致，两条入口不会出现口径分叉。

## 统计口径：不需要迁移

`completion_endpoint_path`（migration 048）的三段都是**显式白名单**：端点表 `endpoint_type = ANY(ARRAY[2,3,4,7,8])`、codex 端点的两个子路径、以及七个 `/api/unified` 常量路径。新路径不在其中。`anthropicCountTokens`（5）与新路径都不在白名单里，计数请求自动落在成功率 / 空回复 / finish_reason 统计之外 —— 理由与 embeddings、Exa 搜索相同：`output_tokens` 恒为 0，纳入会被判成空回。**本次不新增迁移文件。**

请求量、token、费用统计（`request_overview_bucketed` 等）照常计入该路径的请求（token / 费用为 0，因为不入库）。

## 已知取舍

**请求列表的端点筛选是前缀感知的**（`ListRequests` 的 `= $n OR starts_with(endpoint_path, $n || '/')`，为 codex 前缀端点而设），所以按 `/api/unified/v1/messages` 筛选时，`count_tokens` 行会一并出现；反向（按 count_tokens 筛选）不受影响。计数流量占比极低，且与 Messages 请求指向同一上游，本次接受该行为，不为它引入「精确 vs 前缀」的筛选参数。

## 不引入的东西

- 不为 count_tokens 新增 llmbridge 格式、proto 枚举或转换器 —— 纯透传。
- 不新增端点类型：复用 `anthropicCountTokens`（5），`pkg/contract/endpoint.go` 与 `pkg/contract/label.go` 的 `enum` tag 均不动。
- 不新增 sqlc 查询：候选走按模型路由的类型集合查询。
- 不新增数据库迁移：统计口径视图的显式白名单天然排除新路径与类型 5。
- 不改 `ResponseExtractor` / `pricing.go`：计数结果不入库是明确决策，不是待补的缺口。
- 不改 `pkg/server/gateway_unified_helpers.go`、`handle_unified_gateway.go`、`server.go`（透传分支已泛化）。
- 不改 `user_message_preview.go`、`EndpointForm.vue`、`TestView.vue`、`conversation.ts`、`testBody.ts`（注释里「测试台无法构造请求体的类型」已包含 `anthropicCountTokens`，行为落 default 分支）。
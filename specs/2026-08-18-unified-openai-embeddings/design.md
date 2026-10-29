# 设计：unified 网关 OpenAI Embeddings 端点

## 结论先行

embedding 是**纯透传路由**：llmbridge 没有、也不需要 embedding 格式，请求和响应字节原样转发。上一版 Codex 三端点已经把 unified 的调度轴心从「格式」换成「路由」（`unifiedRoute.passthrough()` 由 `Format == FormatUnknown` 派生），这次新增一条透传路由几乎全部复用既有机制：

- 候选类型集合、模型提取 / 回写、成功路径、脚本钩子 —— **零改动**，透传分支已经覆盖；
- token usage 抽取 —— **零改动**，已实测验证（见下）；
- 数据库迁移 —— **不需要**，统计口径视图的白名单是显式枚举，新类型 13 与新路径都不在其中，天然被排除。

需要动的只有四处：端点类型常量与枚举、路由表一行、用户消息预览、前端标签。

## 端点类型

`pkg/contract/endpoint.go` 新增一个常量，续在 `codexSearchV1Alpha = 12` 之后：

| 常量 | 值 | 线上字符串 | 中文名 |
| --- | --- | --- | --- |
| `EndpointType_OpenAIEmbedding` | 13 | `openaiEmbedding` | OpenAI 特征提取 |

`ToEndpointType` / `FromEndpointType` 各加一条分支；`EndpointView.EndpointType`（`endpoint.go`）与 `EndpointLabel.EndpointType`（`label.go`）的 `enum` tag 同步扩充 —— 这两个 tag 决定 `openapi.yaml` 里的枚举值，进而决定 dashboard 的 `EndpointType` TS 联合类型。

这是一个**普通端点类型**：运营者照常在端点表建行（例如 path `/upstream/v1/embeddings`、类型 `openaiEmbedding`、模型字段路径 `model`），绑定到渠道，再在模型的 `provider_models` 里配置 embedding 模型名。路径网关也能直接服务它，与其它类型无差别。

## 路由表

`pkg/server/unified_routes.go` 追加第九条：

| Path | Name | Format | SourceType | 形态 |
| --- | --- | --- | --- | --- |
| `/api/unified/v1/embeddings` | Unified OpenAI Embeddings | `FormatUnknown` | 13 | 透传 |

`passthrough()` 由 `Format == FormatUnknown` 自动为 true，于是下列行为全部自动成立，无需新增分支：

- **候选集合** —— `candidateEndpointTypes` 的透传分支返回单元素集合 `{13}`：只有配置为 `openaiEmbedding` 的上游端点能服务它，命中不了返回 404 `no_provider_available`。不做「退到 chat completions」之类的降级。
- **模型路由** —— `extractUnifiedModel` 的非 Gemini 分支从 body 的 `model` 取（缺失或空串 → 400 `model_not_found`），`setUnifiedModel` 用 `sjson` 回写。因此 `rewriteModel` 钩子与 `beforeRequest` 的 `upstreamModel` 覆写照常生效。
- **尝试准备** —— `handleUnifiedGenerate` 对透传路由选 `identityPrepareAttempt`：不跑 web search 改写、不跑 `beforeTransform`、不做格式转换，也就不会拿 `FormatUnknown` 去查 `DefaultOutboundProfileForFormat`。其余钩子（`sortProviders`、`beforeMetaRequest`、`rewriteModel`、`beforeRequest`、`rewriteRequest`、`afterUpstreamError`、`requestFinished`）全部照常执行。
- **成功路径** —— `unifiedStreamSuccess` 在 `bridging=false`、`wsCtx=nil` 时退化为纯字节转发，同时把 `/api/unified/v1/embeddings` 记到 meta 行的 `endpoint_path`、把上游配置路径记到 upstream 行。聚合工件天然跳过（`defaultAggregationProfile(FormatUnknown)` 为 false）。
- **脚本可见的上游格式** —— `buildProviderModel` 传的是 `contract.FromEndpointType(t)`，新类型自动得到 `"openaiEmbedding"`。

**流式**：embedding 请求体没有 `stream` 字段，`detectStreaming` 五条规则全部落空 → `Streaming = false`。而透传路由的候选集合本就与流式标志无关（单元素集合），所以这个标志在此路由上不参与任何决策。响应是普通 JSON，`ResponseExtractor` 走 `json` 模式。

## token usage 抽取：无需改动（已验证）

`ResponseExtractor.extractJSONMetrics` 对 `usage` 先调 `setOpenAIInputTokens`，后者优先读 `usage.prompt_tokens`（读不到才退到 `usage.input_tokens`），没有 `prompt_tokens_details` 时不做 cache 拆分，直接写入 `InputTokens`。用真实 embedding 响应体探针实测：

```
输入 {"object":"list","data":[…],"model":"text-embedding-3-small","usage":{"prompt_tokens":8,"total_tokens":8}}
结果 InputTokens=8  OutputTokens=nil  CacheRead=nil  CacheWrite=nil  TTFT=nil
     InferredModel="text-embedding-3-small"（来源：response 字段）
```

`total_tokens` 对 embedding 恒等于 `prompt_tokens`，是冗余字段，不读取。`OutputTokens` / `TTFTMs` 为 nil → `metricsToPG` 产出 invalid 的 `pgtype.Int4` → `UpdateRequest` 的 `CASE WHEN` 跳过该列 → 数据库里保持 NULL，这正是「没有输出」的事实表达。

计价照常：`costsFor` 把 nil 的 output 当 `*int32(nil)` 传给 `computeCost`，只按 input 单价结算，embedding 模型在 `model.pricing` 里只配 input 价即可。

行为用一条回归测试钉住（`response_extractor_test.go`），而不是靠注释。

## 统计口径：不需要迁移

`completion_endpoint_path` 视图（migration 047）的两半都是**显式白名单**：

```sql
SELECT path FROM endpoint WHERE endpoint_type = ANY(ARRAY[2,3,4,7,8,11]::int[])
UNION ALL
SELECT unnest(ARRAY[ …七条 unified 路径… ])
```

类型 13 不在数组里，`/api/unified/v1/embeddings` 不在路径列表里，因此 embedding 请求自动落在成功率 / 空回复 / finish_reason 统计口径之外 —— 与 `codexSearchV1Alpha` 同款处理，理由也相同：`output_tokens` 天然为 0，纳入会被「空回复」判为失败。**本次不新增迁移文件。**

请求量、token、费用等不依赖该视图的统计（`request_overview_bucketed` 等）照常计入 embedding 请求。

## 用户消息预览

embedding 请求体形如 `{"model": "...", "input": "text"}` 或 `{"model": "...", "input": ["a", "b"]}`。

`extractUserMessage` 新增 `case contract.EndpointType_OpenAIEmbedding:` → 新函数 `extractEmbeddingUserMessage`：

- `input` 是字符串 → 取该字符串；
- `input` 是数组 → 取第一个非空字符串元素（多段输入时首段最有辨识度；token id 数组这类非字符串元素跳过）；
- 其它形状 → 不产出预览。

不复用 `extractOpenAIResponsesUserMessage`：它虽然也认 `input` 字符串，但数组分支只找 `{role: "user"}` 对象，对字符串数组返回空。这是可读性预览而非输入校验，best-effort 取值不违反 fail-fast 约定 —— 网关的输入校验在 `extractUnifiedModel`（model 缺失即 400）。

## 前端

后端字节透传，前端只需跟上枚举与显示名：

- `dashboard/src/api/index.ts`：`ENDPOINT_TYPE_LABELS` 加 `openaiEmbedding: 'OpenAI 特征提取'`；`ENDPOINT_TYPES_MODEL_ROUTED` 加 `'openaiEmbedding'`（embedding 按模型路由，端点列表页据此显示 accent 徽章）。`EndpointForm` 的类型下拉是从 `ENDPOINT_TYPE_LABELS` 的键推导的，加了标签即自动出现在选项里；模型字段路径输入框只对 `exaSearch` / `modelList` 禁用，embedding 保持可填 —— 无需改 `EndpointForm.vue`。
- `dashboard/src/utils/requestLabels.ts`：`UNIFIED_ENDPOINT_NAMES` 加 `'/api/unified/v1/embeddings': 'OpenAI 特征提取'`。
- `openapi.yaml` 与 `openapi-types.d.ts` 重新生成，`EndpointType` 联合类型自动扩容。

测试台（`TestView`）不改：`endpointTypeToFormat` 对 `openaiEmbedding` 落到 `default` 返回 `null`，发送按钮自动禁用 —— 与两条 Codex 透传端点同款，测试台只构造对话形态的请求体。

请求详情的对话渲染不改：`detectFormat` 对 embedding 请求体按 `'input' in root` 判为 `openaiResponses`（字符串 input 能正常显示为一条用户消息，字符串数组渲染为空）；响应体是浮点数组，没有对话可渲染，`detectFormat` 返回 `null`，落回原始 JSON 视图。这是可接受的现状，本次不做端点类型分派的渲染改造。

## 不引入的东西

- 不为 embedding 新增 llmbridge 格式、proto 枚举或转换器 —— 纯透传。
- 不新增 sqlc 查询 —— 按模型路由，现有类型集合查询够用。
- 不新增数据库迁移 —— 统计口径视图的白名单天然排除新类型。
- 不改 `ResponseExtractor` —— 现有 `prompt_tokens` 分支已覆盖，只补回归测试。
- 不做流式支持 —— 不注册 SSE 相关处理，`detectStreaming` 自然为 false。

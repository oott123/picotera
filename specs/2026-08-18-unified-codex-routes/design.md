# 设计：unified 网关 Codex 三端点

## 现状约束

现有 unified 网关的一切都以 `llmbridge.Format` 为轴心：

- `unifiedRoutes` 表是 `(Path, Format, Name)` 三元组，路由注册、端点标签列表都读它；
- `unifiedRoutePath(format)` 做 format → path 的**反查**，隐含「一种格式只有一条路由」；
- `sourceEndpointType(format)`、`candidateEndpointTypes(format, streaming)`、`extractUnifiedModel(format, …)`、`setUnifiedModel(format, …)` 全部 switch 在 format 上。

这次要加的三条路由打破了两个前提：

1. `/api/unified/codex/responses` 与 `/api/unified/v1/responses` **共用 `FormatOpenAIResponses`** —— format → path 的反查不再成立；
2. codex 压缩 / codex 搜索 v1alpha 在 llmbridge 里**没有对应格式**（也不需要有，它们是纯透传），却必须有自己的 endpoint_type 和候选集合。

因此本设计把 unified 的调度轴心从「格式」换成「路由」：路由表是唯一事实来源，格式只是路由的一个属性。

## 端点类型

`pkg/contract/endpoint.go` 新增两个常量，续在 `modelList = 10` 之后：

| 常量 | 值 | 线上字符串 | 中文名 |
| --- | --- | --- | --- |
| `EndpointType_CodexCompact` | 11 | `codexCompact` | Codex 压缩 |
| `EndpointType_CodexSearchV1Alpha` | 12 | `codexSearchV1Alpha` | Codex 搜索 v1alpha |

`ToEndpointType` / `FromEndpointType` 各加一条分支，`EndpointView.EndpointType` 与 `EndpointLabel.EndpointType` 的 `enum` tag 同步扩充（决定 openapi 里的枚举值，进而决定 dashboard 的 TS 联合类型）。

这两个类型是**普通端点类型**：运营者照常在端点表里建行（例如 path `/upstream/codex/responses/compact`、类型 `codexCompact`、模型字段路径 `model`），再绑定到渠道。路径网关也能直接服务它们，与其它类型无差别。

## 路由表：从格式轴换成路由轴

`pkg/server/unified_routes.go` 的条目扩为：

```go
type unifiedRoute struct {
	Path       string            // 挂载路径，也是 meta 行记录的 endpoint_path
	Name       string            // 端点标签列表里的显示名
	Format     llmbridge.Format  // 源格式；透传路由为 FormatUnknown
	SourceType int32             // contract.EndpointType_*，虚拟端点的 endpoint_type
}

// passthrough 报告该路由是否为纯透传（无跨格式转换）。llmbridge 没有对应格式
// 就意味着没有转换器，两者是同一件事，所以从 Format 派生而不是再存一个字段。
func (r unifiedRoute) passthrough() bool { return r.Format == llmbridge.FormatUnknown }
```

八条路由：

| Path | Format | SourceType | 形态 |
| --- | --- | --- | --- |
| `/api/unified/v1/messages` | AnthropicMessages | 4 | 转换 |
| `/api/unified/v1/responses` | OpenAIResponses | 3 | 转换 |
| `/api/unified/v1/chat/completions` | OpenAIChatCompletions | 2 | 转换 |
| `/api/unified/v1beta/models/{model}:generateContent` | GeminiGenerateContent | 7 | 转换 |
| `/api/unified/v1beta/models/{model}:streamGenerateContent` | GeminiStreamGenerateContent | 8 | 转换 |
| `/api/unified/codex/responses` | OpenAIResponses | 3 | 转换 |
| `/api/unified/codex/responses/compact` | Unknown | 11 | 透传 |
| `/api/unified/v1/alpha/search` | Unknown | 12 | 透传 |

`unifiedRoutePath` 与 `sourceEndpointType` 随之删除：前者的调用点改读 `route.Path`，后者改读 `route.SourceType`。`handleUnifiedGenerate` 的入参从 `llmbridge.Format` 改成 `unifiedRoute`，`registerEndpoints` 的注册循环直接把 `route` 传进去。路径全为静态段，chi 无歧义。

`upstreamFormatFor` 保留原样（新类型落到 `FormatUnknown`），因为透传路由的源格式同样是 `FormatUnknown`，`bridging := srcFormat != upFormat` 天然为 false。

## 各调度点的改造

**候选类型集合** —— `candidateEndpointTypes(route, streaming)`：透传路由返回 `[]int32{route.SourceType}`（只匹配同类型上游）；其余路由维持现有的「四格式 + Gemini 变体按流式过滤」表。

**模型提取 / 回写** —— `extractUnifiedModel(route, r, body)` 与 `setUnifiedModel(route, body, model)` 改按路由分派：Gemini 两条从 `{model}` 路径变量取、回写时不动 body；其余全部（含两条透传路由）从 body 的 `model` 取，缺失或为空串直接 400 `ModelNotFound`，回写用 `sjson` 覆盖 `model`。透传路由由此同样支持 `rewriteModel` 钩子与 `beforeRequest` 的 `upstreamModel` 覆写。

**候选解析** —— 复用 `GetProvidersByEndpointTypesAndModel`，不新增 SQL。透传路由传入单元素类型集合，于是「provider 必须在 `provider_models` 里配置该模型，且模型的 `endpoints` 白名单（若有）包含该端点路径」这一整套既有校验自动适用。`dedupeUnifiedRows` / `betterRow` 不变：单类型集合下 `srcType` 精确匹配总是排第一。

**尝试准备** —— 透传路由用 `identityPrepareAttempt`（路径网关同款：不跑 web search 改写、不跑 `beforeTransform`、不做格式转换）；转换路由维持 `prepareUnifiedAttempt`。这样 `prepareUnifiedOutboundProfile` 不会拿 `FormatUnknown` 去查 `DefaultOutboundProfileForFormat`（那会报错）。除 `beforeTransform` 外的全部钩子（`sortProviders`、`beforeMetaRequest`、`rewriteModel`、`beforeRequest`、`rewriteRequest`、`afterUpstreamError`、`requestFinished`）在透传路由上照常执行。

**成功路径** —— 两类路由统一走 `unifiedStreamSuccess`。透传时 `bridging=false`、`wsCtx=nil`，它退化为纯字节转发；同时它是唯一能把 meta 行记 `/api/unified/...` 路由模式、upstream 行记上游配置路径的分支，正是我们要的。聚合工件天然跳过：`defaultAggregationProfile(FormatUnknown)` 返回 `false`，`StreamAggregationKind(FormatUnknown, …)` 返回 `None`。token / TTFT 抽取由 `ResponseExtractor` 按内容类型启发式完成，与格式无关，因此压缩端点的用量照常入账。

**脚本可见的上游格式** —— `buildProviderModel(..., upstreamFormat)` 目前传 `upstreamFormatFor(t).String()`，新类型会得到 `"unknown"`。改为传 `contract.FromEndpointType(t)`：对现有五种生成类型两者字符串完全相同（无行为变化），新类型则得到 `codexCompact` / `codexSearchV1Alpha`。路径网关与 unified 的候选构建同步改。

## 用户消息预览

`extractUserMessage` 新增两条分支：`codexCompact` 走 OpenAI Responses 的提取（压缩请求体是 Responses 形状），`codexSearchV1Alpha` 走 `extractQueryUserMessage`（取 `query`，与 `exaSearch` 一致）。

## 统计口径

迁移 `047_codex_endpoints.sql` 用 `CREATE OR REPLACE VIEW` 重建 `completion_endpoint_path`（列结构不变，可原地替换；该视图只被查询引用，连续聚合不依赖它）：

- 端点表分支的类型白名单从 `ARRAY[2,3,4,7,8]` 扩为 `ARRAY[2,3,4,7,8,11]`；
- unified 常量路径追加 `/api/unified/codex/responses` 与 `/api/unified/codex/responses/compact`。

`codexSearchV1Alpha`（12）与 `/api/unified/v1/alpha/search` **不进入**该视图：搜索响应的 `output_tokens` 天然为 0，计入会被空回复统计误判为失败。Down 迁移还原 045 的定义。

## 前端

后端字节透传，前端渲染无需按端点类型分派：请求详情的对话渲染是按响应体形状判定的（`typeof root.output === 'string'` → 已有的 search alpha 渲染；压缩响应是 Responses 形状 → 走 Responses 渲染）。因此前端只需要跟上枚举与显示名：

- `ENDPOINT_TYPE_LABELS` 补两条中文名；`ENDPOINT_TYPES_MODEL_ROUTED` 补两个类型（二者都按模型路由，端点列表页据此显示 accent 徽章）；
- `UNIFIED_ENDPOINT_NAMES` 补三条路径显示名；
- `openapi.yaml` 与 `openapi-types.d.ts` 重新生成，`EndpointType` 联合类型自动扩容。

测试台（TestView）不做改动：它按格式选择 unified 路径，`/api/unified/codex/responses` 与 `/api/unified/v1/responses` 是同一格式；两条透传端点的请求体测试台构造不了（`endpointTypeToFormat` 落到 `default` 返回 `null`，发送按钮自动禁用）。

## 不引入的东西

- 不为压缩 / 搜索新增 llmbridge 格式、proto 枚举或转换器 —— 它们是纯透传。
- 不新增 sqlc 查询 —— 三条路由都按模型路由，现有类型集合查询够用。
- 不做「找不到同类型上游就退到 openaiResponses」之类的降级：候选集合就是单类型，命中不了返回 404 `NoProviderAvailable`。

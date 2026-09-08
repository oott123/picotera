# 设计：前缀端点缺失模型字段时按无模型路由

## 背景

无模型路由链路已经完整存在：`endpoint.model_path = ""` 的端点走 `GetProvidersByEndpoint`，候选行的 `model_name` / `upstream_model_name` 为空串，`f.model.Routed` 为 `""`，meta 行的 `model` 列为 NULL，`costsFor` / `fetchModelAnnotations` 都在空模型上短路，`buildUpstreamRequest` 也不会改写 body 的 `model`。

缺的只是"进入该链路的第二个入口"：目前只有端点配置（`model_path` 为空）能触发它，而单次请求的 body 里缺少模型字段一律是 400。

前缀端点的一个路径前缀代表一个上游 base_url，其子路径是开放的（`/responses`、`/responses/compact`、`/alpha/search` …），其中一部分子路径的请求体天然没有 `model` 字段。本设计让"这一次请求取不到模型"也能进入同一条无模型链路。

## 核心决策

**降级判定放在模型提取器内部。** 两个提取器（路径网关的 `extractModel`、统一路由的 `extractUnifiedModel`）改为直接返回 `gatewayModelMode`，自行决定"取到模型"还是"无模型"。`gateway_flow.go` 的主流程不需要任何改动：`resolveAndRewriteModel` 已经处理 `HasModel = false`（`rewriteModel` 返回非空即 `failHook`），`updateMetaModel("")` 写 NULL。

**降级只覆盖 body 分支。** `modelPath` 是单个 `{name}` 变量时（模型来自路径变量）保持原有的硬 400。前缀端点的路径不允许包含 `{}`（`validatePrefixEndpointPath`），因此这两者组合起来路径变量永远取不到值——`handleUpsertEndpoint` 直接拒绝该组合，避免配置出一个必然 400 的端点。Gemini 统一路由的 `{model}` 路径变量分支同理不变。

**降级条件按"前缀风格入口"划分。**

| 入口 | 判据 |
| --- | --- |
| 路径网关 | `endpoint.PrefixMatch` |
| 统一路由 | `unifiedRoute.PrefixMount`（新增字段，仅 `codexUnifiedRoute` 置位） |

`unifiedRoute` 已有的 `Codex` 字段表达的是"codex 是额外候选类型"，与"来自通配前缀挂载"是两件事，因此新增独立字段，将来的前缀挂载只需置位 `PrefixMount`。

**统一路由需要一条无模型查询。** 路径网关的 `GetProvidersByEndpoint` 按 `endpoint_path` 选，统一路由按端点类型集合选，两者不通用。新增姊妹查询 `GetProvidersByEndpointTypes`：列结构与 `GetProvidersByEndpointTypesAndModel` 完全一致，模型相关列（`model_name`、`upstream_model_name`、`priority`、`annotations`、`model_annotations`）压成常量，不 JOIN `model` 表、不展开 `provider_models`。Go 侧用 `fromNoModelTypesRow` 投影成既有行类型，`resolveProvidersByTypes` 内部按 `mode.HasModel` 二选一，去重、有效性过滤与优先级排序仍只有一份。

不采用"给现有查询加一个 `no_model` 布尔参数"的写法——一条 SQL 承担两种路由语义，与仓库里 admin overview 的既有取舍一致。

## 需要补的一个缺口

`buildRewrittenUpstreamRequest` 在统一路由分支里无条件调用 `SetBodyModel(f.body, input.UpstreamModel)`。无模型时 `UpstreamModel` 为空串，`sjson` 会往一个本来没有 `model` 字段的 body 里塞进 `"model": ""`，透传语义就破了。改为仅在 `UpstreamModel != ""` 时改写，与路径网关 `buildUpstreamRequest` 的既有条件一致。这也保留了 `beforeRequest` 钩子在无模型请求上通过 `upstreamModel` 主动注入模型名的能力。

## 行为约定

- 降级后 JS 侧 `ctx.requestModel` 与 `ctx.routedModel.name` 均为 `""`，脚本据此识别无模型请求；`ctx.endpoint.modelPath` 仍是端点配置的值。
- 候选集来自"绑定到该端点/端点类型的全部未禁用 provider"，与 `model_provider_endpoint` 配置无关，`model_cost` 不计算。
- codex `/responses` 子路径在缺失模型时也会降级（它是可桥接路由）。此时若命中非 codex 上游，llmbridge 会转换出一个空模型名的请求体，由上游判定——不额外拦截。

## 影响面

- 管理 API 契约无变化，无需重新生成 `openapi.yaml` 与 dashboard 类型。
- `handleUpsertEndpoint` 新增一条 400 校验（前缀端点 + 路径变量形式的 `modelPath`）。
- Dashboard 仅在端点表单的"模型字段路径"下补一句前缀端点的说明文案。

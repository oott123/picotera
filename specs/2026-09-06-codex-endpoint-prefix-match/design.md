# 设计：Codex 端点类型与端点前缀匹配

## 现状与问题

`2026-08-18-unified-codex-routes` 给 Codex 开了三条固定 unified 路由，并为其中两条各造了一个端点类型：

| 路由 | Format | SourceType |
| --- | --- | --- |
| `/api/unified/codex/responses` | OpenAIResponses | 3 |
| `/api/unified/codex/responses/compact` | Unknown | 11 codexCompact |
| `/api/unified/v1/alpha/search` | Unknown | 12 codexSearchV1Alpha |

三个问题：

1. **类型爆炸**。Codex 每多一个子路径（`/alpha/search`、未来的其它端点）就要加一个端点类型、一条 unified 路由、一条 `completion_endpoint_path` 白名单、一份前端枚举与显示名。类型本该描述「上游说什么协议」，现在却被用来描述「Codex 的哪个子路径」。
2. **上游侧同样要按子路径拆行**。一个 Codex 上游本来只有一个 base_url，运营者却要为 `/responses`、`/responses/compact`、`/alpha/search` 各建一个端点行、各绑一次渠道、各配一遍模型白名单。
3. **base_url 的 `/v1` 变体没法覆盖**。Codex 侧 base_url 既可能是 `…/api/unified/codex`，也可能是 `…/api/unified/codex/v1`，固定路由表只能穷举。

本设计把「子路径」从类型里抽出来，交给端点的**前缀匹配**：一个 codex 类型的端点行代表一个 Codex 上游的 base_url，请求路径里前缀之后的部分原样接到上游 URL 后面。

## 端点类型

`pkg/contract/endpoint.go`：

- 删除 `codexCompact` / `codexSearchV1Alpha` 的枚举字符串与 `ToEndpointType` / `FromEndpointType` 分支。11、12 两个数值保留为占位常量并注明已废弃、禁止复用（历史请求行、历史迁移仍带这两个值）。
- 新增 `EndpointType_Codex int32 = 14`，线上字符串 `codex`，中文名 `Codex`。
- `EndpointView.EndpointType` 与 `EndpointLabel.EndpointType` 的 `enum` tag 同步：去掉两条、加 `codex`。

`codex` 是一个**普通端点类型**，不同之处只有两点：必须开启前缀匹配（见下），以及它是 unified codex 挂载点唯一的透传候选类型。

## 端点前缀匹配

### 数据与契约

`endpoint` 表加列 `prefix_match BOOLEAN NOT NULL DEFAULT FALSE`（迁移 048）；`UpsertEndpoint` 查询与 `EndpointView` 同步加 `prefixMatch`。

`handleUpsertEndpoint` 的校验（全部 400，失败即拒，不做任何归一化）：

- `endpointType == "codex"` 时 `prefixMatch` 必须为 `true`；
- `prefixMatch == true` 时，`path` 不得包含 `{`/`}` 占位符（前缀匹配与路径变量互斥）、不得以 `/` 结尾、不得包含 `%` 或非 ASCII 字符（理由见「后缀取自 EscapedPath」）。

### 匹配规则

`pkg/server/endpoint_router.go` 的 `compiledEndpoint` 加 `prefix bool`，`Match` 多返回一个 `suffix string`：

- **普通端点**：行为不变，`suffix` 为空串。
- **前缀端点**：当且仅当 `strings.HasPrefix(reqPath, ep.Path+"/")` 时命中，`suffix = reqPath[len(ep.Path):]`（以 `/` 开头、非空）。请求路径恰好等于前缀本身**不命中** —— 前缀端点只服务子路径，光秃秃的 `/api/codex` 落回 404 / SPA 兜底。

排序不需要新规则：前缀条目的 `literalLen` 就是 `len(path)`，现有的「字面量长度降序、同长按路径升序」天然让更长的字面量胜出，因此挂在同一前缀下的精确端点（`/api/codex/responses`，字面量 20）永远排在前缀端点（`/api/codex`，字面量 10）前面。

**后缀取自 `EscapedPath`**：路由匹配照旧用 `r.URL.Path`（已解码），但送去拼接上游 URL 的后缀从 `r.URL.EscapedPath()` 上按同样的字节偏移切出来，这样上游看到的转义与客户端发来的完全一致。若 `EscapedPath()` 不是以端点路径开头（客户端把前缀里的字符做了百分号转义，如 `/api/co%64ex/responses`），直接 404 `route_not_found`，不做解码后重试。上面「前缀路径不得含 `%` 或非 ASCII」的校验保证了正常请求下两种形式的前缀字节相同。

### 上游 URL 拼接

后缀跟着**候选**走，不跟着请求走：`gatewayCandidateSidecar` 加 `AppendPath string`，仅当该候选的端点行 `prefix_match = true` 时取本次请求的归一化后缀，否则为空串。于是

- `sidecar.UpstreamURL + sidecar.AppendPath` 是实际请求的上游 URL；
- `sidecar.EndpointPath = 端点行 path + sidecar.AppendPath`，即 upstream 请求行记录的路径。

`buildUpstreamRequest` 新增 `appendPath string` 参数，在 `substitutePathVars` 之后拼接：把后缀接在 URL 字符串第一个 `?` 或 `#` **之前**，避免上游 URL 自带查询串（`…/v1?api-version=x`）时被拼坏。拼接发生在 `rewriteRequest` 钩子之前，因此脚本看到的 `pending.url` 已经是最终 URL。

`GetProvidersByEndpointTypesAndModel` 多选一列 `e.prefix_match`，让 unified 候选集构建能判断某一行是否前缀端点；路径网关只有一个端点行，直接读它自己的 `prefix_match`。

### 记录的 endpoint_path

`gatewayFlowConfig` 加 `RecordedEndpointPath string`：路径网关为 `endpoint.Path + suffix`，unified 为路由的 `Path`（见下）。meta 行的 `endpoint_path` 改记这个值；`config.Endpoint` 仍是数据库原行（`resolveProviders` 要用它的 `path` 查候选）。upstream 行记 `sidecar.EndpointPath`。

JS 侧：`ctx.endpoint.path` 仍是端点行配置的前缀（它是端点配置的投影），脚本要拿具体请求路径读 `ctx.request.path`。

## unified codex 挂载点

删除三条固定路由（`/api/unified/codex/responses`、`/api/unified/codex/responses/compact`、`/api/unified/v1/alpha/search`），改为一个通配挂载 `/api/unified/codex/*`（POST + OPTIONS，与其它 unified 路由同在 `corsMiddleware` 分组内）。`/api/unified/v1/embeddings` 等其余路由不变。

### 后缀归一化与分派

```
raw    := "/" + chi.URLParam(r, "*")
suffix := raw；若 raw == "/v1" 或以 "/v1/" 开头，则去掉这 3 个字符
若 suffix == "" 或 "/" → 404 route_not_found
```

于是 base_url 配 `…/api/unified/codex` 与 `…/api/unified/codex/v1` 等价。

每次请求由 `codexUnifiedRoute(suffix)` 现算一个 `unifiedRoute` 值：

| suffix | Path（meta 行 endpoint_path） | Format | SourceType | UpstreamSuffix |
| --- | --- | --- | --- | --- |
| `/responses` | `/api/unified/codex/responses` | OpenAIResponses | Codex | `/responses` |
| 其它 | `/api/unified/codex` + suffix | Unknown | Codex | suffix |

`unifiedRoute` 加两个字段：`UpstreamSuffix string`（前缀端点候选要拼的后缀，固定路由为空）与 `Codex bool`（是否由 codex 挂载点服务）。`passthrough()` 仍从 `Format == FormatUnknown` 派生，因此只有 `/responses` 走桥接路径。

`SourceType` 恒为 `EndpointType_Codex`：它喂给 `dedupeUnifiedRows` 的 `betterRow` 排序，让「同一个渠道同时配了 codex 端点和 openaiResponses 端点」时 codex 行胜出——这正是 Codex 流量该走的路。同时它也是虚拟端点的 `endpoint_type`，影响 `ctx.endpoint.endpointType` 与用户消息预览的分派（见下）。

### 候选类型集合

`candidateEndpointTypes(route, streaming)`：

- 透传路由（`Format == Unknown`）：`{route.SourceType}` —— codex 挂载点的非 `/responses` 子路径于是只匹配 codex 类型上游，命中不到即 404 `no_provider_available`；
- `/api/unified/codex/responses`：现有四类（AnthropicMessages、OpenAIChatCompletions、OpenAIResponses、按 stream 选的 Gemini 变体）**再加 `codex`**；
- 其余固定路由：不变。

### codex 候选的上游格式

`buildUnifiedCandidateSet` 的上游格式解析改为由调用方传入闭包。unified 配置里：

```
codex 类型 → route.Format（即「codex 上游说的就是本次 codex 源说的话」）
其它类型   → upstreamFormatFor(t)
```

于是 `/responses` 上：codex 候选 `UpstreamFormat == SourceFormat == OpenAIResponses`，`bridging` 为 false，`bridgeUnifiedRequest` 直接短路，字节透传；非 codex 候选照常从 OpenAI Responses 桥接过去。非 `/responses` 子路径上源格式与 codex 候选格式同为 `FormatUnknown`，同样恒等。路径网关继续用 `upstreamFormatFor`（codex → `FormatUnknown`，路径网关本来就恒等转发）。

JS 可见的 `providerModel.upstreamFormat` 仍是 `contract.FromEndpointType(...)`，codex 行读到 `"codex"`。

### 尝试准备

`route.passthrough()` 为真用 `identityPrepareAttempt`，否则 `prepareUnifiedAttempt`。`/responses` 上的 codex 候选走后者：`prepareUnifiedOutboundProfile` 拿 `OpenAIResponses` 的默认 profile（`beforeTransform` 照常触发），`bridgeUnifiedRequest` 因格式相同短路，web search 改写因源格式不是 Anthropic 而跳过。所有钩子的触发与今天一致。

### 运营者视角

一个 Codex 上游只需要一个端点行：

```
path=/api/codex  type=codex  prefixMatch=✓  modelPath=model
```

绑定渠道时 `upstream_url` 填上游 base_url（如 `https://chatgpt.com/backend-api/codex`），模型照常在渠道的 `provider_models` 里配。此后：

- 直连网关：`POST /api/codex/responses` → `{upstream_url}/responses`，`/api/codex/alpha/search` → `{upstream_url}/alpha/search`；
- unified：`POST /api/unified/codex/responses`（或 `/api/unified/codex/v1/responses`）按模型选中该端点 → 同样的上游 URL。

## 用户消息预览

`extractUserMessage` 删掉 11 / 12 两条分支，**不为 codex 加分支**：codex 请求体的形状随子路径变化（`/responses`、`/responses/compact` 是 Responses 形状，`/alpha/search` 是 `{query}` 形状），落到现有 `default` 分支的启发式链正好依次尝试 Responses 与 `query` 提取，无需按类型分派。

## 统计口径

迁移 048 用 `CREATE OR REPLACE VIEW` 重建 `completion_endpoint_path`（列结构不变）：

- 端点表分支的类型白名单退回 `ARRAY[2,3,4,7,8]`（11 已废弃）；
- 追加一段针对 codex 端点的展开：对每个 `endpoint_type = 14` 的行，生成 `path || '/responses'` 与 `path || '/responses/compact'` 两条。这与「记前缀 + 归一化后缀」的记录方式对齐，`path || '/alpha/search'` **不进入**视图——搜索响应的 `output_tokens` 天然为 0，计入会被空回复统计误判为失败；
- unified 常量路径保持 047 的七条：`/api/unified/codex/responses` 与 `/api/unified/codex/responses/compact` 正是归一化后记录的值，历史行与新行口径连续；`/api/unified/codex/alpha/search` 不在其中。

## 请求列表的端点筛选

前缀端点的请求行记的是带后缀的路径，端点标签列表里却只有前缀本身，精确相等的筛选会一条都筛不出来。`ListRequests` 的端点筛选改为前缀感知：

```sql
AND (@endpoint_path IS NULL
     OR r.endpoint_path = @endpoint_path
     OR starts_with(r.endpoint_path, @endpoint_path || '/'))
```

用 `starts_with` 而非 `LIKE`，避免筛选值里的 `%`/`_` 被当成通配符。对非前缀端点这个 OR 恒不命中（没有任何行会记 `path/…`），行为不变。

## 前端

- `src/api/index.ts`：`ENDPOINT_TYPE_LABELS` 去掉 `codexCompact` / `codexSearchV1Alpha`、加 `codex: 'Codex'`；`ENDPOINT_TYPES_MODEL_ROUTED` 同步（codex 按模型路由）。
- `EndpointForm.vue`：新增「前缀匹配」勾选项；类型选 codex 时自动勾上并禁用（提示「Codex 端点必须使用前缀匹配」）；勾选前缀匹配时路径输入框的 placeholder 改为 `例如 /api/codex`，并提示「请求路径中前缀之后的部分会原样接到上游 URL 后面」。
- `EndpointsView.vue`：前缀端点在路径列后显示 `/*` 标记，一眼可辨。
- `requestLabels.ts`：`UNIFIED_ENDPOINT_NAMES` 删掉 `/api/unified/v1/alpha/search`，补 `/api/unified/codex` → `Codex`、`/api/unified/codex/responses` → `Codex Responses`、`/api/unified/codex/responses/compact` → `Codex 压缩`、`/api/unified/codex/alpha/search` → `Codex 搜索 v1alpha`（`/api/unified/codex/responses` 原条目保留）。
- `RequestsView.vue` 的 `endpointDisplay`：精确查不到名字时按**最长前缀**回退到端点标签（`/api/codex/responses` → 显示 codex 端点的名字），仍查不到才显示原始路径。
- `testBody.ts`：注释里的不支持类型清单更新为 codex。测试台仍不为 codex 构造请求体（`endpointTypeToFormat` 返回 `null`，发送按钮禁用）——前缀端点还需要一个子路径，测试台没有这个输入。

## 不引入的东西

- 不为 codex 新增 llmbridge 格式或转换器：codex 上游要么与源格式相同（`/responses`），要么纯透传。
- 不做「codex 上游找不到就退回 openaiResponses 上游」之类的降级：`/responses` 的候选集本来就含四种生成类型，其余子路径就是 codex 单类型，命中不了返回 404。
- 不为 `prefix_match` 做兼容开关：新列默认 `FALSE`，现有端点行行为完全不变。
- 不保留 `/api/unified/v1/alpha/search`：Codex 的搜索请求改由 `/api/unified/codex/alpha/search`（或带 `/v1` 的变体）承接。

## 顺带修复

`pkg/server/handle_unified_gateway_test.go` 的 `TestUnifiedRoutesTable` 在当前 master 上就是失败的（用例表 10 条、路由表 9 条，多出一条 `/api/unified/codex/alpha/search`）。本次重写该测试表时一并修好。

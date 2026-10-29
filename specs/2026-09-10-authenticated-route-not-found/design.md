# 设计：携带有效 API key 的未匹配路由

## 背景

`gatewayHandler.ServeHTTP` 是 `/` 上的 catch-all 挂载：任何没被 `/api/picotera`、`/api/unified` 或 endpoint 表命中的路径都会落到这里。路由未命中时它只看请求"像不像浏览器导航"（GET/HEAD 且 Accept 含 `text/html` 或 `*/*`）来决定回落 SPA 还是返回 JSON 404。

这条判据对真实的 API 客户端并不可靠：`curl`、Go 的 `http.Client`、以及不少 SDK 默认发 `Accept: */*`，GET 类接口（`/v1/models` 之类）配错路径时拿到的是一整页 dashboard HTML；即便返回了 JSON 404，也没有任何一条请求记录，操作者在 dashboard 上看不到"有人打错了地址"。

鉴权发生得太晚是根因：API key 校验在 `gatewayFlow.run()` 内部，而 SPA 回落发生在流程还没开始之前，做决定时手上没有"这是不是一个已认证的 API 客户端"这条信息。

## 核心决策

**鉴权提前到 HTTP 入口，且只做一次。** `authenticateClient` 从 `gatewayFlow.authenticateAndBackfill` 里上移到三个 HTTP 入口（catch-all 网关、统一路由、codex 挂载），结果以值类型 `clientAuth{APIKey, User, Err}` 传进 `newGatewayFlow`。流程内部不再自己查 key，只消费这个结果——因此未命中分支先鉴权、再构造流程时也不会产生第二次数据库往返。

`clientAuth` 携带 `Err` 而不是只在成功时构造：鉴权失败的语义没有变——匹配到端点时仍然先插入 meta 行、再以 401/403 终止它，鉴权失败的请求同样留下记录。上移的只是查询时机，不是失败的处理位置。

**SPA 回落判据加一个前置条件。**

| 路由未命中 | 有效 API key | 结果 |
| --- | --- | --- |
| 是 | 是 | CORS 头 + JSON 404 + 一条 meta 记录 |
| 是 | 否，且是浏览器导航 | SPA html（不变） |
| 是 | 否，且非浏览器导航 | JSON 404，不记录（不变） |

只有"鉴权成功"这一件事会改变判定。key 无效或被禁用时不返回 401/403，而是维持原判据：路由解析先于鉴权，一个不存在的路径上的坏 key 应该报"没有这个路由"，而不是暴露"这个路径存在但你没权限"。

**未命中的记录复用 `gatewayFlow`，在鉴权后立即终止。** 新增一个 `gatewayRouteKind`：`gatewayRouteNotFound`。`run()` 的前半段（读 body → 解析 OTR 头 → 插入 meta 行 → 鉴权回填）原样跑完，紧接着以 404 `ROUTE_NOT_FOUND` 终止，不进入 `resolveAndRewriteModel`。这样一条 404 记录自带：请求 artifact（凭据已脱敏）、`user_message_preview`、project 归属、trace 关联、OTR 模式、以及 dashboard 的实时中断能力——全部来自既有代码，不需要一条平行的写入路径。

不把它塞进 `ResolveCandidates` 之类的既有扩展点：那会先执行 `resolveAndRewriteModel`，在一个零值 `db.Endpoint` 上创建 JS session 并跑 `rewriteModel`，让用户脚本在 404 上被意外调用。`run()` 里一次显式的 kind 分支比这诚实。

**记录什么。** `endpoint_path` 写 `r.URL.Path`（与路由匹配用的解码路径一致），`model` / `provider_id` 为 NULL，`status_code = 404`，`error_message = "route not found"`，`finish_reason = 1`（internal）。不为路由未命中新增 finish_reason 取值——状态码加错误消息已经说清楚了，新增取值要连带改 dashboard 的 finish-reason 分布图。

## 影响面与取舍

- **静态资源**：带有效 API key 时，`/assets/index-*.js` 这类路径同样返回 JSON 404 并被记录。这正是"永远不返回 SPA html"的字面含义。浏览器访问 dashboard 不会带 API key（dashboard 走 `PICOTERA_AUTH_HEADER_NAME` 或单用户模式），实际不受影响；只有当反向代理注入的 `Authorization`/`X-Api-Key` 值恰好等于一个真实 api key 时才会命中，这种巧合不做补偿。
- **数据库开销**：未命中且未携带任何 token 时 `authenticateClient` 直接返回错误，不查库——静态资源请求的常态是零额外开销。携带 token 才会有一次 key + 一次 owner 查询。
- **统计口径**：这些行是 `type = 1` 的 meta 行，会进入 `request_overview_bucketed` 的请求数（token 与 cost 为 0）。成功率相关的图表按 `completion_endpoint_path` 取范围，未匹配路径不在白名单内，因此不会被算成失败。
- **请求列表筛选**：端点下拉来自 `endpoint` 表，未匹配路径不会出现在其中，只能通过时间/关键字定位；dashboard 的 `longestPrefixName` 回落会直接显示原始路径。
- **codex 别名**：`/api/unified/backend-api/codex/...` 上的 404 会把别名路径原样记进 `endpoint_path`。"别名从不出现在请求行里"这条不变量此后只覆盖被正常服务的 codex 请求；404 行不参与 `completion_endpoint_path`、连续聚合和端点筛选，不影响任何统计。
- 管理 API 契约无变化，无需重新生成 `openapi.yaml` 与 dashboard 类型；dashboard 无需改动（无 provider / 无 model 的失败 meta 行早已存在，401 场景就是）。

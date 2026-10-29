# Design: unified 路由按候选真实端点的 resolver 发送上游凭证

## 根因

发往上游的鉴权方式由 `effectiveSendResolver(endpointResolver, peResolver)` 决定：`provider_endpoint.credentials_resolver` 非 `unknown` 时用它，否则继承 `endpoint.credentials_resolver`。

- 路径网关（`buildPathCandidateSet`）传入的是路由命中的真实 `endpoint` 行，行为正确。
- unified 网关（`buildUnifiedCandidateSet`）传入的是 `newUnifiedGatewayFlowConfig` 构造的虚拟端点 `virtualEndpoint`，其 `CredentialsResolver` 固定为 `CredentialsResolver_Unknown`。`GetProvidersByEndpointTypesAndModel` / `GetProvidersByEndpointTypes` 两条查询只选出了 `pe.credentials_resolver`，没有选出候选自身端点的 `e.credentials_resolver`。

结果：只要 provider_endpoint 未覆盖 resolver，unified 候选的 `SendResolver` 一律是 `Unknown`，`applyCredentials` 落入 followRequest 分支，按客户端请求的鉴权位置写凭证。Anthropic 客户端带 `X-Api-Key`，于是 OpenAI 上游也收到 `X-Api-Key`，端点上配置的 `bearerToken` 被忽略。

## 修复

1. 两条类型集查询在 `pe.credentials_resolver AS send_credentials_resolver` 之后新增一列 `e.credentials_resolver AS endpoint_credentials_resolver`，sqlc 生成的两个 Row 类型随之多出 `EndpointCredentialsResolver int32`。
2. `fromNoModelTypesRow` 复制该字段。
3. `buildUnifiedCandidateSet` 改为 `effectiveSendResolver(row.EndpointCredentialsResolver, row.SendCredentialsResolver)`，与路径网关的继承规则一致。`virtualEndpoint` 参数此后在函数内不再被使用，从签名中删除，调用点（`handle_unified_gateway.go`、`gateway_flow_test.go`）同步更新。

`virtualEndpoint.CredentialsResolver` 仍保持 `Unknown`：它只作为 JS 侧 `ctx.endpoint` 的描述，unified 路由本身没有可配置的 resolver。

`resolveWebSearchBackend` 也调用 `GetProvidersByEndpointTypes`，但只读取端点路径与类型；新增的列对它没有影响。

`followRequest` 的语义不变（见 proposal 补充决定）。


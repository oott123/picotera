# Plan: unified 路由按候选真实端点的 resolver 发送上游凭证

设计见 `design.md`。

## Step 1 — 查询补列

1. `db/queries/routing.sql`：在 `GetProvidersByEndpointTypesAndModel` 与 `GetProvidersByEndpointTypes` 的 `pe.credentials_resolver AS send_credentials_resolver,` 之后各加一行 `e.credentials_resolver AS endpoint_credentials_resolver,`。
2. 运行 `sqlc generate`，确认 `pkg/db/routing.sql.go` 中 `GetProvidersByEndpointTypesAndModelRow` / `GetProvidersByEndpointTypesRow` 新增 `EndpointCredentialsResolver int32` 字段。

## Step 2 — 候选构建

1. `pkg/server/gateway_unified_helpers.go` `fromNoModelTypesRow`：在 `SendCredentialsResolver` 后加 `EndpointCredentialsResolver: r.EndpointCredentialsResolver,`。
2. `pkg/server/gateway_flow_candidates.go` `buildUnifiedCandidateSet`：
   - 删除参数 `virtualEndpoint db.Endpoint`；
   - `SendResolver` 改为 `effectiveSendResolver(row.EndpointCredentialsResolver, row.SendCredentialsResolver)`。
3. `pkg/server/handle_unified_gateway.go`：`buildUnifiedCandidateSet(...)` 调用去掉 `virtualEndpoint` 实参。
4. `go build ./...` 通过。

## Step 3 — 测试

`pkg/server/gateway_flow_test.go`：

1. `TestBuildUnifiedCandidateSetAnnotationsAndFormat`、`TestBuildUnifiedCandidateSetCodex` 的调用去掉 `db.Endpoint{}` 实参。
2. 新增 `TestBuildUnifiedCandidateSetSendResolver`，表驱动，三条用例：
   - 端点 `bearerToken`、pe `unknown` → `SendResolver == CredentialsResolver_BearerToken`（本 bug 的回归用例）；
   - 端点 `bearerToken`、pe `xApiKey` → `CredentialsResolver_XApiKey`（pe 覆盖）；
   - 端点 `followRequest`、pe `unknown` → `CredentialsResolver_FollowRequest`。
3. `go test ./pkg/server/...` 通过。


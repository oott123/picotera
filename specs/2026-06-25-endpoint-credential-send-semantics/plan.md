# 执行计划

## 1. 解析与 resolver 解耦（`pkg/server`）

- `gateway_helpers.go`：
  - `extractClientToken(r *http.Request, resolver int32) string` 改为 `extractClientToken(r *http.Request) string`，删除 `switch resolver` 分支，函数体恒定返回 `pickFirst(bearer, xApi, query, goog)`；更新函数文档注释。
  - `authenticateClient(ctx, r, resolver int32)` 去掉 `resolver` 参数，内部调用改为 `extractClientToken(r)`。
  - `applyCredentials` 的 `default` 分支注释 `// GeneralApiKey / Unknown / others` → `// FollowRequest / Unknown / others`。
- `gateway_flow.go`：
  - 删除 `gatewayConfig.Credentials int32` 字段。
  - 删除 `if cfg.Credentials == 0 { cfg.Credentials = cfg.Endpoint.CredentialsResolver }` 块。
  - `authenticateClient(f.ctxs.Request, f.r, f.config.Credentials)` → `authenticateClient(f.ctxs.Request, f.r)`。
- `handle_gateway.go`：删除 `Credentials: endpoint.CredentialsResolver,` 赋值。
- `handle_unified_gateway.go`：删除 `Credentials: contract.CredentialsResolver_Unknown,` 赋值。
- `handle_model_list.go`：`authenticateClient(r.Context(), r, endpoint.CredentialsResolver)` → `authenticateClient(r.Context(), r)`。

## 2. 枚举值重命名 `generalApiKey` → `followRequest`（`pkg/contract`）

- `endpoint.go`：
  - 常量 `CredentialsResolver_GeneralApiKey` → `CredentialsResolver_FollowRequest`。
  - `ToCredentialsResolver`：`case "generalApiKey"` → `case "followRequest"`。
  - `FromCredentialsResolver`：返回 `"followRequest"`。
  - `EndpointView.CredentialsResolver` 的 `enum` 标签首项 `generalApiKey` → `followRequest`。
- `provider_endpoint.go`：`enum` 标签 `generalApiKey` → `followRequest`。
- `provider.go`：三处 `modelsEndpointResolver` 的 `enum` 标签 `generalApiKey` → `followRequest`。

## 3. UI 文案（`dashboard/src`）

- `components/EndpointForm.vue`：
  - 默认值 `?? ('generalApiKey' as const)` → `?? ('followRequest' as const)`。
  - 下拉项 `{ value: 'generalApiKey', label: '通用密钥' }` → `{ value: 'followRequest', label: '跟随请求' }`。
  - `<Field label="凭证解析">` → `<Field label="凭证发送">`。
- `views/EndpointsView.vue`：
  - `<Th>凭证解析</Th>` → `<Th>凭证发送</Th>`。
  - `e.credentialsResolver === 'generalApiKey'` → `=== 'followRequest'`。
- `components/ProviderForm.vue`：
  - 默认值 `?? 'generalApiKey'` → `?? 'followRequest'`。
  - 下拉项 `{ value: 'generalApiKey', label: '通用 API Key' }` → `{ value: 'followRequest', label: '跟随请求' }`。
  - `<Field label="模型列表凭证解析">` → `<Field label="模型列表凭证发送">`。
- `components/ProviderEndpointsPanel.vue`：
  - 下拉项 `{ value: 'generalApiKey', label: '通用密钥' }` → `{ value: 'followRequest', label: '跟随请求' }`。

## 4. 重新生成类型

- `mise run openapi` → 更新 `openapi.yaml`。
- `pnpm --dir dashboard generate-openapi` → 更新 `dashboard/src/openapi-types.d.ts`。

## 5. 验证

- `go build ./...`，确认无 `Credentials` 字段残留引用、无 `CredentialsResolver_GeneralApiKey` 残留。
- `grep -rn "generalApiKey\|GeneralApiKey" pkg dashboard/src openapi.yaml` 应无业务命中。
- 现有 Go 单测（`pkg/server`、`pkg/llmbridge`）通过；`gateway_flow_test.go` / `gateway_flow_attempts_test.go` 的 `SendResolver` / `SendCredentialsResolver` 字段属发送路径，无需改动，确认仍编译通过。
- `pnpm --dir dashboard type-check` 通过。

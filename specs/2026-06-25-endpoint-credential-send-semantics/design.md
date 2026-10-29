# 设计

## 背景

端点的 `credentialsResolver`（DB 整数列 `credentials_resolver`，`generalApiKey=1`）当前被用于两条互不相关的路径：

1. **下游解析**：`authenticateClient(ctx, r, resolver)` → `extractClientToken(r, resolver)`，按 resolver 指定的优先位置取客户端 key，取不到再 fallback 扫描全部位置。
2. **上游发送**：`effectiveSendResolver(endpoint, peOverride)` → `applyCredentials(req, creds, sendResolver, source)`，决定凭证以何种 header / query 写入上游请求。

本次改动把这两条路径解耦：解析路径不再依赖任何 resolver，字段语义收窄为“仅发送”。

## 改动设计

### 1. 解析与 resolver 解耦

- `extractClientToken` 去掉 `resolver` 参数，恒定按固定顺序 `Authorization Bearer → X-Api-Key → ?key= → X-Goog-Api-Key` 取首个非空值。这正是现有 fallback 逻辑，删除前置的 `switch resolver` 分支即可。
- `authenticateClient` 去掉 `resolver` 参数。
- 两处调用点（`gateway_flow.go`、`handle_model_list.go`）去掉传参。
- `gatewayConfig.Credentials` 字段不再被使用，连同其赋值（`gateway_flow.go` 的 `if cfg.Credentials == 0` 块、`handle_gateway.go`、`handle_unified_gateway.go` 的两处 setter）一并删除——这是清理无用字段，不引入兼容层。

发送路径（`effectiveSendResolver` / `applyCredentials` / `SendCredentialsResolver` / `SendResolver`）完全不动。

### 2. 枚举值重命名 `generalApiKey` → `followRequest`

`ToCredentialsResolver` / `FromCredentialsResolver` 是 endpoint、provider_endpoint 覆盖、provider 模型列表解析器三者共用的字符串↔整数转换器，因此重命名一处即全局生效，三者行为一致。整数值 `1` 不变，DB 无需迁移。

- Go 常量 `CredentialsResolver_GeneralApiKey` → `CredentialsResolver_FollowRequest`（内部标识，调用点仅在 `endpoint.go` 内）。
- 转换器的 case 字符串 `"generalApiKey"` → `"followRequest"`。
- 三个 contract 文件中 `enum:"...generalApiKey..."` 标签替换为 `followRequest`。
- `applyCredentials` 的 `default` 分支注释由 `GeneralApiKey` 更新为 `FollowRequest`——该分支行为（镜像源请求携带凭证的位置，无源时写三个 header）本就对应 followRequest 语义，逻辑不变。

### 3. UI 文案

- 端点表单/列表的字段标签 `凭证解析` → `凭证发送`（`EndpointForm.vue`、`EndpointsView.vue`）。
- `provider` 的 `模型列表凭证解析` → `模型列表凭证发送`（`ProviderForm.vue`）。
- 三处下拉选项 `generalApiKey` / 文案 `通用密钥`、`通用 API Key` → 值 `followRequest`、文案 `跟随请求`（`EndpointForm.vue`、`ProviderForm.vue`、`ProviderEndpointsPanel.vue`）。
- 默认值 `'generalApiKey'` → `'followRequest'`（`EndpointForm.vue`、`ProviderForm.vue`）。
- `EndpointsView.vue` 的 `e.credentialsResolver === 'generalApiKey'` 比较改为 `'followRequest'`。

### 4. 类型再生成

改完 contract 后按既定流程 `mise run openapi` 重新生成 `openapi.yaml`，再 `pnpm --dir dashboard generate-openapi` 重新生成 TS 类型，使枚举值同步。

## 影响与风险

- DB 不变，存量数据的整数值 `1` 现按 `followRequest` 渲染，行为等价。
- 解析行为变化：当客户端同时携带多个位置凭证且端点 resolver 原本指向靠后位置时，新逻辑改取固定顺序的首个——这是本次有意收窄的行为。
- 无兼容层、无 fallback 分支保留，符合项目“干净替换”约定。

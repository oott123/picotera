# API 设计

所有路径均在管理前缀 `/api/picotera` 下，经 chi 层 `auth.Middleware` 鉴权（解析当前用户入 context）。

## 鉴权分层（行为，无新路径）

管理操作分两个 Huma group：

- **用户 group**：全体登录用户可访问。
- **管理 group**：经 `requireAdmin` 中间件；非 `is_admin` 用户返回 `403 {"title":"Forbidden","status":403,"detail":"admin required"}`（Huma 标准错误体）；context 无 user 返回 500。

划入管理 group 的操作（非管理员 → 403）：

```
provider list/get/create/upsert/update-models/delete, fetchModels
model    list/get/put/delete
endpoint list/upsert/delete
providerEndpoint list/upsert/delete
project  list/get/upsert/delete/merge
script   list/get/create/update/delete
kv       list/get/upsert/delete
exchangeRate get/put/delete, matchPricing   (list 见用户 group)
user     list/get/create/update/delete
userIdentity list/create/update/delete
globalSetting list/get/upsert/delete
simulateDispatch
```

划入用户 group（全体登录用户）：

```
getMe
overview summary/distribution/series/speed-boxplot
apiKey   list/get/create/update/delete
request  list/get/list-by-span/list-traces/get-live/interrupt
exchangeRate list   (货币换算依赖，用户功能可读)
label    providers/models/endpoints/projects/upstream-models   (新增，见下)
```

## 新增：标签接口（只读，用户 group）

仅返回展示 / 过滤所需的最小字段，不含 `credentials` 等敏感配置。

### `GET /api/picotera/labels/providers`

```json
[{ "id": 1, "name": "openrouter" }]
```

### `GET /api/picotera/labels/models`

```json
[{ "name": "claude-sonnet-4-6" }]
```

### `GET /api/picotera/labels/endpoints`

```json
[{ "path": "/v1/messages", "name": "anthropic", "endpointType": "anthropicMessages" }]
```

`endpointType` 用于网关测试「配置端点」模式推断请求体格式，与 `EndpointView.endpointType` 同枚举。

### `GET /api/picotera/labels/projects`

```json
[{ "id": 3, "name": "picotera" }]
```

契约定义于新增的 `pkg/contract/label.go`：

- 视图：`ProviderLabel{ID,Name}`、`ModelLabel{Name}`、`EndpointLabel{Path,Name,EndpointType}`、`ProjectLabel{ID,Name}`。
- 响应：各 `Body []XxxLabel`。
- operation：`OperationListProviderLabels` 等四个，`Tags:["Label"]`。
- handler 复用既有 `ListProviders`/`ListModels`/`ListEndpoints`/`ListProjects` 查询并投影，不新增 sqlc。

## 短路测试（原始 chi 路由）

`POST /api/picotera/test/direct`：在 `handleTestDirect` 入口加管理员校验，非管理员 →
`403 {"message":"admin required"}`（与该 handler 既有 JSON 错误风格一致，非 Huma 错误体）。

## `/me`（无改动）

`GET /api/picotera/me` → `{ "id", "displayName", "isAdmin" }`。前端两栏分区与路由守卫均消费 `isAdmin`。

## 网关测试（无改动）

经 API Key 鉴权，走 `/api/unified/*` 与网关 catch-all，不在 `/api/picotera` 下，本次不改动。

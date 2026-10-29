# API 设计

本期不新增 REST 操作，仅调整既有操作的归属语义与视图字段。所有受影响操作仍在 `/api/picotera` 下，由 auth 中间件鉴权；用户归属取自 context，不作为查询参数暴露。

## 视图字段变更

### `ApiKeyView`

新增字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `userId` | `int64` | 该 Key 所属用户 ID。 |

### `RequestView`

新增字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `userId` | `int64`（可空，`0`/省略表示未归属） | 该请求所属用户 ID。 |

`CreateApiKeyRequest`/`UpdateApiKeyRequest` 的请求体**不**新增归属字段：创建时 owner 固定为当前登录用户，不支持改派。

## 行为变更（操作语义）

以下操作的返回集合/可达对象被限制为「当前用户归属」：

- `listApiKeys` / `getApiKey` / `updateApiKey` / `deleteApiKey`：仅作用于本人密钥；`createApiKey` 归属当前用户。
- `listRequests` / `getRequest` / `listRequestSpans` / `listRequestTraces`：仅本人请求与追踪。
- `getRequestLive` / `interruptRequest`：仅对本人请求行有效；非本人请求返回 `inFlight=false` / `interrupted=false`（不泄露存在性）。
- `getOverviewSummary` / `getOverviewDistribution` / `getOverviewSeries` / `getOverviewSpeedBoxplot`：仅统计本人数据。

跨用户访问单个请求/追踪/密钥时返回 404（视作不存在），不返回 403，避免泄露资源存在性。

## 网关 / unified（API Key 鉴权）

- 解析 API Key → 取 `user_id` → 查所属用户。
- 所属用户 `disabled` 为真：返回 `403`。
- 请求与追踪写入对应 `user_id`。

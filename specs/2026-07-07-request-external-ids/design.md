# 设计

## 背景

网关为每个请求写两行 `request` 记录：meta 行（type=0，代表客户端请求）和 upstream 行（type=1，代表一次上游尝试）。当前没有字段记录外部系统关联 ID（客户端请求 ID / 上游响应 ID），无法将 PicoTera 记录与外部系统（客户端、CDN、上游 provider）的日志关联。

## 方案

### 数据库

新增迁移 `042_request_external_ids.sql`，给 `request` 表加两个 nullable 列：

- `external_request_id TEXT` — 客户端请求 ID（仅 meta 行写入，upstream 行为 NULL）。
- `external_response_id TEXT` — 上游响应 ID（meta 和 upstream 行均写入）。

`request` 是 TimescaleDB hypertable，`ALTER TABLE ... ADD COLUMN` 直接可用。两个 continuous aggregate（`request_overview_hourly`、`request_speed_hourly`）按维度聚合 token/cost，不涉及这两个列，无需改动。

### 配置

`pkg/configx/configx.go` 新增两个字段（沿用现有 `gateway_*` 前缀）：

| 字段 | mapstructure key | 环境变量 | 默认值 |
|---|---|---|---|
| `GatewayExternalRequestIDHeaders` | `gateway_external_request_id_headers` | `PICOTERA_GATEWAY_EXTERNAL_REQUEST_ID_HEADERS` | `X-Request-Id,X-Log-Id,Cf-Ray` |
| `GatewayExternalResponseIDHeaders` | `gateway_external_response_id_headers` | `PICOTERA_GATEWAY_EXTERNAL_RESPONSE_ID_HEADERS` | `X-Request-Id,X-Log-Id,Cf-Ray` |

每个值是逗号分隔的 header name 列表。`NewServer` 启动时解析为 `[]string`，存于 `Server` 结构体。解析时按逗号分割、逐项 trim，若分割后某项为空字符串则启动报错（fail fast）。

### Header 匹配

新增辅助函数 `matchExternalIDHeader(header http.Header, names []string) pgtype.Text`：按 `names` 顺序调用 `header.Get(name)`，返回第一个非空值；全部未命中返回 `pgtype.Text{Valid: false}`。`http.Header.Get` 走 canonical MIME header 匹配（大小写不敏感），无需手动 case-fold。

### 记录 `external_request_id`（meta 行，客户端请求 header）

在 `insertMetaRequest`（`gateway_flow.go`）中，从 `f.r.Header` 提取 `external_request_id`，随 `InsertRequestParams` 一起写入。此时客户端 header 已可用（body 已读完、meta 行即将插入）。upstream 行的 `InsertRequest` 传 `pgtype.Text{Valid: false}`。

### 记录 `external_response_id`（upstream + meta 行，上游响应 header）

上游响应到达后，从响应 header 提取 `external_response_id`，在完成行写入时通过 `requestUpdate.ExternalResponseID(...)` 更新。无上游响应的路径（认证失败、全部 provider 失败、forward error 等）该字段保持 NULL。

**完成路径与响应 header 可用性：**

| 路径 | upstream 行 | meta 行 | 响应 header 来源 |
|---|---|---|---|
| path 成功 `completeGatewaySuccess` | ✓ | ✓ | `input.Response.Header` |
| unified 成功 `unifiedStreamSuccess` 尾部 | ✓ | ✓ | `a.resp.Header` |
| path 解码失败 `openPathInternalReader` | ✓（`completeFailedAttemptWithReason`） | ✓（直接 update） | `resp.Header` |
| unified 桥接失败 `failUnifiedSuccess` | ✓ | ✓ | `a.resp.Header` |
| path 桥接失败 `failSuccessPath` | ✓（`completeFailedAttemptWithReason`） | ✓（`failMeta`） | `input.Response.Header` |
| 非 200 + hook break `respondUpstreamErrorBreak` | ✓（`handleUpstreamNonOK` 已更新） | ✓（`failMeta`） | `origHeader`（= `resp.Header`） |
| 非 200 不 break `handleUpstreamNonOK` | ✓ | —（不结束 meta） | `resp.Header` |
| forward error `recordAttemptFailure` | ✓（`completeFailedAttemptWithReason`） | — | nil（无响应） |
| 认证失败 / 全部失败 / hook 失败 `failMeta` | — | ✓（NULL） | nil（无响应） |

**签名变更：**

- `failMeta(status int32, errMsg string, finishReason int32)` → `failMeta(status int32, errMsg string, finishReason int32, respHeader http.Header)`。有响应的调用方传 `resp.Header`/`origHeader`，无响应的传 `nil`。`failMeta` 内部在 `respHeader != nil` 时提取 `external_response_id`。
- `completeFailedAttemptWithReason(...)` → 末尾加 `respHeader http.Header`。有响应的调用方传响应 header，无响应的传 `nil`。内部同理提取。

这两个函数都在 `*Server` / `*gatewayFlow` 上，可通过 `s.externalResponseIDHeaders` / `f.h.externalResponseIDHeaders` 访问已解析的 header name 列表。

### sqlc 查询改动

- `InsertRequest`（`db/queries/routing.sql`）：列清单末尾加 `external_request_id, external_response_id`，VALUES 加 `$23, $24`。
- `UpdateRequest`（`db/queries/request.sql`）：加 `external_response_id = CASE WHEN sqlc.arg('set_external_response_id')::bool THEN sqlc.narg('external_response_id')::text ELSE external_response_id END`（仅 `external_response_id`，`external_request_id` 仅 insert 时写入）。
- `ListRequests`（`db/queries/request.sql`）：显式列清单加 `r.external_request_id, r.external_response_id`。
- `ListRequestsBySpan`（`db/queries/request.sql`）：显式列清单加 `r.external_request_id, r.external_response_id`。
- `GetRequest`：使用 `SELECT r.*`，sqlc 自动展开新列，无需手改。

### 合同与 View

`pkg/contract/request.go`：

- `RequestView` 加 `ExternalRequestID string `json:"externalRequestId,omitempty"`` 和 `ExternalResponseID string `json:"externalResponseId,omitempty"``。
- `requestLike` 加 `ExternalRequestID pgtype.Text` 和 `ExternalResponseID pgtype.Text`。
- `toRequestView` 在 `Valid` 时填入。
- `ToRequestView`、`ToListRequestRowView`、`ToListRequestsBySpanRowView` 映射新字段。

### `requestUpdate` builder

`pkg/server/request_update.go` 加 `ExternalResponseID(v pgtype.Text) *requestUpdate`，翻转 `SetExternalResponseID` flag。

### 重新生成

`sqlc generate` → `mise run openapi` → `pnpm --dir dashboard generate-openapi`。

## 非目标

- 不改 dashboard 前端展示（字段已通过 API 暴露，前端可后续按需展示）。
- 不改 continuous aggregates。
- 不对 `test/direct` 路径记录 external IDs（该路径不写 `request` 行）。
- 不为 upstream 行记录 `external_request_id`（仅 meta 行记录客户端请求 ID）。

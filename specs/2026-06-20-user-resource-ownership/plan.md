# 执行计划

## 1. 数据库迁移

### 1.1 迁移 035：列新增（事务内）

新建 `db/migrations/035_user_ownership.sql`：

- `ALTER TABLE api_key ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1;` → `ALTER TABLE api_key ALTER COLUMN user_id DROP DEFAULT;`
- `ALTER TABLE request ADD COLUMN user_id BIGINT DEFAULT 1;` → `ALTER TABLE request ALTER COLUMN user_id DROP DEFAULT;`（保持可空）
- `ALTER TABLE traces ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1;` → `ALTER TABLE traces ALTER COLUMN user_id DROP DEFAULT;`（非空）
- 追踪唯一键改为复合：`ALTER TABLE traces DROP CONSTRAINT traces_parent_span_id_key;` 然后 `ALTER TABLE traces ADD CONSTRAINT traces_parent_span_id_user_id_key UNIQUE (parent_span_id, user_id);`
- 索引：
  - `CREATE INDEX request_user_id_idx ON request (user_id, created_at DESC, id DESC);`
  - `CREATE INDEX api_key_user_id_idx ON api_key (user_id);`
  - `CREATE INDEX traces_user_id_idx ON traces (user_id, last_request_at DESC, id DESC);`
- Down：删索引、还原 `traces` 唯一约束为 `UNIQUE (parent_span_id)`、`DROP COLUMN user_id`（三表）。

### 1.2 迁移 036：重建连续聚合（`-- +goose NO TRANSACTION`）

新建 `db/migrations/036_overview_caggs_user_id.sql`，对 `request_overview_hourly` 与 `request_speed_hourly` 各执行：移除策略 → drop view → 重建（SELECT 与 GROUP BY 加入 `user_id`）→ `materialized_only = false` → 重加策略。完整复制 `025`/`028` 的结构，仅在列清单与 `GROUP BY` 中插入 `user_id`。Down 重建为不含 `user_id` 的版本。

## 2. sqlc 查询（`db/queries/`，改后 `sqlc generate`）

### 2.1 `api_key.sql`

- `ListApiKeys`：`WHERE user_id = $1 ORDER BY created_at DESC, id DESC`。
- `GetApiKey`：`WHERE id = $1 AND user_id = $2`。
- `GetApiKeyByKey`：不变（返回行已含 `user_id`）。
- `InsertApiKey`：列加 `user_id`，新增参数。
- `UpdateApiKey`：`WHERE id = $1 AND user_id = $N`（`user_id` 不进 SET）。
- `DeleteApiKey`：`WHERE id = $1 AND user_id = $2`。

### 2.2 `request.sql`

- `ListRequests`：加 `AND r.user_id = sqlc.arg('user_id')::bigint`。
- `GetRequest`：加 `AND user_id = sqlc.arg('user_id')::bigint`。
- `ListRequestsBySpan`：`anchor` 子查询与外层均加 `user_id` 过滤。
- `ListRequestTraces`：`WHERE` 加 `traces.user_id = sqlc.arg('user_id')::bigint`；所有指标/预览/项目 LATERAL 子查询各加 `AND request.user_id = traces.user_id`。
- `InsertRequest`：列加 `user_id`，新增可空参数。
- `UpdateRequestOnHeader`：SET 加 `user_id = $N`（回填 meta 行）。

### 2.3 `trace.sql`

- `UpsertTrace`：列加 `user_id`，`VALUES` 含 `user_id`，`ON CONFLICT` 目标改为 `(parent_span_id, user_id)`，`LEAST/GREATEST` 维持窗口。
- `ListTraceBackfillCandidates`：`GROUP BY parent_span_id, user_id`，输出列含 `user_id`。
- `BackfillTrace`：插入列加 `user_id`，`ON CONFLICT (parent_span_id, user_id)` 维持窗口。
- `trace_backfill.go`：`BackfillTraceParams` 传入候选行的 `user_id`。

### 2.4 `overview.sql`

对下列查询全部加用户必填过滤（cagg 类过滤本表 `user_id`，`request r` 类过滤 `r.user_id`）：`GetOverviewTotals`、`CountTraces`（`traces.user_id`）、`CountTracesFiltered`、`ListOverviewDistribution`、`ListOverviewDistributionCosts`、`ListOverviewTraceCountsByDimension`、`ListOverviewSeriesMetrics`、`ListOverviewSeriesTraces`、`ListOverviewCacheHitRateSeries`、`GetOverviewTokenBreakdown`、`ListOverviewBreakdownTokens`、`ListOverviewBreakdownCosts`、`ListOverviewSpeedSeries`、`GetOverviewSpeedBoxplot`。

运行 `sqlc generate` 重新生成 `pkg/db/`。

## 3. 网关 / unified（用户解析与写入）

- `pkg/server/gateway_flow.go`：`gatewayAuthState` 增加 `UserID pgtype.Int8`。
- `pkg/server/gateway_helpers.go`：
  - `authenticateClient` 解析 Key 后用 `GetUserByID(apiKey.UserID)` 取用户；`disabled` 为真返回 403 `gatewayError`。返回值保留 `*db.ApiKey`，由调用方读取 `UserID`（或扩展返回用户）。
  - `upsertTrace` 增加 `userID pgtype.Int8` 参数，且仅在 `userID` 有效时执行；其在 `insertRequest` 内的调用改为传 `arg.UserID`（meta 行 UserID 为空 → 自动跳过）。
- `authenticateAndBackfill`：设置 `f.auth.UserID`；`UpdateRequestOnHeader` 传 `UserID`；并显式调用 `upsertTrace(parentSpanID, f.meta.CreatedAt, f.auth.UserID)` 以 meta 时间为锚点创建追踪。
- `gateway_flow.go` 的 meta `insertRequest`：`UserID` 置空（鉴权前未知，trace 触发被跳过）。
- `gateway_flow_attempts.go` 上游 `insertRequest`：`UserID = f.auth.UserID`。
- `gateway_unified_helpers.go`：`unifiedStreamArgs` 增 `userID`，`unifiedStreamArgsFromSuccess` 填 `input.Flow.auth.UserID`，两处 `UpdateRequestOnHeader` 传入。
- 同步检查 unified 的失败/错误路径（`failMeta` 等）中的 `UpdateRequestOnHeader` 调用是否也需带 `UserID`。

## 4. 管理 API handler（注入当前用户）

新增小助手（如 `pkg/server/server.go` 或新文件）：`func requireUser(ctx) (*db.AppUser, error)`，缺失返回 500。各 handler 读取后传 `u.ID`：

- `handle_api_key.go`：list / get / update / delete 传 `UserID`；create 设 `UserID = u.ID`。
- `handle_requests.go`：`handleListRequests`、`handleGetRequest`、`handleListRequestSpans`、`handleListRequestTraces` 传 `UserID`；`GetRequest` 未命中→沿用现有 404。
- `handle_request_live.go`：`handleGetRequestLive`、`handleInterruptRequest` 先 `GetRequest(id, u.ID)` 校验归属；未命中分别返回 `inFlight=false` / `interrupted=false`。
- `handle_overview.go`：所有 overview 查询调用传 `UserID`（覆盖 summary / distribution / series / speed-boxplot 各自调用的查询）。

## 5. 契约与代码生成

- `pkg/contract/api_key.go`：`ApiKeyView` 增 `UserID int64 json:"userId"`，`ToApiKeyView` 填充。
- `pkg/contract/request.go`：`RequestView` 增 `UserID int64 json:"userId,omitempty"`，各 `To*View` 填充（行 `user_id` 可空时映射为 0）。
- `mise run openapi` 重新生成 `openapi.yaml`。
- `pnpm --dir dashboard generate-openapi` 重新生成 TS 类型。

## 6. 前端

无功能改动（隔离在服务端透明完成）。仅确认重新生成类型后 `pnpm --dir dashboard type-check` 通过。

## 7. 验证

- `go build ./...` 通过；`sqlc generate` 无 diff 漏改。
- 启动后跑迁移：035→036 顺序生效；现有 `api_key`/`request`/`traces` 行 `user_id` 均为 1。
- 用两个 header 身份（`PICOTERA_AUTH_HEADER_ENABLED` + `AUTO_CREATE_USER`）造两个用户，各自创建 Key、发起网关请求，验证：
  - 列表/单查/追踪/概览互相不可见。
  - 跨用户取单个请求/密钥返回 404。
  - 跨用户 get-live/interrupt 返回未在途/未中断。
  - 概览数字按用户切分正确。
- 两个用户用**相同的 `parent_span_id`** 各发请求：生成两条独立追踪，各自的 `ListRequestTraces` 只聚合自己的请求，互不串号。
- 禁用某用户后，其 Key 的网关请求返回 403。
- 运行既有 Go 测试（`pkg/server/`、`pkg/llmbridge/`）确保未破坏。

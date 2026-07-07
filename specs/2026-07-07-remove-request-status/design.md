# 设计

## 总体

彻底删除 `request` 表的 `status` 列及其在 API 契约、sqlc 生成代码、Go 网关写入路径、前端中的全部使用点。`status` 的语义由 `(finish_reason, status_code)` 精确还原（见 `proposal.md` 语义等价表）。

## 数据库

### 迁移 `041_drop_request_status.sql`

```sql
-- +goose Up
ALTER TABLE request DROP COLUMN status;

-- +goose Down
ALTER TABLE request ADD COLUMN status INTEGER NOT NULL DEFAULT 0;
```

`status` 列无索引、无连续聚合引用、无外键，直接 DROP COLUMN 即可。`finish_reason` 已是可空列（默认 NULL），删除 `status` 后"进行中"语义由 `finish_reason IS NULL` 表达，无需新增列或默认值。

### SQL 查询改动

- `db/queries/routing.sql` `InsertRequest`：从 INSERT 的列列表与 VALUES 中移除 `status`（第 5 个参数）。新行以 `finish_reason = NULL` 自然表示"进行中"。
- `db/queries/request.sql` `ListRequests`、`ListRequestTraces`（第 177 行起）：从 SELECT 列表移除 `r.status`。
- `db/queries/overview.sql`（第 340 行）与 `db/queries/admin_overview.sql`（第 306 行）：`AND status = 2` 改为 `AND status_code = 200 AND finish_reason IN (2, 3, 5)`，保持速度指标口径完全等价。
- `db/queries/request.sql` `UpdateRequest`：移除 `status` 的 CASE 分支与 `set_status` / `status` 参数。

改完后运行 `sqlc generate` 重新生成 `pkg/db/`。

## 后端 Go

### 常量

`pkg/db/request_constants.go`：删除 `RequestStatusPending`、`RequestStatusHeaderReceived`、`RequestStatusCompleted`、`RequestStatusFailed` 四个常量及其注释。`FinishReason*` 常量保留不动。

### 契约 `pkg/contract/request.go`

- `RequestView`：删除 `Status int32` 字段（第 19 行）。
- `requestLike`：删除 `Status int32` 字段（第 76 行）。
- `toRequestView`（第 107 行）：删除 `Status: r.Status` 赋值。
- 三个 `From*View`/拷贝构造（第 211、245、279 行）：删除 `Status: r.Status`。
- `RequestLiveView` 的 `statusCode` 字段与 live 查询不受影响（那是内存态，不是 DB `status`）。

### 写入路径 `pkg/server/`

- `request_update.go`：删除 `Status(v int32)` setter 方法（第 69-73 行）。
- 所有 `Status(db.RequestStatus*)` 调用点删除：
  - `gateway_flow.go:226`、`gateway_flow_attempts.go:231`（INSERT 的 `Status: db.RequestStatusPending`）——随 `InsertRequestParams` 字段删除而删除。
  - `gateway_flow_attempts.go:337`、`gateway_flow_errors.go:61`、`gateway_flow_success.go:122/129/158/247/251`、`gateway_helpers.go:892`、`gateway_unified_helpers.go:348/355/582/586/642/649` 中的 `.Status(db.RequestStatus*)` 链式调用。
  - `gateway_flow_success.go` 与 `gateway_unified_helpers.go` 中 `status := int32(db.RequestStatusCompleted)` 局部变量及其在 `updateRequest(...).Status(status)` 的使用：删除局部变量与 `.Status(...)` 调用。`finishReason` 已在同一 `updateRequest` 链上写入，不补任何替代字段。
- HeaderReceived 转换（`gateway_flow_success.go:122/129`、`gateway_unified_helpers.go:348/355`）：原 `.Status(db.RequestStatusHeaderReceived)` 调用整体删除。该 DB 状态转换无任何读取方（无 `WHERE status = 1` 查询；前端合并 0/1 为"处理中"；实时面板用内存态 `phase`），删除无功能损失。

### `InsertRequestParams`

`sqlc generate` 后 `pkg/db/request.sql.go` 的 `InsertRequestParams` 不再含 `Status` 字段；调用方（`gateway_flow.go`、`gateway_flow_attempts.go`）相应删除 `Status: db.RequestStatusPending`。

## 前端

### `dashboard/src/components/RequestDetailsContent.vue`

- 删除 `statusLabel` 函数（第 184-194 行）。
- `requestState`（第 171-178 行）：改为基于 `finishReason` 判断进行中，其余按 `statusCode` 分类（与原逻辑等价）：
  ```ts
  function requestState(r: RequestView): RequestState {
    if (r.finishReason === undefined || r.finishReason === null) return 'pending'
    if (r.statusCode === undefined || r.statusCode === null) return 'err'
    if (r.statusCode >= 200 && r.statusCode < 300) return 'ok'
    if (r.statusCode >= 400 && r.statusCode < 500) return 'warn'
    return 'err'
  }
  ```
- `isInFlight`（第 48-50 行）：`r.status === 0 || r.status === 1` 改为 `r.finishReason === undefined || r.finishReason === null`。
- 概览网格：删除"状态" `Field`（第 403-412 行）；将"完成原因" `Field`（原第 466-470 行）移动到原"状态"的位置（紧跟"类型" `Field` 之后）。
- span 卡片与"状态码" `Field` 中 `requestState(...) === 'pending'` 的判断随 `requestState` 改动自动生效，无需额外改动。

### `dashboard/src/views/RequestsView.vue`

- `requestState`（第 519-524 行）：改为基于 `finishReason` + `statusCode` 推导（与原 `status` 等价）：
  ```ts
  function requestState(r: RequestView): RequestState {
    if (r.finishReason === undefined || r.finishReason === null) return 'pending'
    if (r.statusCode !== undefined && r.statusCode !== null
        && r.statusCode >= 200 && r.statusCode < 300
        && [2, 3, 5].includes(r.finishReason)) return 'ok'
    return 'err'
  }
  ```
- 列 `#cell-status` / `#header-status` 的 key、标签"完成原因"、筛选器均不变（它们本就以 finishReason 为内容）。

### OpenAPI 与类型

`RequestView` 删除 `status` 字段后，运行 `mise run openapi` 重生成 `openapi.yaml`，再 `pnpm --dir dashboard generate-openapi` 重生成 `dashboard/src/openapi-types.d.ts`。

## 验证

- `go build ./...` 通过。
- `sqlc generate` 与 `go vet ./...` 无残留 `Status` 引用。
- 请求列表/详情页对进行中、正常结束、各类失败、半截交付取消的请求展示与改动前一致。
- 速度指标（概览速度分布）口径不变（等价谓词替换）。

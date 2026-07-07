# 执行计划

## 阶段 1：数据库与 sqlc

1. 新建 `db/migrations/041_drop_request_status.sql`，Up 删除 `request.status` 列，Down 恢复（`ADD COLUMN status INTEGER NOT NULL DEFAULT 0`）。
2. `db/queries/routing.sql`：`InsertRequest` 从列列表与 VALUES 移除 `status`（含对应占位符）。
3. `db/queries/request.sql`：
   - `ListRequests`、`ListRequestTraces` 的 SELECT 移除 `r.status`。
   - `UpdateRequest` 移除 `status` 的 CASE 分支、`set_status` 与 `status` 参数。
4. `db/queries/overview.sql` 第 340 行、`db/queries/admin_overview.sql` 第 306 行：`AND status = 2` → `AND status_code = 200 AND finish_reason IN (2, 3, 5)`。
5. 运行 `sqlc generate`。

## 阶段 2：后端 Go

6. `pkg/db/request_constants.go`：删除 `RequestStatus*` 四常量及注释。
7. `pkg/contract/request.go`：`RequestView`、`requestLike` 删 `Status` 字段；`toRequestView` 与三个拷贝构造删 `Status` 赋值。
8. `pkg/server/request_update.go`：删除 `Status(v int32)` setter。
9. `pkg/server/gateway_flow.go`、`gateway_flow_attempts.go`：删除 INSERT 处的 `Status: db.RequestStatusPending`。
10. `pkg/server/` 全部 `.Status(db.RequestStatus*)` 链式调用删除（`gateway_flow_attempts.go:337`、`gateway_flow_errors.go:61`、`gateway_flow_success.go:122/129/158`、`gateway_helpers.go:892`、`gateway_unified_helpers.go:348/355/642/649`）。
11. `gateway_flow_success.go`（~247）与 `gateway_unified_helpers.go`（~582）：删除 `status := int32(db.RequestStatusCompleted)` 局部变量、其 `if streamErr` 重赋值、以及 `updateRequest(...).Status(status)` 中的 `.Status(status)`；`FinishReason(...)` 调用保留。
12. `go build ./...` 确认无残留引用。

## 阶段 3：前端

13. `RequestDetailsContent.vue`：删除 `statusLabel`；改写 `requestState`、`isInFlight` 为基于 `finishReason`/`statusCode`；删除"状态" `Field`，将"完成原因" `Field` 移到原"状态"位置。
14. `RequestsView.vue`：改写 `requestState` 为基于 `finishReason` + `statusCode` 的等价实现。
15. `mise run openapi` 重生成 `openapi.yaml`。
16. `pnpm --dir dashboard generate-openapi` 重生成类型。

## 阶段 4：验证

17. `pnpm --dir dashboard type-check` 通过。
18. `pnpm --dir dashboard lint` 通过。
19. `go build ./...` 与 `go test ./...` 通过。
20. 人工核对：进行中（finishReason 缺省）、正常结束（3）、内部错误（1）、读取超时（5）、半截交付取消（2 + 200）、流式错误（6 + 200）在各视图的展示与改动前一致。

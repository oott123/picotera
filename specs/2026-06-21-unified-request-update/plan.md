# 执行计划

## 0. 撤销临时补丁

撤掉上一轮在 `gateway_flow_success.go`、`gateway_unified_helpers.go` 中临时补传 `ProjectID`（含 `projectID := input.Flow.meta.ProjectID` 行）的改动——本方案会用 builder 重写这些调用点。

## 1. 改写 SQL 查询（`db/queries/request.sql`）

- 删除 `UpdateRequestOnHeader`、`UpdateRequestModel`、`UpdateRequestUserMessagePreview`、`UpdateRequestMetrics`、`UpdateRequestOnComplete` 五条查询。
- 新增 `UpdateRequest`（见 `design.md`「查询形态」整段）：24 列各为 `col = CASE WHEN sqlc.arg('set_col')::bool THEN <value> ELSE col END`；`status`/`inferred_model_source` 值用 `sqlc.arg`（NOT NULL），其余用 `sqlc.narg`；`WHERE id = … AND created_at = …`。

## 2. 重新生成 sqlc

```bash
sqlc generate
```

确认 `pkg/db/request.sql.go` 中新增 `UpdateRequest`/`UpdateRequestParams`，并移除上述 5 条查询的生成代码；`pkg/db/querier.go` 同步更新。

## 3. 新增 Go builder（`pkg/server/request_update.go`）

按 `api.md` 表实现 `requestUpdate` 结构、`newRequestUpdate`、24 个链式 setter，以及 `(*Server).updateRequest`（执行 + 出错记日志）。

## 4. 迁移调用点（`pkg/server`）

逐处把旧的 `db.UpdateRequest*Params` 结构体调用换成 `newRequestUpdate(id, createdAt).<setter>…` + `updateRequest`：

- `gateway_flow.go`：`authenticateAndBackfill`（回填，setter：`ApiKeyID`/`UserID`/`ProjectID`）、`updateMetaModel`（`Model`）、预览回填（`UserMessagePreview`）。
- `gateway_flow_success.go`：`markPathHeadersReceived` 两处（`ProviderID`/`Model`/`UpstreamModel`/`EndpointPath`/`Status`，**不含** `UserID`/`ProjectID`）；三处 complete。
- `gateway_unified_helpers.go`：`unifiedStreamSuccess` 两处 header（同上，不含 `UserID`/`ProjectID`）；四处 complete。
- `gateway_flow_attempts.go`、`gateway_flow_errors.go`、`gateway_helpers.go`：各 complete 调用点。

complete 类调用点的 setter 组合见 `api.md`。

## 5. 删除旧 wrapper

删除 `gateway_helpers.go` 中 `updateRequestOnHeader`、`updateRequestModel`、`updateRequestUserMessagePreview`、`updateRequestOnComplete` 四个方法（已无引用）。

## 6. 验证

- `go build ./...` 编译通过。
- `go test ./pkg/server/...` 通过（现有纯 struct 单测不触 DB）。
- 端到端手测：`docker compose up -d` 起依赖，`mise run server`；用带项目路径标记（如 `Primary working directory: /xxx`）的请求打通一个真实渠道。重点验证两条回归路径：
  1. 请求**完成后**刷新 dashboard Requests（默认 meta 视图），project 列仍在；upstream 行 project 也在。
  2. 完成态的 `status`、tokens、`model_cost`、`finish_reason`、`user_message_preview` 等仍正确写入（即统一查询未回退原 complete 行为）。

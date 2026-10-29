# 执行计划

## Phase 1: 后端 — finishReason 查询参数

1. **`db/queries/request.sql`** — `ListRequests`：在 `empty_response` 分支后加 `AND (sqlc.narg('finish_reason')::int IS NULL OR r.finish_reason = sqlc.narg('finish_reason')::int)`。
2. **`sqlc generate`** — 重新生成 `pkg/db/request.sql.go`，确认 `ListRequestsParams` 新增 `FinishReason pgtype.Int4`，且 `ListRequests` query args 顺序中 `arg.FinishReason` 位于 `arg.EmptyResponse` 与 `arg.CursorCreatedAt` 之间。
3. **`pkg/contract/request.go`** — `ListRequestsRequest` 加 `FinishReason *int32 \`query:"finishReason,omitempty"\``。
4. **`pkg/server/handle_requests.go`** — `handleListRequests`：加 `filterFinishReason` 局部变量（`input.FinishReason != nil` 时置 Valid），传入 `ListRequestsParams{ FinishReason: filterFinishReason }`。
5. **`mise run openapi`** — 重新生成 `openapi.yaml`。

## Phase 2: 前端类型 + 数据层

6. **`pnpm --dir dashboard generate-openapi`** — 重新生成 `dashboard/src/openapi-types.d.ts`，确认 `listRequests` query 含 `finishReason?: number`。
7. **`dashboard/src/api/queryKeys.ts`** — `RequestsFilters` 加 `finishReason?: number`。

## Phase 3: 前端 — 列表页

8. **`dashboard/src/views/RequestsView.vue`**：
   - `filters` reactive 加 `finishReason: 0`。
   - `requestFilters` computed 加 `if (filters.finishReason) out.finishReason = filters.finishReason`。
   - 筛选变更 watch 数组加 `filters.finishReason`。
   - `activeFilterCount` / `clearAllFilters` 加 `finishReason`。
   - `columns` 中 `status` 列 `header` 改"完成原因"，`headerClass` 加 `filters.finishReason` 激活下划线。
   - 新增 `finishReasonOptions` 常量数组（7 项 `{ value, label }`，label 用 `finishReasonLabel` 的固定文案）。
   - 模板 `#header-status` slot 加 `ColumnFilter`（`v-model.number="filters.finishReason"`、`:empty-value="0"`、`:searchable="false"`、`:options="finishReasonOptions"`）。
   - `#cell-status` 改：pending → "处理中"（沿用 pending 样式）；否则 `finishReasonLabel(row.finishReason)`，status=2 绿底、status=3 红底。

## Phase 4: 前端 — 详情面板文案

9. **`dashboard/src/components/RequestDetailsContent.vue`** — 第 466 行 `Field label="停止原因"` → `label="完成原因"`。

## Phase 5: 验证

10. **`go build ./cmd/picotera`** — 确认后端编译。
11. **`pnpm --dir dashboard type-check`** — 确认前端类型。
12. **`pnpm --dir dashboard lint --fix`** — 确认 lint。
13. **`go test ./pkg/server/ -run Request`** — 跑请求相关单测（若有）。

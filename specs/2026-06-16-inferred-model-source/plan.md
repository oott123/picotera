# 执行计划：推测模型来源字段

## 1. 数据库迁移

- 新建 `db/migrations/032_request_inferred_model_source.sql`：
  - Up：`ALTER TABLE request ADD COLUMN inferred_model_source SMALLINT NOT NULL DEFAULT 0;`
  - Down：`ALTER TABLE request DROP COLUMN IF EXISTS inferred_model_source;`

## 2. Golang 枚举

- 在 `pkg/db/request_constants.go` 新增常量块：
  - `InferredModelSourceUnknown = 0`
  - `InferredModelSourceSignature = 1`
  - `InferredModelSourceResponse = 2`

## 3. 提取来源（response_extractor.go）

- `ResponseMetrics` 增加字段 `InferredModelSource int32`。
- 在 `Metrics()` 中，与 `InferredModel` 同步计算：`sigModel != ""` → `Signature`；否则 `respModel != ""` → `Response`；否则 `Unknown`。

## 4. SQL 查询（db/queries/request.sql）

- `UpdateRequestOnComplete` 的 `SET` 子句新增 `inferred_model_source = $17`。
- `ListRequests`、`ListRequestsBySpan` 的 SELECT 列追加 `r.inferred_model_source`（`GetRequest` 用 `SELECT *`，无需改）。
- 运行 `sqlc generate` 再生 `pkg/db/`。

## 5. 写入路径（gateway_flow_success.go）

- `completeGatewaySuccess` 中两处 `UpdateRequestOnCompleteParams`（upstream 行、meta 行）各增加：
  `InferredModelSource: pgtype.Int2{Int16: int16(m.InferredModelSource), Valid: true}`。

## 6. 契约（pkg/contract/request.go）

- `RequestView` 增加 `InferredModelSource *int32 \`json:"inferredModelSource,omitempty"\``。
- `requestLike` 增加 `InferredModelSource pgtype.Int2`。
- `toRequestView` 中：`if r.InferredModelSource.Valid && r.InferredModelSource.Int16 != 0 { v := int32(...); view.InferredModelSource = &v }`（来源为 0 时省略）。
- 三个 `To*View`（`ToRequestView`、`ToListRequestRowView`、`ToListRequestsBySpanRowView`）透传新字段。

## 7. 再生 OpenAPI 与 TS 类型

- `mise run openapi`
- `pnpm --dir dashboard generate-openapi`

## 8. 前端展示（RequestDetailsContent.vue）

- 在「推测模型」`Field` 内、`inferredModel` 值之后，按 `inferredModelSource` 渲染一个 `Badge`（来自 `src/ui/`）：
  - `1` → 「思维链」
  - `2` → 「响应」
  - 其余/缺省 → 不渲染。
- 用一个本地小函数（如 `inferredModelSourceLabel`）做映射；`Badge` 标签为空时不渲染该 tag。

## 9. 验证

- `go build ./cmd/picotera`
- `pnpm --dir dashboard type-check`
- 手动确认：含思维签名的响应 → tag 显示「思维链」；仅有 `model` 字段 → 显示「响应」；无推测模型 → 无 tag。

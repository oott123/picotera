# Design

## 现状

### 列表页（`dashboard/src/views/RequestsView.vue`）

"状态"列（`cell-status`）按 `requestState(row)` 三态渲染：

- `pending`（status=0/1）→ 中性灰底 "..."。
- `ok`（status=2）→ 绿底 "成功"。
- `err`（status=3）→ 红底 `finishReasonLabel(row.finishReason)`（如"内部错误"）。

`requestState` 只看 `status`，不看 `statusCode`。筛选器有 provider/endpoint/model/upstreamModel/trace/project/time/emptyResponse，**无完成原因筛选**。

### 详情面板（`dashboard/src/components/RequestDetailsContent.vue`）

`Field label="停止原因"` 渲染 `finishReasonLabel(selected.finishReason)` 为 `Tag`，颜色由 `finishReasonVariant` 决定（reason=3 正常结束→`ok` 绿，其余→`default`）。

### 完成原因常量（`pkg/db/request_constants.go`）

```
1=FinishReasonInternal       内部错误
2=FinishReasonCancelled      已取消
3=FinishReasonEOF            正常结束
4=FinishReasonHeadersTimeout 请求头超时
5=FinishReasonReadTimeout    读取超时
6=FinishReasonStreamError    流式错误
7=FinishReasonDashboardCancelled 控制台打断
```

`finishReasonLabel`（`dashboard/src/utils/requestLabels.ts`）已把这 7 个值映射为中文文案，复用即可。

### 后端

`ListRequestsRequest`（`pkg/contract/request.go:352`）无 `finishReason` 参数。`ListRequests` SQL（`db/queries/request.sql`）无 `finish_reason` 过滤分支。`ListRequestsParams`（`pkg/db/request.sql.go:290`）无 `FinishReason` 字段。成功路径 `classifyStreamFinishReason` 默认返回 `FinishReasonEOF(3)`，所以 Completed 行的 `finish_reason` 恒有值；Pending 行 `finish_reason` 为 NULL。

## 方案

### 1. 列表列改名 + 直接显示完成原因

`RequestsView.vue`：

- 列 `header` 由"状态"改为"完成原因"（`columns` 里 `{ key: 'status', header: '完成原因' }`）。
- `cell-status` 渲染改为：
  - `requestState(row) === 'pending'` → 保留中性灰底 "处理中"（沿用现有 pending 样式，文案从 "..." 改为 "处理中"以与详情一致）。
  - 否则 → 用 `finishReasonLabel(row.finishReason)` 作文案，颜色按 `status`：status=2 绿底（`bg-ok-faint text-ok-ink`），status=3 红底（`bg-err-faint text-err-ink`）。
- `requestState` 逻辑不变（仍是 status-based pending/ok/err），只是 err 分支文案从"固定 finishReasonLabel"扩展为"所有非 pending 行都显示 finishReasonLabel"。

> 颜色仍按 status 而非 finishReason 取色：正常结束(status=2)绿、失败原因(status=3)红，保持列表可扫性，与现有 ok/err 视觉语义一致。不引入 `finishReasonVariant` 的 default 中性色到列表（列表需要强对比的二态信号）。

### 2. 完成原因筛选（列表头 ColumnFilter）

`RequestsView.vue` 增加 `filters.finishReason: number`（默认 0=不过滤），与 `filters.emptyResponse` 同模式（number + `:empty-value="0"`）。

- `#header-status` slot 放一个 `ColumnFilter`：
  - `v-model.number="filters.finishReason"`
  - `label="完成原因"`
  - `:empty-value="0"`
  - `:searchable="false"`（7 项固定，无需搜索）
  - `:options` 为 7 个完成原因的 `{ value, label }`，文案直接复用 `finishReasonLabel` 对应值（用一个小数组映射，不调函数以避免 `—` 兜底）。
- `requestFilters` computed 增加 `if (filters.finishReason) out.finishReason = filters.finishReason`。
- `filters` reactive 初始值加 `finishReason: 0`。
- 筛选变更 watch 数组加 `filters.finishReason`。
- `activeFilterCount` / `clearAllFilters` 加 `finishReason`。
- 列 `headerClass` 加完成原因激活下划线（与现有 provider/endpoint 一致）。

### 3. 后端：新增 finishReason 查询参数

#### `db/queries/request.sql` — `ListRequests`

在 `empty_response` 分支后、cursor 分支前加一行：

```sql
AND (sqlc.narg('finish_reason')::int IS NULL OR r.finish_reason = sqlc.narg('finish_reason')::int)
```

#### `pkg/contract/request.go` — `ListRequestsRequest`

加字段：

```go
FinishReason *int32 `query:"finishReason,omitempty"`
```

用 `*int32` 指针：0 不是合法完成原因值（常量从 1 开始），但 Huma 对 `int32` 零值会省略 `omitempty` 的判定需用指针区分"未传"与"传 0"。未传时 nil → SQL narg NULL → 不过滤。

#### `pkg/server/handle_requests.go` — `handleListRequests`

加：

```go
var filterFinishReason pgtype.Int4
if input.FinishReason != nil {
    filterFinishReason = pgtype.Int4{Int32: *input.FinishReason, Valid: true}
}
```

并加入 `ListRequestsParams` 调用：`FinishReason: filterFinishReason`。

#### sqlc 重新生成

`sqlc generate` 后 `ListRequestsParams` 增加 `FinishReason pgtype.Int4`，`ListRequests` query args 顺序对应新增。**sqlc 按 SQL 里 narg 出现顺序排参数**——`finish_reason` narg 位置在 `empty_response` 之后、`cursor_created_at` 之前，所以 `arg.FinishReason` 插在 `arg.EmptyResponse` 与 `arg.CursorCreatedAt` 之间。务必确认生成后 args 顺序与 SQL 一致。

#### OpenAPI 重新生成

`mise run openapi` 写 `openapi.yaml`，`pnpm --dir dashboard generate-openapi` 写 `dashboard/src/openapi-types.d.ts`，`RequestsFilters`（`dashboard/src/api/queryKeys.ts`）加 `finishReason?: number`。

### 4. 详情面板文案

`RequestDetailsContent.vue` 第 466 行 `Field label="停止原因"` → `label="完成原因"`。

## 不涉及

- 不动 `finishReasonLabel` / `finishReasonVariant` 函数本身。
- 不动 `requestState` 逻辑（仍 status-based）。
- 不新增 DB 列、不动 status 常量。
- 不把"处理中"作为筛选项（在途请求仍显示，但不参与完成原因筛选）。
- 不改详情面板完成原因的渲染方式（仍是 Tag + finishReasonVariant）。

# 设计：请求ID 筛选器

## 概述

在请求列表界面（`RequestsView.vue`）的「结束时间」右侧新增一个文本输入框「请求ID」。用户输入后，后端在 `request` 表的 4 个栏位上做精确匹配（OR），返回所有匹配行；该筛选器与其他筛选器 AND 叠加。

## 匹配语义

单个 `requestId` 参数对以下 4 列做精确等值匹配，OR 组合：

| 列 | 类型 | 现有索引 |
|---|---|---|
| `request.id` | text（xid，PK 主键） | 复合主键 `(id, created_at)` |
| `request.parent_span_id` | text | `request_parent_span_created_at_idx`（partial） |
| `request.external_request_id` | text | 无 |
| `request.external_response_id` | text | 无 |

与其他筛选器（`type`、`providerId`、`endpointPath`、`model`、`upstreamModel`、`traceId`、`projectId`、`startAt`/`endAt`、`emptyResponse`、`finishReason`）全部 AND 叠加，与现有 `traceId` 筛选器行为一致。

## 输入处理

- 空字符串 = 不筛选（narg 为 NULL），与现有文本筛选器（`model`、`endpointPath` 等）一致。
- **不做格式校验**。`requestId` 跨 4 种异构 ID 格式匹配（xid、span id、provider 专属字符串），无法用单一正则约束。任何非空字符串均透传到 SQL。这与 `traceId`（校验 xid 格式）不同，是刻意的——对 external ID 做严格校验会拒绝合法的 provider 专属字符串，违反「精确匹配」的要求。

## 从无到有自动切换

`external_request_id` / `external_response_id` 通常只存在于上游请求（type=1）上，而列表默认类型为 `meta`（type=0），会隐藏这些行。为让用户填入 ID 后能立即看到结果，在 `filters.requestId` 从空变为非空时（从无到有），前端自动：

1. 将 `filters.type` 设为 `'all'`；
2. 清空其他筛选器：`providerId`、`endpointPath`、`model`、`upstreamModel`、`traceId`、`projectId`、`startAt`、`endAt`、`emptyResponse`、`finishReason`。

触发条件严格限定为「空 → 非空」这一次性跃迁。非空 → 非空（继续编辑修正 ID）与非空 → 空（清空输入）均不触发，避免覆盖用户在已搜索状态下手动设置的筛选器。实现方式：用 `watch(() => filters.requestId, (next, prev) => { if (!prev && next) { ... } })` 捕获跃迁，并在其内直接改 `filters` 各字段（该 watch 与现有变更监听 watch 并存，重置后由后者负责重置分页与同步 URL）。

从 URL 恢复 `requestId`（如刷新页面、打开分享链接）时同样视为「从无到有」：初始 `filters.requestId` 由 `route.query.requestId` 置为非空，此时 `prev` 为初始的 `''`、`next` 为该非空值，触发同样的自动切换。这与手动输入语义一致，确保分享链接打开即处于「全部 + 无其他筛选」状态。

## 检索范围限制（最近 30 天）

`requestId` 搜索在无时间窗口时默认覆盖最近 30 天，避免对整个 hypertable 做无界扫描。**后端默认**：在 `handleListRequests` 解析 `parseTimeWindow` 之后，若 `input.RequestID != ""` 且 `startAt` 为空（`!startAt.Valid`），则填入 `now() - 30 天`；前端传入了 `startAt`（不论多远）则原样尊重，不钳制。`endAt` 始终不变。

这与「从无到有自动切换清空时间范围」配合：自动切换把 `startAt`/`endAt` 清空 → 请求带空 `startAt` 到达后端 → 后端填入 30 天前作为默认。用户随后手动设 `startAt`（非空→非空不触发自动切换，可设到 60 天前）→ 后端尊重前端值、不钳制，搜索范围由用户掌控。URL 只持久化 `requestId`（不带 `startAt`），刷新后 `startAt` 重新为空、后端再次填 30 天默认，不会因链接保存过久而 stale。

## 数据库

### SQL 查询（`db/queries/request.sql` → `ListRequests`）

在现有 WHERE 子句末尾（cursor 条件之前）追加：

```sql
AND (
  sqlc.narg('request_id')::text IS NULL
  OR r.id = sqlc.narg('request_id')::text
  OR r.parent_span_id = sqlc.narg('request_id')::text
  OR r.external_request_id = sqlc.narg('request_id')::text
  OR r.external_response_id = sqlc.narg('request_id')::text
)
```

`sqlc generate` 后 `ListRequestsParams` 新增 `RequestID pgtype.Text` 字段。

### 索引（新迁移 `043_request_external_ids_index.sql`）

`external_request_id` 与 `external_response_id` 当前无索引。虽然 `requestId` 搜索已被限定在最近 30 天（chunk exclusion 只扫近期 chunk），但在这些 chunk 内若无索引，OR 的两个 external_id 分支仍会触发 chunk 内全表扫描。加这两个 partial 索引后，BitmapOr 可合并全部 4 路索引扫描（`id` 走主键、`parent_span_id` 走现有 partial 索引、两个 external_id 走新索引）。partial 索引 `WHERE IS NOT NULL AND <> ''` 与现有 `request_parent_span_created_at_idx` 风格一致，跳过空值行以缩小每个 chunk 的索引体积。

```sql
-- +goose Up
CREATE INDEX request_external_request_id_idx
  ON request (external_request_id)
  WHERE external_request_id IS NOT NULL AND external_request_id <> '';
CREATE INDEX request_external_response_id_idx
  ON request (external_response_id)
  WHERE external_response_id IS NOT NULL AND external_response_id <> '';

-- +goose Down
DROP INDEX IF EXISTS request_external_response_id_idx;
DROP INDEX IF EXISTS request_external_request_id_idx;
```

## 后端

### 契约（`pkg/contract/request.go`）

`ListRequestsRequest` 新增字段：

```go
RequestID string `query:"requestId,omitempty"`
```

### Handler（`pkg/server/handle_requests.go` → `handleListRequests`）

新增局部变量并传入 `ListRequestsParams`，与现有 `filterTraceID` 同构（但无 `validateTraceID` 校验）：

```go
var filterRequestID pgtype.Text
if input.RequestID != "" {
    filterRequestID = pgtype.Text{String: input.RequestID, Valid: true}
}

startAt, endAt, err := parseTimeWindow(input.StartAt, input.EndAt)
if err != nil {
    return nil, err
}
// requestId 搜索：前端未传 startAt 时默认最近 30 天，传了则原样尊重。
if input.RequestID != "" && !startAt.Valid {
    startAt = pgtype.Timestamp{Time: time.Now().UTC().Add(-requestIDLookback), Valid: true}
}
```

`requestIDLookback` 为包级常量 `30 * 24 * time.Hour`。随后将 `RequestID: filterRequestID`、`StartAt: startAt`、`EndAt: endAt` 传入 `db.ListRequestsParams{}`。`parseTimeWindow` 的 start>end 校验在填默认值前完成；若前端传入的 `endAt` 早于 30 天前且 `startAt` 为空，填默认后 `startAt > endAt`，查询自然返回空结果（不会 400）——这是边界情形的可接受行为。

OpenAPI spec 需重新生成（`mise run openapi`），dashboard TS 类型需重新生成（`pnpm --dir dashboard generate-openapi`）。

## 前端

### `dashboard/src/api/queryKeys.ts`

`RequestsFilters` 新增 `requestId?: string`。

### `dashboard/src/views/RequestsView.vue`

1. `filters` reactive 新增 `requestId: ''`（含 route.query 初始化）。
2. `requestFilters` computed 新增 `if (filters.requestId) out.requestId = filters.requestId`。
3. 变更监听数组新增 `filters.requestId`。
4. `activeFilterCount` 新增 `if (filters.requestId) n++`。
5. `clearAllFilters` 新增 `filters.requestId = ''`。
6. `syncFiltersToQuery` 新增 `requestId` 的 set/delete（参照 `traceId`）。
7. 新增 route.query `requestId` → `filters.requestId` 的 watch（参照 `traceId` watch），用于外部导航/刷新恢复。当它将 `filters.requestId` 从空置为非空时，会触发第 8 项的自动切换 watch。
8. 新增 `watch(() => filters.requestId, (next, prev) => { if (!prev && next) { filters.type = 'all'; filters.providerId = 0; filters.endpointPath = ''; filters.model = ''; filters.upstreamModel = ''; filters.traceId = ''; filters.projectId = 0; filters.startAt = ''; filters.endAt = ''; filters.emptyResponse = 0; filters.finishReason = 0 } })`，捕获「从无到有」跃迁，在该回调内直接重置其他筛选器（随后由第 3 项的变更监听 watch 负责重置分页与同步 URL）。此 watch 必须在 route.query `requestId` → `filters.requestId` watch 之外独立存在，确保从 URL 恢复非空 `requestId` 时同样触发。
9. 模板：在 `<TimeRangeFilter />` 之后追加一个 `Field`，含 `type="text"` 输入框，`v-model="filters.requestId"`，placeholder「精确匹配 ID / span / 外部 ID」，样式与 TimeRangeFilter 内的 input 一致。输入框下方追加 `<span v-if="filters.requestId && !filters.startAt" class="text-2xs text-ink-faint">默认搜索最近 30 天，可设起始时间收窄</span>`：仅在 `requestId` 非空且前端未设 `startAt` 时显示，提示后端默认窗口；用户设了 `startAt` 后该提示消失（时间范围筛选器已显示用户设的窗口）。

`listRequests`（`client.ts`）无需改动——`api.GET` 的 `query: filters` 已透传所有 `RequestsFilters` 字段。

## 分页

沿用现有 `(created_at, id) DESC` 游标分页。按 `id` 精确匹配至多 1 行；按 `parent_span_id` / external ID 匹配多行时照常分页翻页。「找到所有满足的条目显示」由分页列表满足。

## 不做的事

- 不对「从无到有」之外的状态跃迁做自动切换（非空→非空、非空→空均不重置其他筛选器，见「从无到有自动切换」）。
- 不对 `requestId` 做格式校验（见「输入处理」）。
- 不引入兼容层、不改其他筛选器语义。

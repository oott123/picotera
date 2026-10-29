# 设计

## 背景

请求详情（`RequestDetailsContent.vue` 的「基本信息」区）当前展示两个字段：

- **Span** = `selected.spanId`
- **Parent Span** = `selected.parentSpanId`

「追踪」由 `traces` 表聚合，每行 `traces` 的主键 `id` 是网关生成的 xid（`UpsertTrace` 里 `xid.New().String()`），**不等于** `parent_span_id`。`/requests?traceId=X` 的后端过滤（`db/queries/request.sql` 的 `ListRequests`）按 `selected_trace.id = trace_id` 连接，再以 `r.parent_span_id = selected_trace.parent_span_id` 取同追踪请求。因此 URL 里的 `traceId` 必须是 `traces.id`（xid），用 `parentSpanId` 代会匹配不到任何行。

一个请求通过 `(parent_span_id, user_id)` 归属到一条追踪。

## 方案

把 `traces.id` 作为 `traceId` 暴露到 `RequestView`，由请求详情按「是否能匹配到追踪」决定是否渲染链接。不做单独的「按 parentSpanId 查 trace」接口，避免额外往返——追踪 id 随请求详情已经加载的 span 列表一起返回。

### 后端

在两个喂给请求详情的 sqlc 查询里加 `LEFT JOIN traces t ON t.parent_span_id = r.parent_span_id AND t.user_id = r.user_id`，选出 `t.id AS trace_id`：

- `GetRequest`（`GET /requests/{id}` 单条 + `listRequestSpans` 的 0 行回退路径）
- `ListRequestsBySpan`（请求详情的主路径，返回 meta + 所有 upstream span）

`LEFT JOIN` 命中不到时 `trace_id` 为 NULL，正好对应「能匹配到对应追踪」与否。所有同追踪的 span 共享同一 `parent_span_id`，故同一详情内各 span 的 `traceId` 一致。

`ListRequests`（请求列表）**不改**：列表不展示 `traceId`，给热路径加无条件 JOIN 是不必要的。`traceId` 是 `omitempty` 字段，列表行直接省略，与 `RequestView` 上其它按需填充的可选字段一致。

### 合同

`pkg/contract/request.go`：

- `RequestView` 增加 `TraceID string `json:"traceId,omitempty"``。
- 内部 `requestLike` 增加 `TraceID pgtype.Text`；`toRequestView` 在 `r.TraceID.Valid` 时填 `view.TraceID`。
- `ToRequestView` 的入参由 `*db.Request` 改为 `*db.GetRequestRow`（`GetRequest` 改成带 JOIN 的查询后 sqlc 生成的新行类型），并映射 `TraceID: r.TraceID`。字段名（`ID`/`SpanID`/…/`UserID`）不变，调用侧 `handleGetRequest`、`handleListRequestSpans` 的 `req.CreatedAt` 等访问保持有效。
- `ToListRequestsBySpanRowView` 映射 `TraceID: r.TraceID`。
- `ToListRequestRowView`（`ListRequests`）不设 `TraceID` → 省略。

`ownsRequestRow`（`handle_request_live.go`）用 `_, err = s.queries.GetRequest(...)` 丢弃返回值，行类型变更对其透明。

### 重新生成

`sqlc generate` → `mise run openapi` → `pnpm --dir dashboard generate-openapi`。后端只在已有响应类型上加字段，不新增操作，故无新 API 设计。

### 前端

`dashboard/src/components/RequestDetailsContent.vue`：

- 删除「基本信息」区里的 `Span` 与 `Parent Span` 两个 `Field`。
- 增加 `追踪` `Field`（`v-if="selected.traceId"`，`col-span-2`，与 `ID` 字段一致的全宽处理）：等宽显示 `selected.traceId`，后接一个 `RouterLink` 图标，`:to="{ name: 'requests', query: { traceId: selected.traceId } }"`，图标用 `route`（与侧边栏「追踪」导航同款 `IconRoute`）。
- 用 `RouterLink` 而非裸 `<a>`：左键走 SPA 内导航，中键/Cmd 点击原生新标签页打开。
- `selected.spanId` / `selected.parentSpanId` 数据字段保留——`isMeta`、span 排序等逻辑仍依赖它们，只是不再作为带标签字段展示。

## 非目标

- 不新增数据库列或迁移。
- 不改 `ListRequests` 查询或请求列表页。
- 不新增 API 操作；`traceId` 只随已有 `GET /requests/{id}`、`GET /requests/{id}/spans` 返回。
- 不做 `parentSpanId` 兼容别名；`traceId` 严格为 `traces.id`。

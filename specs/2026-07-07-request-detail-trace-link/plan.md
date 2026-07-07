# 执行计划

## 后端

1. `db/queries/request.sql`
   - `GetRequest`：改为
     ```sql
     -- name: GetRequest :one
     SELECT r.*, t.id AS trace_id
     FROM request r
     LEFT JOIN traces t ON t.parent_span_id = r.parent_span_id AND t.user_id = r.user_id
     WHERE r.id = $1
       AND r.created_at = sqlc.arg('id_created_at')::timestamp
       AND r.user_id = sqlc.arg('user_id')::bigint;
     ```
   - `ListRequestsBySpan`：列清单末尾加 `t.id AS trace_id`，`FROM request r, anchor` 改为 `FROM request r CROSS JOIN anchor`，并在其后加 `LEFT JOIN traces t ON t.parent_span_id = r.parent_span_id AND t.user_id = r.user_id`，`WHERE`/`ORDER BY` 不变。

2. `sqlc generate`（在仓库根目录）。确认 `pkg/db` 里 `GetRequestRow`、`ListRequestsBySpanRow` 各新增 `TraceID pgtype.Text`。

3. `pkg/contract/request.go`
   - `requestLike` 末尾加 `TraceID pgtype.Text`。
   - `RequestView` 加 `TraceID string `json:"traceId,omitempty"``（放在 `ParentSpanID` 之后）。
   - `toRequestView` 内（`r.UserID` 分支后）加：
     ```go
     if r.TraceID.Valid {
         view.TraceID = r.TraceID.String
     }
     ```
   - `ToRequestView`：签名改为 `func ToRequestView(r *db.GetRequestRow) *RequestView`，结构体字面量末尾加 `TraceID: r.TraceID,`。
   - `ToListRequestsBySpanRowView`：结构体字面量末尾加 `TraceID: r.TraceID,`。
   - `ToListRequestRowView` 不动（`ListRequestsRow` 无 `TraceID`）。

4. `pkg/server` 无需手改：`handleGetRequest`、`handleListRequestSpans` 对 `req` 的字段访问（`CreatedAt` 等）在新行类型上依旧有效；`ownsRequestRow` 用空标识符丢弃返回值。

5. 重新生成 OpenAPI 与前端类型：
   ```bash
   mise run openapi
   pnpm --dir dashboard generate-openapi
   ```

## 前端

6. `dashboard/src/components/RequestDetailsContent.vue`
   - `<script setup>` 顶部加 `import { RouterLink } from 'vue-router'`。
   - 删除「基本信息」grid 内的 `Span` 与 `Parent Span` 两个 `Field`（原 `v-if="selected.spanId"` 与 `v-if="selected.parentSpanId"` 两块）。
   - 在原位置（`完成原因` 之后、`用户消息` 之前）插入：
     ```vue
     <Field v-if="selected.traceId" label="追踪" as="div" class="col-span-2">
       <span class="inline-flex items-center gap-1.5 min-w-0">
         <span class="font-mono text-xs text-ink break-all">{{ selected.traceId }}</span>
         <RouterLink
           :to="{ name: 'requests', query: { traceId: selected.traceId } }"
           class="inline-flex items-center text-ink-faint hover:text-accent transition-colors shrink-0"
           :title="`查看追踪 ${selected.traceId}`"
         >
           <Icon name="route" :size="13" />
         </RouterLink>
       </span>
     </Field>
     ```

## 验证

7. `go build ./cmd/picotera`（确认 sqlc 生成代码与合同改动编译通过）。
8. `pnpm --dir dashboard type-check`（确认 `RequestView.traceId` 类型已生成且模板用法正确）。
9. `pnpm --dir dashboard lint`。
10. 目视：请求详情「基本信息」不再出现 Span / Parent Span；属于某追踪的请求显示「追踪」字段，id 旁的图标点击跳到 `/requests?traceId=<traceId>` 并只列出该追踪的请求；无追踪的请求不显示该字段。

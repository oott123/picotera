# 执行计划：请求ID 筛选器

## 阶段 1：数据库

1. **新建迁移 `db/migrations/043_request_external_ids_index.sql`**
   - Up：在 `request` 表上建 `request_external_request_id_idx`（partial btree on `external_request_id` WHERE IS NOT NULL AND <> ''）与 `request_external_response_id_idx`（同上，列为 `external_response_id`）。
   - Down：DROP 两个索引（先 response 后 request）。

2. **改 `db/queries/request.sql` 的 `ListRequests`**
   - 在 WHERE 子句的 `finishReason` 条件之后、cursor 条件之前，追加 `request_id` narg 的 OR 四列精确匹配块。
   - 不动 SELECT 列表（4 列已在现有 SELECT 中）。

3. **`sqlc generate`**
   - 重新生成 `pkg/db/`，确认 `ListRequestsParams` 新增 `RequestID pgtype.Text`，`ListRequestsRow` 不变。

## 阶段 2：后端

4. **改 `pkg/contract/request.go`**
   - `ListRequestsRequest` 新增 `RequestID string \`query:"requestId,omitempty"\``，放在 `TraceID` 字段之后。

5. **改 `pkg/server/handle_requests.go` 的 `handleListRequests`**
   - 在 `filterTraceID` 块之后新增 `filterRequestID pgtype.Text`（非空即 Valid），无校验。
   - 新增包级常量 `requestIDLookback = 30 * 24 * time.Hour`。
   - 在 `parseTimeWindow` 之后，若 `input.RequestID != ""` 且 `startAt` 为空（`!startAt.Valid`），填入 `now().UTC().Add(-requestIDLookback)` 作为默认；前端传入了 `startAt`（不论多远）则原样尊重、不钳制。`endAt` 不变。
   - 在 `s.queries.ListRequests` 调用的 `db.ListRequestsParams{}` 中加入 `RequestID: filterRequestID`（`StartAt`/`EndAt` 用上述结果）。

6. **重新生成 OpenAPI 与 TS 类型**
   - `mise run openapi` → 更新 `openapi.yaml`。
   - `pnpm --dir dashboard generate-openapi` → 更新 `dashboard/src/openapi-types.d.ts`。

## 阶段 3：前端

7. **改 `dashboard/src/api/queryKeys.ts`**
   - `RequestsFilters` 新增 `requestId?: string`。

8. **改 `dashboard/src/views/RequestsView.vue`**
   - `filters` reactive：新增 `requestId`，从 `route.query.requestId` 初始化（string 或 ''）。
   - `requestFilters` computed：返回类型新增 `requestId?: string`；新增 `if (filters.requestId) out.requestId = filters.requestId`。
   - 变更监听 watch 数组：追加 `filters.requestId`。
   - `activeFilterCount`：追加 `if (filters.requestId) n++`。
   - `clearAllFilters`：追加 `filters.requestId = ''`。
   - `syncFiltersToQuery`：参照 `traceId` 段，新增 `requestId` 的 `query.set`/`query.delete`，并把 `filters.requestId === currentRequestId` 纳入提前返回的相等性判断。
  - 新增 watch `route.query.requestId` → `filters.requestId`（参照 `traceId` watch），用于外部导航/刷新恢复。
  - 新增「从无到有」自动切换 watch：`watch(() => filters.requestId, (next, prev) => { if (!prev && next) { filters.type='all'; filters.providerId=0; filters.endpointPath=''; filters.model=''; filters.upstreamModel=''; filters.traceId=''; filters.projectId=0; filters.startAt=''; filters.endAt=''; filters.emptyResponse=0; filters.finishReason=0 } })`。仅捕获空→非空跃迁；非空→非空、非空→空不触发。该 watch 与 route.query watch 独立并存，确保从 URL 恢复非空 `requestId` 时同样触发自动切换。重置其他筛选器后由现有变更监听 watch 负责重置分页与同步 URL。
   - 模板：在 `<TimeRangeFilter ... />` 之后、闭合 `</div>`（filters 行）之前，插入：
     ```vue
     <Field label="请求ID">
       <input
         v-model="filters.requestId"
         type="text"
         placeholder="精确匹配"
         class="..."
       />
      <span v-if="filters.requestId && !filters.startAt" class="text-2xs text-ink-faint">默认搜索最近 30 天，可设起始时间收窄</span>
     </Field>
     ```
     class 与 TimeRangeFilter 内 input 一致（`rounded-md border border-line bg-surface-0 px-2 py-1.5 text-sm text-ink outline-none focus:border-accent focus-visible:ring-1 focus-visible:ring-accent`）。

## 阶段 4：验证

9. **后端编译**
   - `go build ./cmd/picotera`，确认无编译错误。

10. **后端测试**
    - 运行 `go test ./pkg/server/... ./pkg/llmbridge/...`，确认现有测试通过（本次为新增可选查询参数，不改既有路径）。
    - `handle_requests_test.go` 为纯单测、无 DB 依赖，新增参数默认空串 → `filterRequestID` 无效 → 行为不变，无需新增测试用例即可保证不回归。

11. **前端检查**
    - `pnpm --dir dashboard type-check`（vue-tsc）确认类型无错（`requestId` 已在 `RequestsFilters` 与 openapi-types 中）。
    - `pnpm --dir dashboard lint`（oxlint + eslint --fix）确认无 lint 错。

12. **冒烟验证（手动，若有运行环境）**
    - 启动 `docker compose up -d` + `mise run server`。
    - 在「请求ID」输入一个已知 request id → 列表过滤到该行（类型自动切到「全部」，其他筛选器清空）。
    - 输入一个已知 parent_span_id → 列表显示该 trace 的所有请求。
    - 输入一个已知 external_response_id → 命中对应上游请求（自动切到「全部」后可见）。
    - 在已有 requestId 状态下手动设渠道筛选器，再修改 requestId 内容（非空→非空）→ 渠道筛选器保留不被重置（验证仅「从无到有」触发自动切换）。
    - 清空「请求ID」输入 → 其他筛选器保持不变，列表按剩余筛选器显示（验证非空→空不触发自动切换）。
    - 先设好类型=meta + 渠道筛选，再在「请求ID」输入 → 类型自动切「全部」、渠道清空（验证从无到有重置）。
    - 刷新页面 → `requestId` 从 URL query 恢复，且自动切到「全部」、其他筛选器清空（验证 URL 恢复也触发自动切换）。
    - 不设起始时间、输入一个 35 天前的 request id → 列表无结果（验证后端默认 30 天窗口；URL 不含 startAt，刷新后仍默认 30 天）。
    - 不设起始时间、输入一个 10 天前的 request id → 列表命中该行（验证默认窗口内可见）。
    - 在 requestId 状态下手动把起始时间设到 7 天前（非空→非空不触发自动切换）→ 搜索范围收窄到 7 天，列表只显示该窗口内匹配。
    - 在 requestId 状态下手动把起始时间设到 60 天前 → 后端尊重前端值、不钳制，搜索范围扩到 60 天，列表显示该窗口内匹配（验证前端 startAt 优先于 30 天默认）。

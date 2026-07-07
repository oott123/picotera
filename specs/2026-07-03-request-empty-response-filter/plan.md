# Plan — 请求页面 Tokens 列「空回」筛选

## 1. Backend SQL

1. 编辑 `db/queries/request.sql` 的 `ListRequests`，在现有 `WHERE` 条件后追加 `empty_response` 段（见 design 中的 SQL）：缺省或 `false` 时 no-op，`true` 时要求 `output_tokens IS NULL OR = 0` 且端点为补全端点（统一路由路径列表 `OR EXISTS` join `endpoint` 表按 `endpoint_type` 集合）。查询注释标注补全类型常量来源 `pkg/contract/endpoint.go`、统一路由来源 `pkg/server/unified_routes.go`。
2. 运行 `sqlc generate`，确认 `pkg/db/request.sql.go` 的 `ListRequestsParams` 增加 `EmptyResponse pgtype.Bool`，`listRequests` SQL 包含新条件。

## 2. Backend contract and handler

1. `pkg/contract/request.go` 的 `ListRequestsRequest` 增加 `EmptyResponse bool \`query:"emptyResponse,omitempty"\``。
2. `pkg/server/handle_requests.go` 的 `handleListRequests` 构造 `db.ListRequestsParams` 时传入 `EmptyResponse: pgtype.Bool{Bool: input.EmptyResponse, Valid: true}`。

## 3. OpenAPI and generated dashboard types

1. 运行 `mise run openapi`，确认 `openapi.yaml` 的 `listRequests` 操作新增 `emptyResponse` query 参数。
2. 运行 `pnpm --dir dashboard generate-openapi`，确认 `dashboard/src/openapi-types.d.ts` 暴露该参数。

## 4. Dashboard types

1. `dashboard/src/api/queryKeys.ts` 的 `RequestsFilters` 类型增加 `emptyResponse?: boolean`。

## 5. RequestsView integration

1. `filters` reactive 增加 `emptyResponse: 0`（`0` / `1`，数字类型，匹配 `ColumnFilter` 的 `string | number` 泛型约束）。
2. `requestFilters` computed：`filters.emptyResponse` 为 `1` 时写入 `out.emptyResponse = true`（API 布尔参数）。
3. 现有 filter watcher 依赖数组加入 `filters.emptyResponse`。
4. `activeFilterCount()` 加入 `if (filters.emptyResponse) n++`；`clearAllFilters()` 加入 `filters.emptyResponse = 0`。
5. Tokens 列定义加 `headerClass`：`filters.emptyResponse` 为真时 `shadow-[inset_0_-2px_0_var(--color-accent)]`。
6. 模板增加 `#header-tokens` slot，放置 `ColumnFilter`：`v-model.number="filters.emptyResponse"`、`label="Token"`、`:options="[{ value: 1, label: '空回' }]"`、`:empty-value="0"`、`:searchable="false"`。

## 6. Verification

1. `go build ./...` 确认编译通过。
2. `go test ./pkg/server/...` 确认现有请求相关测试不回归。
3. `pnpm --dir dashboard type-check`。
4. `pnpm --dir dashboard lint`。
5. `pnpm --dir dashboard build`。
6. 手动验证（需运行环境）：
   - 请求页 Tokens 列表头出现 `全部`/`空回` 筛选。
   - 选「空回」后只显示补全端点且 `output_tokens` 为 0/NULL 的行。
   - 与「类型」筛选叠加（元/上游/全部）行为正确。
   - 切换筛选重置分页；清除按钮恢复「全部」。

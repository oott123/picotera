# 执行计划：管理员全局概览页面

## 一、后端 SQL

1. 新建 `db/queries/admin_overview.sql`，逐条镜像 `db/queries/overview.sql`，统一改造：
   - 删除 `AND user_id = sqlc.arg('user_id')::bigint`。
   - 新增 `AND (sqlc.narg('user_id')::bigint IS NULL OR user_id = sqlc.narg('user_id')::bigint)`。
   - 删除 `api_key_id` / `project_id` 过滤参数；保留 `model` / `upstream_model` / `provider_id`。
   - `CASE dimension` 改为 `user` / `model` / `upstreamModel` / `provider`。
   - traces 查询保留 EXISTS 内 `r.user_id = t.user_id` 关联，仅去外层 `t.user_id = $N`。
   - 查询名加 `Admin`：`GetAdminOverviewTotals`、`CountAdminTraces`、`CountAdminTracesFiltered`、`ListAdminOverviewDistribution`、`ListAdminOverviewDistributionCosts`、`ListAdminOverviewTraceCountsByDimension`、`ListAdminOverviewSeriesMetrics`、`ListAdminOverviewSeriesTraces`、`ListAdminOverviewCacheHitRateSeries`、`GetAdminOverviewTokenBreakdown`、`ListAdminOverviewBreakdownTokens`、`ListAdminOverviewBreakdownCosts`、`ListAdminOverviewSpeedSeries`、`GetAdminOverviewSpeedBoxplot`。
   - `ListAdminOverviewBreakdownTokens` / `...Costs` 的 GROUP BY 与 SELECT 改为 `user_id, model, upstream_model, provider_id`（替代 api_key_id / project_id）。
2. 运行 `sqlc generate`，确认 `pkg/db/` 生成新方法与 `Querier` 接口更新。

## 二、后端契约

3. 新建 `pkg/contract/admin_overview.go`：
   - `AdminOverviewCommonRequest`（range / userId / model / upstreamModel / providerId）。
   - `AdminOverviewBreakdownRowView`、`AdminOverviewSummaryView`。
   - 四组 Request/Response 类型，distribution / series / speed-boxplot 复用现有 `Overview*View`。
   - 四个 `huma.Operation`（路径 `/admin/overview/*`）。

## 三、后端 Handler 与注册

4. 新建 `pkg/server/admin_overview_breakdown.go`：`mergeAdminBreakdown`，按 `(userId, model, upstreamModel, providerId)` 合并。
5. 新建 `pkg/server/handle_admin_overview.go`：四个 handler，镜像 `handle_overview.go`，去掉 `requireUser`，改传可选 `userId`（新增 `toPgInt8` 辅助），复用现有辅助函数。
6. `pkg/server/server.go`：在 `admin` 分组上 `huma.Register` 四个 operation。
7. 运行 `mise run openapi` 重新生成 `openapi.yaml`。

## 四、前端数据层

8. `dashboard/src/api/queryKeys.ts`：新增 `AdminOverviewFilters` 类型与 `adminOverview` 键族（summary / distribution / series / speed / speedBoxplot / cacheHitRate）。
9. `dashboard/src/api/client.ts`：新增 `getAdminOverviewSummary/Distribution/Series/SpeedBoxplot` 与 `adminOverviewQuery` 参数拼装；复用既有 `listUsers`。
10. `pnpm --dir dashboard generate-openapi` 重新生成类型；在 `dashboard/src/api/index.ts` 重新导出 admin overview 视图类型与维度枚举别名。

## 五、前端视图与导航

11. 新建 `dashboard/src/views/AdminOverviewView.vue`（基于 `OverviewView.vue` 复刻）：
    - 过滤状态用 `userId` 替换 `apiKeyId`/`projectId`；保留 range / model / upstreamModel / providerId / 币种覆盖。
    - 用户下拉与 `userLabelById`（来自 `listUsers`）。
    - 维度选项改为 user / model / upstreamModel / provider（series/speed 含 none）。
    - Sankey 层级与构图改为以用户替代密钥/项目；数据源用 `AdminOverviewBreakdownRowView`。
    - 查询指向 admin fetcher。
12. `dashboard/src/router/index.ts`：新增 `adminOverview` 路由并加入 `ADMIN_ROUTES`。
13. `dashboard/src/components/AppSidebar.vue`：`adminNav` 顶部加入 `{ name: 'adminOverview', label: '全局概览', icon: 'chart-pie' }`。
14. `dashboard/src/App.vue`：`pageMeta` 加入 `adminOverview` 条目。

## 六、验证

15. `go build ./...` 通过。
16. `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`、`pnpm --dir dashboard build` 通过。
17. 手动验证（`mise run server` + `mise run web`）：管理员可见「全局概览」入口；跨用户聚合正确；用户筛选与用户维度生效；非管理员访问 `/admin/overview` 被重定向、API 返回 403；币种切换正常。

# 设计：管理员全局概览页面

## 背景

现有概览（`/overview`）完全按当前用户作用域：`pkg/server/handle_overview.go` 的每个 handler 先 `requireUser(ctx)`，并把 `u.ID` 作为强制过滤条件传给每条 SQL（`db/queries/overview.sql` 中每条查询都带 `AND user_id = sqlc.arg('user_id')::bigint`）。两张连续聚合 `request_overview_hourly`、`request_speed_hourly` 自 migration 036 起已在 GROUP BY 中携带 `user_id`，`traces` 表与 `request` 表也都带 `user_id`。

管理员全局概览需要：跨所有用户聚合、以「用户」筛选替代密钥/项目筛选、以「用户」维度替代密钥/项目维度。

## 总体方案

新建一套 **admin 专用端点**，挂在 `pkg/server/server.go` 中已有的 `admin` Huma 分组（`admin.UseMiddleware(s.requireAdmin)`）下，路径前缀 `/api/picotera/admin/overview`。这套端点：

- 不做 `requireUser` 用户作用域过滤——跨所有用户聚合；鉴权由 `requireAdmin` 中间件统一负责（非管理员 403）。
- 接受可选 `userId` 过滤（替代原 `apiKeyId` / `projectId`）。
- 保留 `model` / `upstreamModel` / `providerId` 过滤与 `range` 时间范围。
- 维度集合改为 `user` / `model` / `upstreamModel` / `provider`。

这一隔离方案的理由：现有用户查询把 `user_id = $N` 写死，若改成「管理员可关闭该过滤」的条件分支，会让共享查询同时承担两种作用域语义，存在非管理员越权读取全局数据的风险，也违反项目「不引入兼容/分支垫片、严格失败」的约定。新建独立端点 + 独立 SQL 隔离最干净。

币种换算复用前端既有的 `useCurrencyContext` 逻辑，后端按原始币种返回，不变。

## 后端

### SQL（新文件 `db/queries/admin_overview.sql`）

逐条镜像 `overview.sql`，差异统一为：

1. 去掉 `AND user_id = sqlc.arg('user_id')::bigint`。
2. 增加可选用户过滤 `AND (sqlc.narg('user_id')::bigint IS NULL OR user_id = sqlc.narg('user_id')::bigint)`。
3. 去掉 `apiKey` / `project` 过滤参数；保留 `model` / `upstreamModel` / `provider`。
4. `CASE dimension` 分支集合改为 `user`（`COALESCE(user_id::text,'')`）/ `model` / `upstreamModel` / `provider`，去掉 `apiKey` / `project`。

涉及查询：`GetAdminOverviewTotals`、`CountAdminTraces`、`CountAdminTracesFiltered`、`ListAdminOverviewDistribution`、`ListAdminOverviewDistributionCosts`、`ListAdminOverviewTraceCountsByDimension`、`ListAdminOverviewSeriesMetrics`、`ListAdminOverviewSeriesTraces`、`ListAdminOverviewCacheHitRateSeries`、`GetAdminOverviewTokenBreakdown`、`ListAdminOverviewBreakdownTokens`、`ListAdminOverviewBreakdownCosts`、`ListAdminOverviewSpeedSeries`、`GetAdminOverviewSpeedBoxplot`。

traces 相关查询：`CountAdminTracesFiltered`、`ListAdminOverviewTraceCountsByDimension`、`ListAdminOverviewSeriesTraces` 保留 EXISTS 子查询中 `r.user_id = t.user_id` 的关联（保证 parent_span_id 跨用户不串），仅去掉外层 `t.user_id = $N`。

无需新增 migration——两张连续聚合与 `traces` / `request` 表已带 `user_id`。

### 契约（新文件 `pkg/contract/admin_overview.go`）

- `AdminOverviewCommonRequest`：`range`（必填）、`userId`（可选，`minimum:1`）、`model` / `upstreamModel`（可选）、`providerId`（可选）。
- `AdminOverviewBreakdownRowView`：`userId` / `model` / `upstreamModel` / `providerId` / `totalTokens` / `costs`（用户维度替代密钥/项目，供 Sankey 构图）。
- `AdminOverviewSummaryView`：`window` / `totalTokens` / `totalRequests` / `totalTraceCount` / `costs` / `tokenBreakdown` / `breakdown`（`[]AdminOverviewBreakdownRowView`）。复用 `OverviewWindowView`、`OverviewCostView`、`OverviewTokenBreakdownView`。
- distribution / series / speed-boxplot 三个端点**直接复用** `OverviewDistributionView`、`OverviewSeriesView`、`OverviewSpeedBoxplotView`（它们都是 key/label/value 泛型结构，与维度无关）。
- 四个 `huma.Operation`，路径 `/admin/overview/{summary,distribution,series,speed-boxplot}`。

### Handler（新文件 `pkg/server/handle_admin_overview.go`）

镜像 `handle_overview.go` 四个 handler，差异：

- 不调用 `requireUser`；不传 `UserID` 强制参数，改传可选 `userId` narg。
- 复用既有辅助函数 `overviewWindow`、`overviewSeriesBucketInterval`、`overviewBuckets`、`overviewBucketAt`、`toPgInt4`、`toPgText`、`windowView`、`parseCostsJSON`、`emptyIfNil`。
- 新增 `mergeAdminBreakdown`（新文件 `pkg/server/admin_overview_breakdown.go`），按 `(userId, model, upstreamModel, providerId)` 合并 token/cost 行。
- summary 的 traces 计数沿用「无过滤走 `CountAdminTraces`、有过滤走 `CountAdminTracesFiltered`」逻辑，过滤判定改为 `userId/model/upstreamModel/providerId` 任一非空。

`userId` → `pgtype.Int8` 的可选转换需要一个 `toPgInt8(int32) pgtype.Int8` 小辅助（`0` 视为未设置）。

### 注册（`pkg/server/server.go`）

在 `admin` 分组上 `huma.Register` 四个新 operation。

## 前端

### 新视图 `dashboard/src/views/AdminOverviewView.vue`

以 `OverviewView.vue` 为蓝本复刻，差异：

- 过滤状态：用 `userId` 替换 `apiKeyId` / `projectId`；保留 `range` / `model` / `upstreamModel` / `providerId` / 币种覆盖。
- 用户下拉：复用 admin 既有 `listUsers()`（返回 `UserView[]`，含 `id` / `displayName`），构建 `userOptions`（含「全部」=0）与 `userLabelById` 映射，用于把 `user` 维度的 key（`user_id`）渲染为显示名。
- 维度选项：distribution 为 `user` / `model` / `upstreamModel` / `provider`；series / speed 为 `none` / `user` / `model` / `upstreamModel` / `provider`。
- Sankey：层级标签去掉密钥/项目，加入用户。`tokensIn` 等变体的层级改为 `总Token → 渠道 → 上游模型 → 请求模型 → 用户`（及对应反向 / 费用变体）；构图数据源改用 `AdminOverviewBreakdownRowView`（含 `userId`）。
- 数据查询指向新增的 admin client fetcher。

所有图表组件（`OverviewDonut` / `OverviewAreaStack` / `OverviewLineChart` / `OverviewSpeedTimeline` / `OverviewSankey`）原样复用。

### 数据层

- `dashboard/src/api/client.ts`：新增 `getAdminOverviewSummary` / `getAdminOverviewDistribution` / `getAdminOverviewSeries` / `getAdminOverviewSpeedBoxplot`，并新增对应的查询参数拼装（`adminOverviewQuery`，仅拼非默认值，`userId` 替代 `apiKeyId`/`projectId`）。
- `dashboard/src/api/queryKeys.ts`：新增 `adminOverview` 键族（`summary` / `distribution` / `series` / `speed` / `speedBoxplot` / `cacheHitRate`），并新增 `AdminOverviewFilters` 类型。
- `dashboard/src/api/index.ts`：重新导出 admin overview 视图类型（`AdminOverviewSummaryView`、`AdminOverviewBreakdownRowView` 等），以及 admin 维度枚举类型别名。
- 运行 `mise run openapi` + `pnpm --dir dashboard generate-openapi` 重新生成类型。

### 路由与导航

- `dashboard/src/router/index.ts`：新增路由 `{ path: '/admin/overview', name: 'adminOverview', component: AdminOverviewView }`，并把 `adminOverview` 加入 `ADMIN_ROUTES`。
- `dashboard/src/components/AppSidebar.vue`：在「全局」分组 `adminNav` 顶部加入 `{ name: 'adminOverview', label: '全局概览', icon: 'chart-pie' }`。
- `dashboard/src/App.vue`：`pageMeta` 加入 `adminOverview` 条目（title「全局概览」+ hint）。

## 不做的事

- 不新增 migration。
- 不改动现有用户概览端点 / 视图。
- 不为 admin 概览引入 `apiKey` / `project` 筛选或维度。
- 不引入兼容垫片；admin 与用户两条路径各自独立。

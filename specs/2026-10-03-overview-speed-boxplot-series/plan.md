# 执行计划

设计见 `design.md`，接口见 `api.md`。

## 1. 数据库

1. 新建 `db/migrations/052_drop_request_speed_bucketed.sql`（goose 格式，参照 `040_overview_caggs_10min.sql`）：
   - Up：`SELECT remove_continuous_aggregate_policy('request_speed_bucketed', if_exists => true);` + `DROP MATERIALIZED VIEW IF EXISTS request_speed_bucketed;`
   - Down：把 040 Up 中 `request_speed_bucketed` 的 `CREATE MATERIALIZED VIEW … WITH NO DATA`、`SET (timescaledb.materialized_only = false)`、`add_continuous_aggregate_policy`（35 days / 5 minutes / 5 minutes）原样搬过来。
   - 连续聚合不能在事务内创建或删除，文件首行与 040 一样写 `-- +goose NO TRANSACTION`。
2. `db/queries/overview.sql`：
   - 删除 `ListOverviewSpeedSeries`。
   - 新增 `ListOverviewSpeedDistributionSeries :many`，按 `design.md`「SQL：一次扫描出三项指标」编写，参数：`bucket_width`、`bucket_origin`、`dimension`、`start_at`、`end_at`、`user_id`，以及 narg `api_key_id`、`model`、`upstream_model`、`provider_id`、`project_id`。维度 CASE 与 `ListOverviewSeriesTraces` 相同（带 `r.` 前缀）。
3. `db/queries/admin_overview.sql`：
   - 删除 `ListAdminOverviewSpeedSeries`。
   - 新增 `ListAdminOverviewSpeedDistributionSeries :many`，作用域与维度对照 `ListAdminOverviewSeriesTraces`：narg `user_id`、`model`、`upstream_model`、`provider_id`，维度 `user` / `model` / `upstreamModel` / `provider`。
4. 运行 `sqlc generate`，确认 `pkg/db/` 中两个新查询的 Row 类型的 `MinValue` … `SampleCount` 都是非指针的 `float64` / `int64`，`Metric` 为 `string`，`BucketAt` 为 `pgtype.Timestamp`。

## 2. Contract

1. `pkg/contract/overview.go`：
   - 新增 `OverviewSpeedSeriesPointView`、`OverviewSpeedSeriesView`（字段见 `api.md`）。
   - 新增 `GetOverviewSpeedSeriesRequest`（`OverviewCommonRequest` + `Dimension` + `Bucket`，tag 与 `GetOverviewSeriesRequest` 相同）和 `GetOverviewSpeedSeriesResponse`。
   - 新增 `OperationGetOverviewSpeedSeries`（`GET /overview/speed-series`，OperationID `getOverviewSpeedSeries`）。
2. `pkg/contract/admin_overview.go`：
   - 新增 `GetAdminOverviewSpeedSeriesRequest`（`AdminOverviewCommonRequest` + `Dimension enum:"none,user,model,upstreamModel,provider"` + `Bucket`）和 `GetAdminOverviewSpeedSeriesResponse`（Body 为 `OverviewSpeedSeriesView`）。
   - 新增 `OperationGetAdminOverviewSpeedSeries`（`GET /admin/overview/speed-series`，OperationID `getAdminOverviewSpeedSeries`）。

## 3. 服务端

1. 新建 `pkg/server/handle_overview_speed_series.go`：
   - 定义 `overviewSpeedSeriesRow` 和 `buildOverviewSpeedSeries(rows, start, interval) (groupKeys []string, points []contract.OverviewSpeedSeriesPointView)`，行为见 `design.md`「Handler」；分组排序复用 `handle_overview_outcome.go` 的 `sortedKeys`，转视图复用 `outcomeGroupViews`。
   - `handleGetOverviewSpeedSeries`：`requireUser` → `resolveOverviewSeriesWindow`（错误 400）→ 调 `ListOverviewSpeedDistributionSeries`（`BucketWidth: overviewBucketWidthPG(bucketInterval)`、`BucketOrigin: startTS`，失败返回 500 `failed to query speed series`）→ 转行 → `buildOverviewSpeedSeries` → 组装 `OverviewSpeedSeriesView`（`buckets` 来自 `overviewBuckets`，`window.Bucket = overviewBucketLabel(bucketInterval)`）。
   - `handleGetAdminOverviewSpeedSeries`：同上，不调 `requireUser`，查询换成 admin 版，`UserID: toPgInt8(in.UserID)`。
2. `pkg/server/server.go` 的 `register()`：在 `OperationGetOverviewSpeedBoxplot` 旁注册 `OperationGetOverviewSpeedSeries`（mgmt），在 `OperationGetAdminOverviewSpeedBoxplot` 旁注册 `OperationGetAdminOverviewSpeedSeries`（admin）。
3. `pkg/server/handle_overview.go` 的 `handleGetOverviewSeries`：删除 `ListOverviewSpeedSeries` 调用、speed 相关注释与五个 `prefill*` / `decode*` map 及其累加循环、`prefillSpeed` / `decodeSpeed` / `avgTtft` 三段点输出。
4. `pkg/server/handle_admin_overview.go` 的 `handleGetAdminOverviewSeries`：同样删除。
5. 新增单元测试（放在 `pkg/server/handle_overview_speed_series_test.go`）覆盖 `buildOverviewSpeedSeries`：
   - 多个分组乱序输入 → `groupKeys` 升序且去重，三项指标共用。
   - `BucketAt` 被格式化为 RFC3339Nano，且与 `overviewBuckets` 生成的桶字符串逐字相等（用非整点 `start` 的自定义窗口验证 origin 对齐）。
   - 五数与 `Count` 原样带到点上，不出现补零的点。
   - 空输入 → 空 `groupKeys`、空 `points`（都是非 nil 的空切片，JSON 序列化为 `[]`）。
6. `go build ./...` 与 `go test ./pkg/server/...` 通过。

## 4. OpenAPI 与 TS 类型

1. `mise run openapi` 重新生成 `openapi.yaml`。
2. `pnpm --dir dashboard generate-openapi` 重新生成 `dashboard/src/openapi-types.d.ts`。
3. `dashboard/src/api/index.ts` 导出 `OverviewSpeedSeriesView`、`OverviewSpeedSeriesPointView`。

## 5. 前端 API 层

1. `dashboard/src/api/client.ts`：
   - 新增 `getOverviewSpeedSeries(filters, dimension, bucket)`，请求 `/api/picotera/overview/speed-series`，错误文案 `加载速度统计失败`。
   - 新增 `getAdminOverviewSpeedSeries(filters, dimension, bucket)`，请求 `/api/picotera/admin/overview/speed-series`，错误文案相同。
2. `dashboard/src/api/queryKeys.ts`：`overview.speed` / `adminOverview.speed` 保持原签名，无需修改。

## 6. 图表组件

1. 新建 `dashboard/src/components/charts/OverviewBoxplotSeries.vue`，按 `design.md`「新组件」实现。图例交互、坐标轴、`themeVersion`、`escape`、默认桶格式化函数照搬 `OverviewLineChart.vue`；箱体配色照搬 `OverviewSpeedTimeline.vue`（`groupColor` / `groupBorderColor`）。
2. `echarts.ts` 已注册 `BoxplotChart`，无需修改。

## 7. 视图

对 `dashboard/src/views/OverviewView.vue` 和 `dashboard/src/views/AdminOverviewView.vue` 做相同修改：

1. import `getOverviewSpeedSeries`（admin 页为 `getAdminOverviewSpeedSeries`）、`OverviewSpeedSeriesPointView` 类型与 `OverviewBoxplotSeries` 组件。
2. `speedSeriesQuery.queryFn` 改为调用新接口。
3. 删除 `seriesPrefillSpeed` / `seriesDecodeSpeed` / `seriesAvgTtft`；新增一个 `speedPoints(metric)` 辅助函数与 `speedPrefillPoints` / `speedDecodePoints` / `speedTtftPoints` 三个 computed，按 `metric` 过滤 `points` 并映射成组件的 `BoxplotPoint`。`speedGroups` / `speedBuckets` 改从新接口数据取（写法不变）。
4. 模板中三张卡片的 `OverviewLineChart` 换成 `OverviewBoxplotSeries`，`:points` 绑定新的三个 computed，`:value-format` 分别为 `formatSpeed`、`formatSpeed`、`formatTtft`，`:bucket-format="formatBucket"`；第三张卡片标题由「TTFT 平均时间」改为「TTFT」。
5. `OverviewSeriesPointView` 与 `OverviewLineChart` 仍被缓存命中率等其它卡片使用，保留这两个 import。
6. 横向「输出速度」卡片（`OverviewSpeedTimeline`）不动。

## 8. 文档

1. 根 `CLAUDE.md`：
   - mgmt 分组列表「overview ×5 (`summary`, `distribution`, `series`, `speed-boxplot`, `outcome-series`)」改为「overview ×6」并加入 `speed-series`。
   - 「All three continuous aggregates (`request_overview_bucketed`, `request_speed_bucketed`, `request_outcome_bucketed`) carry `user_id`」改为两个连续聚合。
   - 数据库章节「TimescaleDB continuous aggregates (`request_overview_bucketed`, `request_speed_bucketed`, `request_outcome_bucketed`, …)」去掉 `request_speed_bucketed`，补一句：速度统计（`speed-series`、`speed-boxplot`）直接扫 `request` 原始行算分位数，`request_speed_bucketed` 已由 migration 052 删除。同一段里「unlike the other two」等指代连续聚合个数的措辞随之修正。
2. `dashboard/CLAUDE.md` Charts 一节：`OverviewLineChart` 描述改为通用的多系列折线图（去掉 speed metrics）；新增 `OverviewBoxplotSeries` 条目：「vertical multi-series boxplot over time buckets（每个桶内各分组并排），used for the speed section (prefill/decode speed, TTFT)；配色与 `OverviewSpeedTimeline` 相同」。

## 9. 验证

1. `go build ./...`、`go test ./pkg/server/...`。
2. `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`。
3. `docker compose up -d` 后 `mise run server`，确认 migration 052 执行成功，`\d+ request_speed_bucketed` 不存在；再用 goose down 一步确认 Down 能重建该连续聚合，然后重新 up。
4. `mise run web` 打开用户总览和管理员总览：
   - 速度统计区三张卡片显示纵向盒须图，每个桶内各分组并排，颜色与图例一致，中位线可辨。
   - 切换粒度（自动 / 10m / 1h / 6h / 12h / 24h）和维度，桶数与上方趋势图一致；缺数据的桶不绘制箱体（确认 `'-'` 占位生效）。
   - 图例单击隐藏、右键单独显示正常；tooltip 显示桶标签与各分组 `min · med · p99`、`n=`。
   - TTFT 卡片标题为「TTFT」，纵轴与 tooltip 单位为 ms / s。
   - 横向「输出速度」卡片显示不变。
   - 亮色、暗色主题下各检查一次。

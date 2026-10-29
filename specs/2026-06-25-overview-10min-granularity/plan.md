# 执行计划

## 1. 迁移：重建连续聚合为 10 分钟桶并改名

新增 `db/migrations/040_overview_caggs_10min.sql`（`-- +goose NO TRANSACTION`）。

- Up：
  - `remove_continuous_aggregate_policy` + `DROP MATERIALIZED VIEW IF EXISTS` 旧 `request_overview_hourly`、`request_speed_hourly`。
  - 以 `time_bucket(INTERVAL '10 minutes', created_at)` 和迁移 036 中的**完整列集**创建 `request_overview_bucketed`、`request_speed_bucketed`（overview 含 `user_id`/`project_id`/`cost_currency` 等；speed 含 `prefill_request_count`/`user_id`/`project_id`）。
  - `SET (timescaledb.materialized_only = false)`，按现值重挂策略（`start_offset 35 days`、`end_offset 5 minutes`、`schedule_interval 5 minutes`）。
  - `CALL refresh_continuous_aggregate('request_overview_bucketed', NULL, NULL)`、`CALL refresh_continuous_aggregate('request_speed_bucketed', NULL, NULL)` 回填。
- Down：对称重建回 1 小时桶的 `_hourly` 两视图（列/策略同 036 的 Up）。

## 2. SQL 查询：改名引用 + 序列查询改造

`db/queries/overview.sql` 与 `db/queries/admin_overview.sql`（两文件对称改）：

- 全量把 `request_overview_hourly` → `request_overview_bucketed`、`request_speed_hourly` → `request_speed_bucketed`。
- `ListOverviewSpeedSeries` / `ListAdminOverviewSpeedSeries`：输出列由预算比值改为原始和——
  `SUM(prefill_token_sum)`、`SUM(prefill_time_sum)`、`SUM(prefill_request_count)`、`SUM(decode_token_sum)`、`SUM(decode_time_sum)`（均 `::float8`，count 为 `::bigint`）。保留 `HAVING SUM(prefill_time_sum) > 0 OR SUM(decode_time_sum) > 0`。
- `ListOverviewSeriesTraces` / `ListAdminOverviewSeriesTraces`：把 `time_bucket(INTERVAL '1 hour', r.created_at)` 改为 `time_bucket(sqlc.arg('bucket_width')::text::interval, r.created_at, sqlc.arg('bucket_origin')::timestamp)`。

执行 `sqlc generate`。

## 3. 后端 handler

`pkg/server/handle_overview.go`：

- `overviewSeriesBucketIntervalFor`：新增 `case "10m"` → 当 `rangeKey == "1m"` 返回错误，否则 `return 10 * time.Minute, nil`。
- 新增窗口对齐辅助：序列 handler 计算 `endAlign = min(bucketInterval, time.Hour)`，据此对齐 `end`（10m 桶按 10min 截断，≥1h 维持整点）；`overviewWindow` 增加可选对齐参数或由序列 handler 自算，汇总/分布维持整点。
- `handleGetOverviewSeries`：
  - 给 traces 查询传 `BucketWidth`（如 `"10 minutes"`/`"1 hour"`/…，对应 bucketInterval）与 `BucketOrigin = start`。
  - 速度聚合改为累加和：新增 `prefillTokenSumByBG`、`prefillTimeSumByBG`、`prefillReqCountByBG`、`decodeTokenSumByBG`、`decodeTimeSumByBG`，对 speedRows `+=`；在出点循环里计算 `prefillSpeed = tokenSum/(timeSum/1000)`、`decodeSpeed`、`avgTtft = prefillTimeSum/prefillReqCount`，分母 >0 才出点。
  - 缓存命中率改为累加和：新增 `cacheReadByBG`、`cacheInputByBG`，`+=`；出点时 `input>0` 才算 `read/input`。
- `windowView`：`Bucket` 字段填实际生效桶宽（接收 bucket key 参数）。

`pkg/server/handle_admin_overview.go`：对 `handleGetOverviewSeries` 的对称改动同样套用（速度累加、缓存累加、traces 传 width+origin、window bucket）。

## 4. Contract / OpenAPI

- `pkg/contract/overview.go`、`pkg/contract/admin_overview.go`：`GetOverviewSeriesRequest.Bucket` / admin 对应字段枚举改为 `auto,10m,1h,6h,12h,24h`。
- `mise run openapi` → `pnpm --dir dashboard generate-openapi`。

## 5. 前端

`dashboard/src/api/queryKeys.ts`：`OverviewGranularity` 并集加 `'10m'`。

`dashboard/src/views/OverviewView.vue` 与 `AdminOverviewView.vue`（对称）：

- `granularityOptions` 由 const 改 `computed`：基础含 `{value:'10m',label:'10m'}`；`range === '1m'` 时过滤掉 10m。`SegmentedControl :options` 绑定该 computed。
- `watch` range：变为 `'1m'` 且 `granularity === '10m'` 时，`granularity.value = 'auto'`。
- `formatBucket`：依据所选 `granularity` 判断格式——10m 显示 `HH:MM`（1d）/ `M/D HH:MM`（7d）；其余维持现有 `HH:00` / `M/D HH:00` / `M/D`。

## 6. 验证

- `go build ./...`、`go test ./pkg/server/...`（纯结构单测，验证 bucket 校验、速度/缓存累加辅助；DB 路径无端到端测试）。
- `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`。
- 手动：`docker compose up -d` 后跑迁移启动，1d/7d 选 10m 验证 144/1008 点曲线与 `HH:MM` 轴标；1m 下确认 10m 选项消失、由 10m 切 1m 自动回落自动；核对 6h/12h/24h 速度/缓存命中率较改造前更平滑（多小时聚合而非末小时值）。

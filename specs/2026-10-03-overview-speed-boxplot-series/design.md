# 设计：速度统计改为按粒度的纵向盒须图

## 现状

用户总览（`OverviewView.vue`）和管理员总览（`AdminOverviewView.vue`）的「速度统计」区各有四张卡片：

- 输入速度、输出速度、TTFT 平均时间：三张 `OverviewLineChart` 折线图，数据来自 `GET /overview/series`（admin 走 `/admin/overview/series`）的 `prefillSpeed` / `decodeSpeed` / `avgTtft` 三个指标。后端从 `request_speed_bucketed` 连续聚合读 `*_sum` / `*_count`，按显示桶折叠后相除，所以每个桶只有一个均值。
- 输出速度：一张 `OverviewSpeedTimeline` 横向盒须图，数据来自 `GET /overview/speed-boxplot`，直接扫 `request` 原始行，对**整个显示时间范围**算 `percentile_cont`。

连续聚合只存了求和与计数，算不出分位数，所以按桶的盒须图只能像 speed-boxplot 一样扫原始行。

## 方案概览

1. 后端新增 `GET /overview/speed-series` 和 `GET /admin/overview/speed-series`，一次查询返回三项指标在每个（显示桶，分组）上的五数统计。
2. 前端新增 `OverviewBoxplotSeries.vue` 纵向多系列盒须图组件，替换三张折线图。
3. 删除不再使用的旧数据源：series 接口的三个速度指标、`ListOverviewSpeedSeries` / `ListAdminOverviewSpeedSeries` 查询、`request_speed_bucketed` 连续聚合。
4. 横向「输出速度」盒须图（`speed-boxplot` 接口 + `OverviewSpeedTimeline`）保持不变。

## 统计口径

### 三项指标的样本集合

样本都取自 `request` 中 `type = 1`（上游行）且 `created_at` 落在窗口内的行，再叠加各维度筛选（与现有 series / speed-boxplot 相同的 `narg` 条件）。每项指标的入选条件和取值：

| metric | 入选条件 | 取值 | 来源 |
|---|---|---|---|
| `prefillSpeed` | `input_tokens >= 50 AND ttft_ms >= 500` | `input_tokens / (ttft_ms / 1000)`（tok/s） | 沿用 `request_speed_bucketed` 的 prefill 条件 |
| `ttft` | `input_tokens >= 50 AND ttft_ms >= 500` | `ttft_ms`（ms） | 沿用原 `avgTtft` 的分母集合（`prefill_request_count`） |
| `decodeSpeed` | `status_code = 200 AND finish_reason IN (2, 3, 5) AND output_tokens >= 50 AND ttft_ms IS NOT NULL AND time_spent_ms IS NOT NULL AND (time_spent_ms - ttft_ms) >= 500` | `output_tokens / ((time_spent_ms - ttft_ms) / 1000)`（tok/s） | 沿用现有 speed-boxplot 的条件 |

`decodeSpeed` 采用 speed-boxplot 的条件（比原折线图多了成功状态过滤），因此同一窗口内，按桶盒须图和横向盒须图的输出速度样本集合一致。

### 五数定义

与现有 speed-boxplot 完全一致，三项指标统一使用：

- `min` = `MIN(value)`
- `p25` = `percentile_cont(0.25)`
- `median` = `percentile_cont(0.5)`
- `p95` = `percentile_cont(0.95)`
- `max` = `GREATEST(percentile_cont(0.99), percentile_cont(0.5) * 3)`
- `count` = 样本数

ECharts boxplot 的 `value` 为 `[min, p25, median, p95, max]`。tooltip 沿用现有文案 `min · med · p99` 与 `n=`。

## 后端

### SQL：一次扫描出三项指标

`db/queries/overview.sql` 新增 `ListOverviewSpeedDistributionSeries`，`db/queries/admin_overview.sql` 新增对应的 `ListAdminOverviewSpeedDistributionSeries`（两者的差异只在作用域与维度，与其它 admin 查询一一对应）。结构：

```sql
WITH samples AS (
  SELECT
    time_bucket(sqlc.arg('bucket_width')::text::interval, r.created_at, sqlc.arg('bucket_origin')::timestamp)::timestamp AS bucket_at,
    CASE sqlc.arg('dimension')::text ... END AS group_key,
    s.metric,
    s.value
  FROM request r
  CROSS JOIN LATERAL (VALUES
    ('prefillSpeed', CASE WHEN <prefill 条件> THEN <prefill 取值> END),
    ('ttft',         CASE WHEN <prefill 条件> THEN r.ttft_ms::float8 END),
    ('decodeSpeed',  CASE WHEN <decode 条件>  THEN <decode 取值>  END)
  ) AS s(metric, value)
  WHERE r.type = 1
    AND r.created_at >= start_at AND r.created_at < end_at
    AND <作用域 + 维度筛选>
    AND ((<prefill 条件>) OR (<decode 条件>))
)
SELECT
  bucket_at,
  group_key,
  metric::text AS metric,
  MIN(value)::float8 AS min_value,
  percentile_cont(0.25) WITHIN GROUP (ORDER BY value)::float8 AS p25_value,
  percentile_cont(0.5)  WITHIN GROUP (ORDER BY value)::float8 AS median_value,
  percentile_cont(0.95) WITHIN GROUP (ORDER BY value)::float8 AS p95_value,
  GREATEST(
    percentile_cont(0.99) WITHIN GROUP (ORDER BY value),
    percentile_cont(0.5)  WITHIN GROUP (ORDER BY value) * 3
  )::float8 AS max_value,
  COUNT(*)::bigint AS sample_count
FROM samples
WHERE value IS NOT NULL
GROUP BY bucket_at, group_key, metric
ORDER BY bucket_at ASC, group_key ASC, metric ASC;
```

要点：

- `LATERAL VALUES` 把一行展开成三条（metric, value），不满足条件的取值为 `NULL`，外层 `WHERE value IS NOT NULL` 过滤掉。于是只扫一次 `request`，并且每个输出行都至少有一个样本，所有聚合列都非 `NULL`，sqlc 生成的都是非指针的 `float64` / `int64`。
- 外层 `WHERE` 里的 `(prefill 条件) OR (decode 条件)` 在展开前先排除三项都不入选的行。
- `time_bucket` 的宽度与 origin 用法与 `ListOverviewSeriesTraces` 相同（`overviewBucketWidthPG(bucketInterval)` + `bucket_origin = start`），SQL 直接产出显示桶。
- 用户版的作用域是 `r.user_id = sqlc.arg('user_id')::bigint`，筛选包含 `api_key_id` / `model` / `upstream_model` / `provider_id` / `project_id`，维度为 `apiKey` / `model` / `upstreamModel` / `provider` / `project`；admin 版作用域是可选的 `user_id` narg，筛选不含 `api_key_id` / `project_id`，维度为 `user` / `model` / `upstreamModel` / `provider`。

性能：扫描范围与现有 speed-boxplot 相同（窗口内的原始上游行），多出的开销只是按（桶，分组，指标）分组排序。同一组的几个 `percentile_cont` 共享排序状态。

### Handler

两套 handler 共用一个纯函数做组装：

```go
type overviewSpeedSeriesRow struct {
	BucketAt time.Time
	GroupKey string
	Metric   string
	Min, P25, Median, P95, Max float64
	Count    int64
}

func buildOverviewSpeedSeries(rows []overviewSpeedSeriesRow, start time.Time, interval time.Duration) (groupKeys []string, points []contract.OverviewSpeedSeriesPointView)
```

- 窗口与桶宽：`resolveOverviewSeriesWindow(in.Range, in.StartAt, in.EndAt, in.Bucket, time.Now())`，与 series / outcome-series 完全一致，因此三类图在同一粒度下桶对齐。
- `BucketAt` 用 `overviewBucketAt(start, row.BucketAt, interval)` 格式化成 RFC3339Nano，与 series handler 的格式化方式一致。
- 分组：取所有行出现过的 `GroupKey`，升序排列（`sortedKeys`），三项指标共用一份分组列表。没有任何行时返回空列表，不补 `""` 分组。
- `points` 每行原样转成一个点，不做插值或补零；没有样本的（桶，分组，指标）就是没有点。
- `buckets` 由 `overviewBuckets(start, end, interval)` 生成，`window.Bucket = overviewBucketLabel(interval)`。
- 两个 handler 各自把 sqlc 行类型转成 `overviewSpeedSeriesRow`（`BucketAt.Valid == false` 的行跳过，与现有 handler 一致），再调用 `buildOverviewSpeedSeries`。

### 删除的旧代码

- `db/queries/overview.sql` 的 `ListOverviewSpeedSeries`、`db/queries/admin_overview.sql` 的 `ListAdminOverviewSpeedSeries`。
- `handleGetOverviewSeries` / `handleGetAdminOverviewSeries` 中的速度查询、`prefill*` / `decode*` 累加 map，以及 `prefillSpeed` / `decodeSpeed` / `avgTtft` 三类点的输出。series 接口之后只输出 `tokens` / `requests` / `traces` / `cacheHitRate` / `cost`。
- 新 migration `052_drop_request_speed_bucketed.sql`：Up 先 `remove_continuous_aggregate_policy('request_speed_bucketed', if_exists => true)`，再 `DROP MATERIALIZED VIEW IF EXISTS request_speed_bucketed`；Down 按 `040_overview_caggs_10min.sql` Up 中的定义原样重建该连续聚合、`materialized_only = false` 以及刷新策略。

## 前端

### 新组件 `OverviewBoxplotSeries.vue`

位置 `dashboard/src/components/charts/OverviewBoxplotSeries.vue`，接口仿照 `OverviewLineChart`：

```ts
interface SeriesGroup { key: string; label: string }
interface BoxplotPoint {
  groupKey: string
  bucketAt: string
  min: number
  p25: number
  median: number
  p95: number
  max: number
  count: number
}
defineProps<{
  groups: SeriesGroup[]
  buckets: string[]
  points: BoxplotPoint[]
  height?: number
  valueFormat?: (value: number) => string
  bucketFormat?: (iso: string) => string
}>()
```

- 横轴 `category`（各桶，标签由 `bucketFormat` 生成），纵轴 `value`（标签由 `valueFormat` 生成），网格、坐标轴样式与 `OverviewLineChart` 相同。
- 每个可见分组一条 `type: 'boxplot'` 系列。ECharts 会在同一类目内把多条 boxplot 系列并排摆放，这就是「每个点纵向多系列」。系列数据按 `buckets` 顺序对齐，缺数据的桶填 `'-'`（ECharts 的空数据占位）。
- `boxWidth: [2, 24]`，桶多、分组多时箱体可以压到 2px，不会互相重叠。
- 颜色：`itemStyle.color = groupColor(originalIdx)`，`itemStyle.borderColor = groupBorderColor(originalIdx)`，`originalIdx` 是分组在 `groups` 中的下标，与图例色块一致；中位线靠边框色突出，做法与 `OverviewSpeedTimeline` 相同。
- 图例：与 `OverviewLineChart` 相同的 `Tag` 列表，单击切换显示，右键单独显示，状态逻辑（`hiddenKeys` / `toggleSeries` / `isolateSeries`）照搬。
- tooltip：`trigger: 'axis'`，头部为桶标签，下面每个有数据的分组一行：色块、分组名、`min · med · p99`（`p99` 位置显示 `max`，与现有文案一致）以及 `n=count`。
- 中位数折线：每个可见分组再配一条 `type: 'custom'` 系列，用单个占位数据在 `renderItem` 里画整条 `polyline`，经过该分组各桶的中位数，没有样本的桶直接跳过（折线跨过空桶相连）。横向位置用 `boxOffset` 复刻 ECharts `boxplotLayout` 的 `calculateBase` 公式，对准本分组箱体的中心；颜色取 `groupBorderColor`，与箱体中位线同色。折线系列 `silent`、`tooltip.show = false`，不参与 tooltip。
- 悬停聚焦：箱体和折线是两条系列，ECharts 的 `focus: 'series'` 会把被悬停分组自己的折线也淡化，所以改由组件维护 `focusedKey`（`mouseover` 箱体时设置，`mouseout` / `globalout` 时清空），非聚焦分组的箱体和折线透明度都降为 0.3。
- 默认高度 180px，与折线图相同。所有分组都没有点时只显示「暂无数据」，与 `OverviewSpeedTimeline` 相同。
- 主题切换沿用 `themeVersion` 触发 option 重算。

### 视图改动（两个总览页面相同）

- `speedSeriesQuery` 改为调用新的 `getOverviewSpeedSeries` / `getAdminOverviewSpeedSeries`（参数为筛选条件、`speedDimension`、`granularity`），queryKey 沿用 `queryKeys.overview.speed` / `queryKeys.adminOverview.speed`。
- 删除 `seriesPrefillSpeed` / `seriesDecodeSpeed` / `seriesAvgTtft`，改为按 `metric` 过滤新接口 `points` 的三个 computed；`speedGroups` / `speedBuckets` 从新接口的 `groups` / `buckets` 取。
- 三张卡片把 `OverviewLineChart` 换成 `OverviewBoxplotSeries`：输入速度、输出速度用 `formatSpeed`，第三张标题改为「TTFT」（不再是平均值），用 `formatTtft`。
- 横向「输出速度」卡片不变。

## 文档

- 根 `CLAUDE.md`：mgmt 分组的 overview 列表改为 ×6 并加入 `speed-series`；数据库章节里关于连续聚合的描述删去 `request_speed_bucketed`（「三个连续聚合」改为两个，并注明速度统计直接扫 `request` 原始行）。
- `dashboard/CLAUDE.md` Charts 一节：`OverviewLineChart` 的用途描述去掉「speed metrics」；新增 `OverviewBoxplotSeries` 条目。

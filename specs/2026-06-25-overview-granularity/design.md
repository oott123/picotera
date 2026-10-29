# 设计

## 背景

时间序列图表的数据来自 `getOverviewSeries` / `getAdminOverviewSeries` 端点。当前分桶逻辑：

- `overviewWindow(range)` 由时间范围决定**窗口** `[start, end)`，与本需求无关、保持不变。
- `overviewSeriesBucketInterval(range)` 由时间范围派生**序列桶大小**：`1d→1h`、`7d→4h`、`1m→8h`。
- 序列处理器从小时连续聚合（`request_overview_hourly` / `request_speed_hourly`）取数，在 Go 侧用 `overviewBucketAt(start, at, interval)` 相对窗口起点重新分桶。

因为 6h/12h/24h 都是 1h 的整数倍，且窗口起点已对齐到整点，重新分桶纯粹是把 `interval` 换成更大的值即可，**无需新建迁移、连续聚合或 SQL 查询**。

## 方案

引入一个显式的粒度参数 `bucket`，取值 `auto / 1h / 6h / 12h / 24h`，默认 `auto`。

- `auto`：沿用 `overviewSeriesBucketInterval(range)` 的现有派生逻辑。
- `1h / 6h / 12h / 24h`：直接使用对应固定间隔。

新增 `overviewSeriesBucketIntervalFor(rangeKey, bucketKey)`：当 `bucketKey == "auto"` 时委托给 `overviewSeriesBucketInterval(rangeKey)`，否则返回固定间隔。两个序列处理器（用户、admin）都改用它。

`bucket` 参数只挂在 series 请求类型上（`GetOverviewSeriesRequest` / `GetAdminOverviewSeriesRequest`），不进入 `OverviewCommonRequest`，因为汇总/分布/箱线图不分桶。

`OverviewWindowView.Bucket` 字段（当前恒为 `"hour"`）保持不变——前端未使用它，序列图表的桶格式化基于 `buckets.length`。

## 分桶对齐

继续沿用**相对窗口起点**的分桶（`overviewBucketAt` 现有行为），与现网的 4h/8h 一致。即 24h 粒度的桶边界是「now 减 N 天」而非自然日 0 点。这是已有行为的延续，本需求不改。

## 前端

- 两个概览页面新增一个 `granularity` 响应式状态，类型 `OverviewGranularity = 'auto' | '1h' | '6h' | '12h' | '24h'`，默认 `'auto'`，与 `range` 一样为页面级临时状态（不持久化到 preferences）。
- 控制栏在「时间范围」旁新增一个 `SegmentedControl`「统计粒度」，选项：自动 / 1h / 6h / 12h / 24h。
- 粒度作为独立参数（类比现有的 `dimension`）传入三个序列查询：`seriesQuery`、`speedSeriesQuery`、`cacheHitRateSeriesQuery`，并加入它们的 query key。`speedBoxplotQuery` 不传。
- 不把粒度并入 `OverviewFilters`，避免改变粒度时连带刷新汇总/分布/桑基图。

数据层（`queryKeys.ts` / `client.ts`）的 series / speed / cacheHitRate key 与 `getOverviewSeries` / `getAdminOverviewSeries` 增加 `bucket` 参数。

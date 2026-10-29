# 设计：概览粒度下探至 10 分钟

## 背景与现状

时间序列图表由四组查询供数：

- `ListOverviewSeriesMetrics`（tokens / requests / cost）— 读小时连续聚合 `request_overview_hourly`。
- `ListOverviewCacheHitRateSeries`（缓存命中率）— 读 `request_overview_hourly`。
- `ListOverviewSpeedSeries`（prefill/decode 速度、TTFT）— 读 `request_speed_hourly`。
- `ListOverviewSeriesTraces`（追踪数）— 直接读原始 `request` 表，硬编码 `time_bucket('1 hour')`（因 `COUNT(DISTINCT parent_span_id)` 不可由聚合预算）。

handler 用 `overviewBucketAt(start, bucketAt, interval)` 做 **Go 端二次分桶**：把 CA 行按目标粒度（6h/12h/24h）相对 `start` 折叠。`AdminOverviewView` 有一套完全对称的查询与 handler。

最小粒度被锁死在 1h，是因为 CA 桶宽为 1h——无法从中派生更细的桶。

## 核心决策：把 CA 桶宽降到 10 分钟

将 `request_overview_hourly` / `request_speed_hourly` 重建为 **10 分钟桶**。10min 整除 1h/6h/12h/24h，现有 Go 二次分桶逻辑即可从这同一数据源派生出全部粒度（10m 时为恒等映射，≥1h 时为折叠求和）。这是单数据源方案，不引入双路径或原始表查询。

两个视图同名改造：`request_overview_hourly` → `request_overview_bucketed`、`request_speed_hourly` → `request_speed_bucketed`（桶宽已成为实现细节，名字改为粒度中性）。

### 必须连带修复的正确性问题

降桶宽后，三个非加性指标的折叠方式必须修正，否则引入回归：

1. **速度（prefill/decode/TTFT）**：当前 `ListOverviewSpeedSeries` 在 SQL 里预先算好比值，handler 用 `prefillSpeedByBG[bg] = s.PrefillSpeed` **直接赋值**。CA 变 10min 后，1h 桶由 6 个 10min 行折叠，赋值只保留最后一行 → 回归。
   - 改法：查询改为返回**原始分子分母**（`prefill_token_sum`、`prefill_time_sum`、`prefill_request_count`、`decode_token_sum`、`decode_time_sum`）。handler 按 bucket-group **累加**这些和，最后统一做除法得出速度与 TTFT。
2. **缓存命中率**：handler 当前 `cacheHitRateByBG[bg] = read/input` **直接赋值**，同样的覆盖问题。
   - 改法：查询不变（已返回 `cache_read_token_sum`、`input_token_sum`）；handler 改为分别**累加** read 和、input 和，最后统一做除法。
3. **追踪数（COUNT DISTINCT，不可加）**：不能先按 10min 分桶再 Go 折叠（会重复计跨桶 trace）。
   - 改法：`ListOverviewSeriesTraces` 用 `time_bucket(<width>::interval, created_at, origin => <start>)` 在 SQL 里**直接按目标粒度**分桶，得到精确去重计数。`origin => start` 保证桶边界与 `overviewBuckets`（相对 start 走步）对齐。handler 仍对其调用 `overviewBucketAt`（此时为恒等，无害）。

上述 1、2 项同时修复了**现存的** 6h/12h/24h 速度/缓存命中率不准（当前这些粗粒度桶只显示窗口内最后一个小时的值）。这是有意的正确性改进。

非序列查询（totals、distribution、breakdown）按整窗 `SUM`/`GROUP`，与桶宽无关，重建后结果不变，仅需跟随视图改名更新引用。

### 窗口对齐

`overviewWindow` 当前把 `end` 截断到整点（`Truncate(1h).Add(1h)`），当前未完成的那个小时被排除。10m 粒度若仍按整点对齐，最近数据会滞后近 1 小时，失去细粒度意义。

改法：序列窗口的 `end` 对齐到 `min(bucketInterval, 1h)`——10m 桶按 10min 对齐（暴露最近数据），≥1h 桶维持整点对齐（不改动现有行为）。汇总/分布查询维持整点对齐。

### 粒度与范围校验

`overviewSeriesBucketIntervalFor(rangeKey, bucketKey)` 新增 `10m` 分支：返回 `10 * time.Minute`；当 `rangeKey == "1m"` 时返回错误，handler 转 400。该函数为两个 handler 共用，一处改动覆盖普通与全局概览。

## 迁移

新增一条 `-- +goose NO TRANSACTION` 迁移（沿用现有 CA 迁移范式）：

- 移除旧策略、`DROP MATERIALIZED VIEW` 旧的两个 `_hourly` 视图。
- 以 10 分钟桶、当前完整列集（含 `user_id`、`project_id`、speed 的 `prefill_request_count`）`CREATE MATERIALIZED VIEW` 新的两个 `_bucketed` 视图，`materialized_only = false`，重挂连续聚合策略（offset/schedule 维持现值）。
- `CALL refresh_continuous_aggregate(..., NULL, NULL)` 从 `request` 表回填历史。

Down 迁移对称重建回 `_hourly`（10 分钟桶 vs 1 小时桶仅 `time_bucket` 间隔不同，列与策略一致）。

历史数据无损：源始终是保留中的 `request` hypertable。代价是 CA 行数最多约 6 倍（按稀疏度通常远低于此），35 天保留期。

## 前端

`OverviewView` 与 `AdminOverviewView` 对称修改：

- `OverviewGranularity` 类型并集加 `'10m'`。
- 粒度选项改为 `computed`：基础列表含 10m；当 `range === '1m'` 时过滤掉 10m。
- `watch(range)`：切到 1m 且当前为 10m 时，把 `granularity` 重置为 `'auto'`。
- `formatBucket` 改为依据所选粒度而非桶数量判断格式：10m 时显示 `HH:MM`（1d）或 `M/D HH:MM`（7d），其余维持 `HH:00` / `M/D HH:00` / `M/D`。

UI 基础组件沿用本地 `SegmentedControl`（`:options` 接受 computed），不引入第三方库。

# Design: 概览页「成功率统计」

## 1. 口径定义

所有口径都以 `request` 行为单位，只统计**对话 / 补全类端点**（见 §1.3）。

### 1.1 成功

一行请求「成功」⟺ `finish_reason = 3`（`db.FinishReasonEOF`，UI 文案「正常结束」）**且** `COALESCE(output_tokens, 0) > 0`。

不额外判断 `status_code`：网关对任何非 200 上游响应都写 `finish_reason = 1`（`FinishReasonInternal`，见 `gateway_flow_attempts.go:handleUpstreamNonOK`），因此 `finish_reason = 3` 已经隐含 200。

- **上游成功率** = 成功的 upstream 行（`type = 1`）/ 全部 upstream 行
- **下游成功率** = 成功的 meta 行（`type = 0`）/ 全部 meta 行

分母包含**进行中**的行（`finish_reason IS NULL`，插入时为 NULL，终态由后续 UPDATE 写入）。这些行只会出现在窗口最后一两个桶里，占比可忽略；把它们排除会让「当前桶」的分母比实际请求数少，反而更容易误读。它们在「完成原因」图里作为独立类别「进行中」出现。

### 1.2 空回

一行请求「空回」⟺ `COALESCE(output_tokens, 0) = 0`。与请求列表 `emptyResponse` 筛选同一定义。「空回」图统计 upstream 行（`type = 1`）的空回占比。

### 1.3 端点范围

沿用 `ListRequests` 里 `emptyResponse` 筛选的口径：

```
endpoint_path ∈ {5 条 /api/unified 路由}
  OR EXISTS (SELECT 1 FROM endpoint e
             WHERE e.path = endpoint_path
               AND e.endpoint_type IN (2, 3, 4, 7, 8))
```

这段谓词目前在 `db/queries/request.sql` 里硬写了一份，本次还要在两条新查询里用到 —— 因此抽成数据库视图 `completion_endpoint_path(path)`，三处统一 `endpoint_path IN (SELECT path FROM completion_endpoint_path)`，`ListRequests` 一并换过去（不保留旧写法）。

`endpoint_path IS NULL` 的行不匹配任何分支，自然被排除。

**已知取舍**：用 `general`（type 1）端点代理的对话请求不计入统计 —— 它的 `endpoint_type` 不表达格式，无法判断是否该有输出 token。

## 2. 数据层：新连续聚合 `request_outcome_bucketed`

现有 `request_overview_bucketed` 不可用：它 `WHERE type = 1`（没有 meta 行），也没有 `finish_reason` / 输出 token 是否为 0 这两个维度。连续聚合的定义无法 ALTER，因此新建一个，而不是重建现有的。

**迁移 `045_request_outcome_cagg.sql`**（`-- +goose NO TRANSACTION`，与 040 一致）：

```sql
CREATE MATERIALIZED VIEW request_outcome_bucketed
WITH (timescaledb.continuous) AS
SELECT
  time_bucket(INTERVAL '10 minutes', created_at) AS bucket_at,
  type,
  endpoint_path,
  api_key_id,
  model,
  upstream_model,
  provider_id,
  project_id,
  user_id,
  COALESCE(finish_reason, 0)::int      AS finish_reason,
  (COALESCE(output_tokens, 0) = 0)     AS empty_response,
  COUNT(*)::bigint                     AS request_count
FROM request
GROUP BY bucket_at, type, endpoint_path, api_key_id, model, upstream_model,
         provider_id, project_id, user_id, finish_reason, empty_response
WITH NO DATA;
```

- 桶宽 10 分钟、`materialized_only = false`、刷新策略 `start_offset 35 days / end_offset 5 minutes / schedule 5 minutes` —— 全部对齐迁移 040 的两个聚合。终态字段由后续 UPDATE 写入，靠 TimescaleDB 的失效记录 + 策略重算覆盖，和 token / cost 现在的行为完全一致。
- `finish_reason` 取 `COALESCE(..., 0)`：库里实际取值只有 1..7，`0` 唯一地表示「进行中 / 未记录」。
- `empty_response` 与 `finish_reason` 作为 GROUP BY 键（而不是 `SUM(CASE ...)` 度量），这样一条查询就能同时喂四张图。基数上限是现有概览聚合的 8 × 2 倍，实际分布高度集中在 `(3, false)` 和 `(1, true)`。
- 不带 `WHERE type = 1` —— 上游与下游都要。
- 保留 `endpoint_path` 作为维度键（基数很小），端点范围在查询时过滤：`endpoint.endpoint_type` 可被改动、统一路由是代码常量，都不适合固化进聚合定义。

同一迁移里建视图：

```sql
CREATE VIEW completion_endpoint_path AS
SELECT path FROM endpoint WHERE endpoint_type IN (2, 3, 4, 7, 8)
UNION ALL
SELECT unnest(ARRAY[
  '/api/unified/v1/messages',
  '/api/unified/v1/responses',
  '/api/unified/v1/chat/completions',
  '/api/unified/v1beta/models/{model}:generateContent',
  '/api/unified/v1beta/models/{model}:streamGenerateContent'
]::text[]);
```

**运维说明**：新聚合 `WITH NO DATA` 建出，首次策略运行（≤ 5 分钟后）会回填 35 天历史 —— 一次性的批量物化，之后是增量。`materialized_only = false` 保证回填完成前查询也返回正确结果（实时聚合直接扫原表）。

## 3. 查询（`db/queries/overview.sql`）

两条新查询，都带完整的窗口 + `user_id` + 页面级筛选（apiKey / model / upstreamModel / provider / project），与现有概览查询逐字对齐。

- **`ListOverviewOutcomeSeries`** —— 一次扫描喂四张图。按 `(bucket_at, group_key, type, finish_reason, empty_response)` 分组返回 `SUM(request_count)`。`group_key` 用现有查询里那套 `CASE dimension` 表达式。
- **`GetOverviewUpstreamSuccessTotals`** —— 顶部卡片。`WHERE type = 1`，返回 `SUM(request_count) FILTER (WHERE finish_reason = 3 AND NOT empty_response)` 与 `SUM(request_count)` 两个数。

## 4. 服务端

新增 `GET /api/picotera/overview/outcome-series`（mgmt 组，用户维度隔离），细节见 `api.md`。理由是不往 `/overview/series` 上再挂查询：概览页对该端点会用三个不同维度各发一次请求，每次都已经跑 4 条 SQL，再加一条会让不相关的图变慢。

- 新文件 `pkg/server/handle_overview_outcome.go`。复用 `handle_overview.go` 的 `resolveOverviewSeriesWindow` / `overviewBucketAt` / `overviewBucketLabel` / `toPgInt4` / `toPgText` / `windowView`。
- 折叠逻辑抽成纯函数 `buildOutcomeSeries(rows, start, interval, bucketStrs, dimension) outcomeSeriesResult`，便于单测：**比率是非可加量**，先把 10 分钟源桶的分子 / 分母计数累加到展示桶，最后除一次 —— 与现有 speed / cacheHitRate 的处理方式相同。
- 分母为 0 的 (桶, 分组) **不产出点**，折线断开而不是画成 0%。
- `finishReasonShare` 仅在 `dimension = none` 时产出（`groupKey` 恒为 `""`）。分组维度下这张图不渲染，产出只会白白放大响应体（144 桶 × 30 分组 × 8 类别）。
- `handleGetOverviewSummary` 追加 `GetOverviewUpstreamSuccessTotals`，把 `upstreamSuccess` 塞进 `OverviewSummaryView`。

**不在范围内**：管理员全局概览（`/api/picotera/admin/overview/*`、`AdminOverviewView.vue`）。它是一套独立 SQL + 视图的平行副本；本次只改用户概览，需要的话后续按同一口径复制。

## 5. 前端（`OverviewView.vue`）

在「缓存命中率」区块之后新增区块，标题「成功率统计」，自带维度 `SegmentedControl`：全部 / 渠道 / 请求模型 / 上游模型（默认全部）。四张卡片放 `grid-cols-1 lg:grid-cols-2`：

| 卡片 | 图形 | 数据 | 可见性 |
| --- | --- | --- | --- |
| 上游成功率 | `OverviewLineChart` | `upstreamSuccessRate` | 始终 |
| 下游成功率 | `OverviewLineChart` | `downstreamSuccessRate` | 维度 ∈ {全部, 请求模型} 且未启用渠道 / 上游模型筛选，否则整卡隐藏 |
| 空回比例 | `OverviewLineChart` | `emptyResponseRate` | 始终 |
| 完成原因 | `OverviewAreaStack` | `finishReasonShare` | 维度 = 全部 |

- 折线的取值格式化用已有的 `formatPercent`，纵轴自动缩放（与「缓存命中率」一致，便于看出小幅下跌）。
- **下游成功率的不适用态**（实现期核实后修正）：meta 行的 `provider_id` / `upstream_model` **不是恒为 NULL** —— 它们在拿到上游响应头时由 header update 回填成最终服务该请求的那个渠道（path 路由 `gateway_flow_success.go:120`，unified `gateway_unified_helpers.go:343`）。但所有候选渠道都失败时 `failMeta`（`gateway_flow_errors.go:51`）只写 status / error / finish_reason / time_spent，不碰这两列，于是保持 NULL。

  后果比「查不到数据」更糟：按渠道 / 上游模型分组时**能**查到数据，但失败行全落进 `groupKey = ""`，各渠道的分母只剩成功行 —— 成功率虚高到接近 100%，`""` 组则是一条 0% 线。这是「看起来合理的错数字」，比空图更容易误导。因此按渠道 / 上游模型分组、或页面级这两个筛选生效时，**整张卡片直接不渲染**（`v-if="downstreamDimensionApplicable"`），不留占位文案 —— 与「完成原因」卡在非 `none` 维度下的处理一致。

  `model` 维度不受影响：meta 行的 `model` 在 `resolveAndRewriteModel` 里、**选渠道之前**就写入（`gateway_flow.go:349`，`rewriteModel` 改名后再写一次 `:408`），失败行同样有值。unified 路由复用同一个 `gatewayFlow`，行为一致。
- **完成原因图**：`OverviewAreaStack` 的 `groups` = 窗口内出现过的完成原因类别，顺序固定为 `正常结束 → 内部错误 → 已取消 → 请求头超时 → 读取超时 → 流式错误 → 控制台打断 → 进行中`（成功带落在堆叠底部）。标签复用 `utils/requestLabels.ts` 的 `finishReasonLabel`，`0` 由视图内的小包装映射成「进行中」（不改 `finishReasonLabel` —— `0` 在请求列表里是「无筛选」哨兵值）。
- `OverviewAreaStack` 加一个可选 `yMax?: number` prop，本图传 `1`，让纵轴稳定在 0–100%。
- 顶部卡片行由 `lg:grid-cols-4` 改成 `lg:grid-cols-5`，第五张卡「成功率」：大号百分比 + 下方一行灰色 `成功数 / 总数`；`total = 0` 时显示 `—`。
- 新查询接入 `overviewRefreshing` 与 `refreshOverview()`。

数据层照现有约定：`client.ts` 加 `getOverviewOutcomeSeries` fetcher、`queryKeys.overview.outcome(filters, dimension, bucket)`、`api/index.ts` 补类型导出。

## 6. 测试

`pkg/server/handle_overview_outcome_test.go`（纯函数、手搓 struct，与现有 `pkg/server` 测试风格一致）覆盖 `buildOutcomeSeries`：

- 多个 10 分钟源桶折叠进一个展示桶后，比率 = 合并后的分子 / 分母（不是各源桶比率的平均）
- 分母为 0 的桶不产出点
- 上游 / 下游按 `type` 正确分流
- 空回比例只看 `empty_response`，与 `finish_reason` 无关
- `finish_reason = 3` 但 `empty_response = true` 的行**不算**成功
- `finishReasonShare` 各类别之和 = 1；且仅在 `dimension = none` 时产出

第三方依赖：无新增。

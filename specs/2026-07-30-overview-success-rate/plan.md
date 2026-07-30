# Plan: 概览页「成功率统计」

前置阅读：`proposal.md`（需求 + 澄清）、`design.md`（口径与架构）、`api.md`（契约）。

## 步骤 1 —— 迁移：连续聚合 + 端点视图

新建 `db/migrations/045_request_outcome_cagg.sql`，首行 `-- +goose NO TRANSACTION`（连续聚合不能在事务里建，参照 `040_overview_caggs_10min.sql`）。

**Up**：

1. `CREATE MATERIALIZED VIEW request_outcome_bucketed WITH (timescaledb.continuous) AS ...`，SELECT / GROUP BY 见 `design.md` §2，`WITH NO DATA` 结尾。
2. `ALTER MATERIALIZED VIEW request_outcome_bucketed SET (timescaledb.materialized_only = false);`
3. `SELECT add_continuous_aggregate_policy('request_outcome_bucketed', start_offset => INTERVAL '35 days', end_offset => INTERVAL '5 minutes', schedule_interval => INTERVAL '5 minutes');`
4. `CREATE VIEW completion_endpoint_path AS ...`（`design.md` §1.3）。两个 UNION ALL 分支都显式写 `AS path`，让 sqlc 能拿到列名。

**Down**：`DROP VIEW IF EXISTS completion_endpoint_path;` → `SELECT remove_continuous_aggregate_policy('request_outcome_bucketed', if_exists => true);` → `DROP MATERIALIZED VIEW IF EXISTS request_outcome_bucketed;`

验证：`docker compose up -d` 后 `mise run server`，确认 goose 迁移无报错；`psql` 里 `SELECT count(*) FROM request_outcome_bucketed;` 能返回（实时聚合，回填前也应有结果）。

## 步骤 2 —— 查询

### 2a. `db/queries/overview.sql` 追加两条

**`ListOverviewOutcomeSeries :many`**

```sql
SELECT
  bucket_at::timestamp AS bucket_at,
  CASE sqlc.arg('dimension')::text
    WHEN 'apiKey' THEN COALESCE(api_key_id::text, '')
    WHEN 'model' THEN COALESCE(model, '')
    WHEN 'upstreamModel' THEN COALESCE(upstream_model, '')
    WHEN 'provider' THEN COALESCE(provider_id::text, '')
    WHEN 'project' THEN COALESCE(project_id::text, '')
    ELSE ''
  END AS group_key,
  type::int AS request_type,
  finish_reason::int AS finish_reason,
  empty_response::bool AS empty_response,
  SUM(request_count)::bigint AS request_count
FROM request_outcome_bucketed
WHERE bucket_at >= sqlc.arg('start_at')::timestamp
  AND bucket_at < sqlc.arg('end_at')::timestamp
  AND user_id = sqlc.arg('user_id')::bigint
  AND endpoint_path IN (SELECT path FROM completion_endpoint_path)
  AND (sqlc.narg('api_key_id')::int IS NULL OR api_key_id = sqlc.narg('api_key_id')::int)
  AND (sqlc.narg('model')::text IS NULL OR model = sqlc.narg('model')::text)
  AND (sqlc.narg('upstream_model')::text IS NULL OR upstream_model = sqlc.narg('upstream_model')::text)
  AND (sqlc.narg('provider_id')::int IS NULL OR provider_id = sqlc.narg('provider_id')::int)
  AND (sqlc.narg('project_id')::int IS NULL OR project_id = sqlc.narg('project_id')::int)
GROUP BY bucket_at, group_key, request_type, finish_reason, empty_response
ORDER BY bucket_at ASC, group_key ASC;
```

**`GetOverviewUpstreamSuccessTotals :one`** —— 同样的 WHERE（外加 `type = 1`），返回：

```sql
COALESCE(SUM(request_count) FILTER (
  WHERE finish_reason = 3 AND NOT empty_response
), 0)::bigint AS successful,
COALESCE(SUM(request_count), 0)::bigint AS total
```

`finish_reason = 3` 处加注释指向 `db.FinishReasonEOF`（与 `request.sql` 里 `<> 3` 的注释风格一致）。

### 2b. `ListRequests` 换用视图

把 `db/queries/request.sql` 里 `emptyResponse` 分支中那段 `endpoint_path = ANY(ARRAY[...]) OR EXISTS (...)` 整块替换成 `r.endpoint_path IN (SELECT path FROM completion_endpoint_path)`，保留说明注释（改成指向视图）。不留旧写法。

### 2c. 生成

```bash
sqlc generate
go build ./...
```

以上迁移 / 查询 / `ListRequests` 改写已在 planning 期间用 sqlc 在临时目录验证通过，生成结果为：

```go
type ListOverviewOutcomeSeriesRow struct {
    BucketAt      pgtype.Timestamp
    GroupKey      string
    RequestType   int32
    FinishReason  int32
    EmptyResponse bool
    RequestCount  int64
}
type GetOverviewUpstreamSuccessTotalsRow struct {
    Successful int64
    Total      int64
}
```

## 步骤 3 —— 契约

`pkg/contract/overview.go`：

1. 新增 `OverviewSuccessRateView`、`OverviewOutcomePointView`、`OverviewOutcomeSeriesView`、`GetOverviewOutcomeSeriesRequest`、`GetOverviewOutcomeSeriesResponse`（字段见 `api.md`）。
2. `OverviewSummaryView` 追加 `UpstreamSuccess OverviewSuccessRateView`。
3. 新增 `OperationGetOverviewOutcomeSeries`（`GET /overview/outcome-series`，OperationID `getOverviewOutcomeSeries`，Summary `Get request outcome rate series for a dimension`）。

`pkg/server/server.go`：在第 257 行 `OperationGetOverviewSpeedBoxplot` 之后注册到 **mgmt** 组。

## 步骤 4 —— 处理器

新建 `pkg/server/handle_overview_outcome.go`：

- `handleGetOverviewOutcomeSeries` —— `requireUser` → `resolveOverviewSeriesWindow` → 查询 → `buildOutcomeSeries` → 组装 `OverviewOutcomeSeriesView`，`window.Bucket = overviewBucketLabel(bucketInterval)`。
- 纯函数 `buildOutcomeSeries(rows []db.ListOverviewOutcomeSeriesRow, start time.Time, interval time.Duration, bucketStrs []string, dimension string)`，返回上下游分组、`finishReasons`、points。实现要点：
  - 每行按 `overviewBucketAt(start, r.BucketAt.Time, interval)` 折进展示桶；`!r.BucketAt.Valid` 跳过。
  - 按 `(bucket, group)` 分别累加：upstream 总数 / upstream 成功数 / upstream 空回数 / meta 总数 / meta 成功数；按 `(bucket, finishReason)` 累加 upstream 各完成原因数（仅 `dimension == "none"` 时需要）与桶内总数。
  - 成功判定：`r.FinishReason == 3 && !r.EmptyResponse`。
  - 分组集合按 `type` 分别收集并 `sort.Strings`；`dimension == "none"` 时两侧都退化成 `[""]`（对齐 `handleGetOverviewSeries` 里 `groupKeys = []string{""}` 的处理）。
  - 逐 (分组, 桶) 产出点，**分母为 0 则跳过**；`Count` / `Total` 一并填上。
  - `finishReasonShare` 仅在 `dimension == "none"` 时产出，`GroupKey = ""`，`Category = strconv.FormatInt(int64(code), 10)`，`Total` = 桶内 upstream 总数。`FinishReasons` 升序返回实际出现过的取值；其他维度返回空切片（非 nil）。

`handle_overview.go` 的 `handleGetOverviewSummary`：追加 `GetOverviewUpstreamSuccessTotals` 调用（参数与同函数内其他查询一致），失败返回 `huma.Error500InternalServerError("failed to query success totals", err)`；填充 `UpstreamSuccess`，`Total == 0` 时 `Rate = 0`。

## 步骤 5 —— 单测

新建 `pkg/server/handle_overview_outcome_test.go`，手搓 `[]db.ListOverviewOutcomeSeriesRow` 覆盖 `design.md` §6 的六个场景。

```bash
go test ./pkg/server/... ./pkg/llmbridge/...
```

## 步骤 6 —— OpenAPI + TS 类型

```bash
mise run openapi
pnpm --dir dashboard generate-openapi
```

确认 `openapi.yaml` 出现 `/overview/outcome-series` 与三个新 schema，且 `OverviewSummaryView` 带上 `upstreamSuccess`。

## 步骤 7 —— 前端数据层

- `dashboard/src/api/index.ts`：导出 `OverviewOutcomeSeriesView`、`OverviewOutcomePointView`、`OverviewSuccessRateView`、`OverviewOutcomeMetric`。
- `dashboard/src/api/queryKeys.ts`：`overview` 节点加 `outcome(f, dim, bucket)`。
- `dashboard/src/api/client.ts`：加 `getOverviewOutcomeSeries`（紧随 `getOverviewSeries`，沿用 `overviewQuery(filters)`）。

## 步骤 8 —— 图表组件

`dashboard/src/components/charts/OverviewAreaStack.vue`：`props` 加可选 `yMax?: number`，在 `option.yAxis` 上透传 `max: props.yMax`（未传时不设，保持现有行为）。

## 步骤 9 —— 概览视图

`dashboard/src/views/OverviewView.vue`：

1. `outcomeDimension = ref<OverviewSeriesDimension>('none')`；`outcomeDimensionOptions` = 全部 / 渠道 / 请求模型 / 上游模型。
2. `outcomeSeriesQuery`（`OPERATIONAL_STALE_TIME`，key 用 `queryKeys.overview.outcome`），接入 `overviewRefreshing` 与 `refreshOverview()`。
3. computed：
   - `outcomeUpstreamGroups` / `outcomeDownstreamGroups`（经 `dimensionLabel(outcomeDimension.value, key)`）。
   - `outcomeBuckets`。
   - `seriesUpstreamSuccessRate` / `seriesDownstreamSuccessRate` / `seriesEmptyResponseRate` —— 按 `metric` 过滤成 `{groupKey, bucketAt, value}`。
   - `finishReasonGroups` —— 由 `finishReasons` 按固定展示顺序（3,1,2,4,5,6,7,0）排出，标签走本地 `outcomeFinishReasonLabel`（`0` → 「进行中」，其余委托 `finishReasonLabel`）。
   - `seriesFinishReasonShare` —— `groupKey` 取 `category`，供 `OverviewAreaStack` 按类别堆叠。
   - `downstreamDimensionApplicable` —— `outcomeDimension ∈ {none, model} && !filters.providerId && !filters.upstreamModel`。
4. 模板：在「缓存命中率」区块之后加「成功率统计」区块（标题 + `SegmentedControl`），四张 `DataCard` 放 `grid-cols-1 lg:grid-cols-2`，loading / error 态照现有卡片写法。
   - 下游卡片：`downstreamDimensionApplicable` 为假时渲染 `StateText`「下游请求不含渠道 / 上游模型，该维度不适用」。
   - 完成原因卡片：`v-if="outcomeDimension === 'none'"`，用 `OverviewAreaStack` + `:y-max="1"` + `:value-format="formatPercent"`。
5. 顶部卡片行：`lg:grid-cols-4` → `lg:grid-cols-5`，追加「成功率」卡 —— `total > 0` 时大号 `formatPercent(rate)` + 下方 `text-2xs text-ink-faint mono tabular` 的 `成功数 / 总数`；`total === 0` 显示 `—`。

## 步骤 10 —— 校验

```bash
pnpm --dir dashboard type-check
pnpm --dir dashboard lint
pnpm --dir dashboard build
go build ./... && go test ./pkg/...
```

手工验收（`mise run server` + `mise run web`）：

- 顶部「成功率」卡与维度=全部时「上游成功率」折线的窗口均值一致。
- 维度切到渠道：上游成功率 / 空回比例出现多条线；完成原因图消失；下游成功率显示不适用文案。
- 页面级筛选选中某个渠道：下游成功率同样显示不适用文案（不是空图）。
- 「完成原因」图各带之和恒为 100%，含流量的桶纵轴顶到 100%。
- 粒度切 10m / 24h，比率随桶宽重算而不是简单平均（同一窗口下各桶比率的加权平均应等于总体成功率）。

## 步骤 11 —— 文档

更新根 `CLAUDE.md`：

- 「Database Schema」段落提及新连续聚合 `request_outcome_bucketed`（含 `type` / `finish_reason` / `empty_response` 维度，覆盖 meta + upstream）与视图 `completion_endpoint_path`。
- 「Unified generation routes」/「Key Patterns」不变；`mgmt` 组端点清单补上 `overview/outcome-series`（见「is_admin capability gate」段的 mgmt 列表）。

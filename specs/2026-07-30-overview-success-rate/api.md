# API: 概览成功率统计

## 1. 新增 `GET /api/picotera/overview/outcome-series`

Huma operation `getOverviewOutcomeSeries`，注册在 **mgmt** 组（全部已认证用户，强制 `user_id` 隔离）。

### 请求

复用 `OverviewCommonRequest`，再加两个参数 —— 与 `GET /overview/series` 完全同构：

| 参数 | 类型 | 约束 |
| --- | --- | --- |
| `range` | string | `1d,7d,1m,custom`，必填 |
| `startAt` / `endAt` | string | `range=custom` 时必填，严格 RFC3339Nano，`startAt < endAt` |
| `apiKeyId` | int32 | `minimum:1` |
| `model` | string | `minLength:1` |
| `upstreamModel` | string | `minLength:1` |
| `providerId` | int32 | `minimum:1` |
| `projectId` | int32 | `minimum:1` |
| `dimension` | string | `none,apiKey,model,upstreamModel,provider,project`，必填 |
| `bucket` | string | `auto,10m,1h,6h,12h,24h`，默认 `auto`；窗口跨度 > 7 天时 `10m` 报 400 |

```go
type GetOverviewOutcomeSeriesRequest struct {
    OverviewCommonRequest
    Dimension string `query:"dimension" enum:"none,apiKey,model,upstreamModel,provider,project" required:"true"`
    Bucket    string `query:"bucket,omitempty" enum:"auto,10m,1h,6h,12h,24h" default:"auto"`
}
```

### 响应

```go
type OverviewOutcomePointView struct {
    Metric   string  `json:"metric"`
    BucketAt string  `json:"bucketAt"`
    GroupKey string  `json:"groupKey"`
    Category string  `json:"category"`
    Value    float64 `json:"value"`
    Count    int64   `json:"count"`
    Total    int64   `json:"total"`
}

type OverviewOutcomeSeriesView struct {
    Window           OverviewWindowView        `json:"window"`
    Dimension        string                    `json:"dimension"`
    UpstreamGroups   []OverviewSeriesGroupView `json:"upstreamGroups"`
    DownstreamGroups []OverviewSeriesGroupView `json:"downstreamGroups"`
    FinishReasons    []int32                   `json:"finishReasons"`
    Buckets          []string                  `json:"buckets"`
    Points           []OverviewOutcomePointView `json:"points"`
}
```

- `window.bucket` 回填实际生效的桶宽标签（`10m` / `1h` / …），与 `/overview/series` 一致。
- `buckets` 是窗口内全部展示桶的 RFC3339Nano 列表（含无数据的桶）。
- `upstreamGroups` / `downstreamGroups` 分别是 upstream（`type=1`）与 meta（`type=0`）行里出现过的分组键，各自升序。`dimension=none` 时为单个 `key=""` 的分组。两侧分开返回：同一维度下两边出现的分组可能不同（meta 行的 `provider_id` / `upstream_model` 恒为 NULL，此时 `downstreamGroups` 只有 `""`）。
- `finishReasons` 是窗口内出现过的完成原因取值（升序，`0` = 进行中 / 未记录）。`dimension != none` 时为空数组。

### `metric` 取值

| metric | 行范围 | `value` | `count` / `total` | `groupKey` | `category` |
| --- | --- | --- | --- | --- | --- |
| `upstreamSuccessRate` | `type=1` | 成功占比 0..1 | 成功数 / 总数 | ∈ `upstreamGroups` | `""` |
| `downstreamSuccessRate` | `type=0` | 成功占比 0..1 | 成功数 / 总数 | ∈ `downstreamGroups` | `""` |
| `emptyResponseRate` | `type=1` | 空回占比 0..1 | 空回数 / 总数 | ∈ `upstreamGroups` | `""` |
| `finishReasonShare` | `type=1` | 该完成原因占比 0..1 | 该类别数 / 桶内总数 | `""` | 完成原因十进制字符串 `"0".."7"` |

规则：

- **分母为 0 的 (桶, 分组) 不产出任何点** —— 折线断开，而不是画成 0%。
- `finishReasonShare` **仅在 `dimension = none` 时产出**；同一 (桶) 下各 `category` 的 `value` 之和为 1。
- 只统计对话 / 补全类端点（`design.md` §1.3）。
- 成功 = `finish_reason = 3` 且 `output_tokens > 0`；空回 = `output_tokens = 0 或 NULL`；分母含进行中的行（`category = "0"`）。

### 错误

- `400` —— `range=custom` 缺少 / 非法的 `startAt`/`endAt`、非法 `bucket`、窗口 > 7 天时用 `10m`。
- `401` —— 未认证（chi 层 `auth.Middleware`）。
- `500` —— SQL 失败。

## 2. `GET /api/picotera/overview/summary` 扩展

`OverviewSummaryView` 追加一个字段（无 breaking change）：

```go
type OverviewSuccessRateView struct {
    Rate       float64 `json:"rate"`        // 0..1；total = 0 时为 0
    Successful int64   `json:"successful"`
    Total      int64   `json:"total"`
}

type OverviewSummaryView struct {
    // …既有字段…
    UpstreamSuccess OverviewSuccessRateView `json:"upstreamSuccess"`
}
```

口径与 `upstreamSuccessRate` 完全一致，只是覆盖整个窗口而非逐桶。前端在 `total = 0` 时显示 `—`。

## 3. 前端数据层

```ts
// src/api/index.ts
export type OverviewOutcomeSeriesView = components['schemas']['OverviewOutcomeSeriesView']
export type OverviewOutcomePointView  = components['schemas']['OverviewOutcomePointView']
export type OverviewSuccessRateView   = components['schemas']['OverviewSuccessRateView']
export type OverviewOutcomeMetric =
  | 'upstreamSuccessRate' | 'downstreamSuccessRate' | 'emptyResponseRate' | 'finishReasonShare'

// src/api/queryKeys.ts —— overview 节点内
outcome: (f: OverviewFilters, dim: OverviewSeriesDimension, bucket: OverviewGranularity) =>
  ['overview', 'outcome', dim, bucket, { ...f }] as const,

// src/api/client.ts
export async function getOverviewOutcomeSeries(
  filters: OverviewFilters,
  dimension: OverviewSeriesDimension,
  bucket: OverviewGranularity,
): Promise<OverviewOutcomeSeriesView>   // 失败文案「加载成功率统计失败」
```

# API：速度分布时间序列

## 新增

### `GET /api/picotera/overview/speed-series`

- OperationID：`getOverviewSpeedSeries`，注册在 `mgmt` 组。
- Summary：`Get decode / prefill speed and TTFT distribution series for a dimension`

查询参数（`GetOverviewSpeedSeriesRequest`）：

| 参数 | 说明 |
|---|---|
| `OverviewCommonRequest` 的全部字段 | `range`、`startAt`、`endAt`、`apiKeyId`、`model`、`upstreamModel`、`providerId`、`projectId`，校验与其它 overview 接口相同 |
| `dimension` | 必填，`enum:"none,apiKey,model,upstreamModel,provider,project"` |
| `bucket` | `enum:"auto,10m,1h,6h,12h,24h"`，默认 `auto`；规则与 `/overview/series` 相同（窗口超过 7 天时 `10m` 返回 400） |

### `GET /api/picotera/admin/overview/speed-series`

- OperationID：`getAdminOverviewSpeedSeries`，注册在 `admin` 组（非管理员 403）。
- Summary：`Get global decode / prefill speed and TTFT distribution series for a dimension (admin)`

查询参数（`GetAdminOverviewSpeedSeriesRequest`）：`AdminOverviewCommonRequest` 的全部字段，`dimension` 为 `enum:"none,user,model,upstreamModel,provider"`，`bucket` 同上。

### 响应体（两个接口共用 `OverviewSpeedSeriesView`）

```go
type OverviewSpeedSeriesPointView struct {
	// prefillSpeed (tok/s) | decodeSpeed (tok/s) | ttft (ms)
	Metric   string  `json:"metric"`
	BucketAt string  `json:"bucketAt"`
	GroupKey string  `json:"groupKey"`
	Min      float64 `json:"min"`
	P25      float64 `json:"p25"`
	Median   float64 `json:"median"`
	P95      float64 `json:"p95"`
	Max      float64 `json:"max"`
	Count    int64   `json:"count"`
}

type OverviewSpeedSeriesView struct {
	Window    OverviewWindowView             `json:"window"`
	Dimension string                         `json:"dimension"`
	Groups    []OverviewSeriesGroupView      `json:"groups"`
	Buckets   []string                       `json:"buckets"`
	Points    []OverviewSpeedSeriesPointView `json:"points"`
}
```

示例：

```json
{
  "window": { "range": "1d", "startAt": "2026-10-02T09:00:00Z", "endAt": "2026-10-03T09:00:00Z", "bucket": "1h" },
  "dimension": "model",
  "groups": [{ "key": "claude-sonnet-5-5", "label": "claude-sonnet-5-5" }],
  "buckets": ["2026-10-02T09:00:00Z", "2026-10-02T10:00:00Z"],
  "points": [
    { "metric": "decodeSpeed", "bucketAt": "2026-10-02T09:00:00Z", "groupKey": "claude-sonnet-5-5",
      "min": 31.2, "p25": 58.4, "median": 66.0, "p95": 81.7, "max": 198.0, "count": 42 },
    { "metric": "prefillSpeed", "bucketAt": "2026-10-02T09:00:00Z", "groupKey": "claude-sonnet-5-5",
      "min": 820.5, "p25": 2410.0, "median": 3902.3, "p95": 9120.8, "max": 11706.9, "count": 57 },
    { "metric": "ttft", "bucketAt": "2026-10-02T09:00:00Z", "groupKey": "claude-sonnet-5-5",
      "min": 512.0, "p25": 980.0, "median": 1530.0, "p95": 4210.0, "max": 6120.0, "count": 57 }
  ]
}
```

语义：

- `window.bucket` 为实际桶宽标签（`10m` / `1h` / …），与 `/overview/series` 相同。
- `groups` 按 key 升序，三项指标共用；没有任何样本时为 `[]`。`label` 与 `key` 相同，前端自行映射显示名。
- `points` 只包含有样本的（桶，分组，指标）组合，`count >= 1`；没有点即没有样本，不补零。
- 五数与样本口径见 `design.md`「统计口径」。

## 修改

### `GET /api/picotera/overview/series`、`GET /api/picotera/admin/overview/series`

`points[].metric` 不再出现 `prefillSpeed`、`decodeSpeed`、`avgTtft`，只剩 `tokens`、`requests`、`traces`、`cacheHitRate`、`cost`。参数和其余字段不变。

## 不变

`GET /api/picotera/overview/speed-boxplot`、`GET /api/picotera/admin/overview/speed-boxplot` 保持原样。

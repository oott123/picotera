# API：管理员全局概览

全部位于 `admin` Huma 分组下，前缀 `/api/picotera`，由 `requireAdmin` 中间件鉴权（非管理员返回 403）。

## 通用查询参数（`AdminOverviewCommonRequest`）

| 参数 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `range` | enum `1d` / `7d` / `1m` | 是 | 时间范围 |
| `userId` | int32（`minimum:1`） | 否 | 按用户过滤；缺省=全部用户 |
| `model` | string | 否 | 按请求模型过滤 |
| `upstreamModel` | string | 否 | 按上游模型过滤 |
| `providerId` | int32（`minimum:1`） | 否 | 按 Provider 过滤 |

无 `apiKeyId` / `projectId`。

## 端点

### GET /api/picotera/admin/overview/summary

- OperationID：`getAdminOverviewSummary`
- 请求：`AdminOverviewCommonRequest`
- 响应：`AdminOverviewSummaryView`

```
AdminOverviewSummaryView {
  window: OverviewWindowView
  totalTokens: int64
  totalRequests: int64
  totalTraceCount: int64
  costs: OverviewCostView[]
  tokenBreakdown: OverviewTokenBreakdownView
  breakdown: AdminOverviewBreakdownRowView[]
}

AdminOverviewBreakdownRowView {
  userId: int32
  model: string
  upstreamModel: string
  providerId: int32
  totalTokens: int64
  costs: OverviewCostView[]
}
```

（`OverviewWindowView` / `OverviewCostView` / `OverviewTokenBreakdownView` 复用现有定义。）

### GET /api/picotera/admin/overview/distribution

- OperationID：`getAdminOverviewDistribution`
- 请求：`AdminOverviewCommonRequest` + `dimension` enum `user` / `model` / `upstreamModel` / `provider`（必填）
- 响应：`OverviewDistributionView`（复用）。`rows[].key` 在 `dimension=user` 时为 `user_id` 字符串，前端用 `userLabelById` 渲染显示名。

### GET /api/picotera/admin/overview/series

- OperationID：`getAdminOverviewSeries`
- 请求：`AdminOverviewCommonRequest` + `dimension` enum `none` / `user` / `model` / `upstreamModel` / `provider`（必填）
- 响应：`OverviewSeriesView`（复用）。同时承载 tokens / requests / traces / prefillSpeed / decodeSpeed / avgTtft / cacheHitRate / cost 多 metric 点（与用户端一致）。

### GET /api/picotera/admin/overview/speed-boxplot

- OperationID：`getAdminOverviewSpeedBoxplot`
- 请求：`AdminOverviewCommonRequest` + `dimension` enum `none` / `user` / `model` / `upstreamModel` / `provider`（必填）
- 响应：`OverviewSpeedBoxplotView`（复用）。

## 用户列表（下拉与标签来源）

复用现有 admin 端点 `GET /api/picotera/users`（`listUsers` → `UserView[]`，含 `id` / `displayName`）。不新增标签端点。

# API — 概览页面自定义时间范围

## 受影响接口

所有概览与全局概览接口的 query 参数集变更，路径不变。

### 用户概览（`/api/picotera/overview/*`）

| 方法 | 路径 |
| --- | --- |
| GET | `/api/picotera/overview/summary` |
| GET | `/api/picotera/overview/distribution` |
| GET | `/api/picotera/overview/series` |
| GET | `/api/picotera/overview/speed-boxplot` |

### 全局概览（`/api/picotera/admin/overview/*`）

| 方法 | 路径 |
| --- | --- |
| GET | `/api/picotera/admin/overview/summary` |
| GET | `/api/picotera/admin/overview/distribution` |
| GET | `/api/picotera/admin/overview/series` |
| GET | `/api/picotera/admin/overview/speed-boxplot` |

## Query 参数变更

公共请求结构（`OverviewCommonRequest` / `AdminOverviewCommonRequest`）新增两个字段，并扩展 `range` 枚举：

```
range    string  enum:"1d,7d,1m,custom"  required
startAt  string  query:"startAt,omitempty"  RFC3339/RFC3339Nano
endAt    string  query:"endAt,omitempty"    RFC3339/RFC3339Nano
```

原有字段（`apiKeyId`/`model`/`upstreamModel`/`providerId`/`projectId` 或 `userId`）与 `series` 的 `dimension`/`bucket` 参数不变。

### 校验规则

- `range` 为预设值（`1d`/`7d`/`1m`）时：`startAt`/`endAt` 不参与计算；传入与否不影响结果。
- `range=custom` 时：`startAt` 与 `endAt` 均必填，否则返回 `400`。
  - 两者必须为合法 RFC3339 / RFC3339Nano（`time.Parse(time.RFC3339Nano, ...)`）。
  - `startAt` 必须严格早于 `endAt`，否则返回 `400`。
- `series` 的 `bucket=10m` 仅在窗口跨度 ≤ 7 天时允许；跨度更大时返回 `400`。
- `series` 的 `bucket=auto` 按窗口跨度选择：≤36h→1h，≤8d→4h，否则→8h。

## 响应

`OverviewWindowView` 不变：

```json
{
  "range": "custom",
  "startAt": "2026-07-01T00:00:00Z",
  "endAt": "2026-07-03T08:00:00Z",
  "bucket": "hour"
}
```

`range=custom` 时 `range` 字段返回 `"custom"`，`startAt`/`endAt` 为解析后的 UTC 时间戳。

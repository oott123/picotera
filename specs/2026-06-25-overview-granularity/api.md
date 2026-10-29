# API 变更

仅新增一个查询参数，无新增端点、无 schema 破坏性变更。

## `GET /api/picotera/overview/series`

新增查询参数：

| 参数 | 类型 | 必填 | 默认 | 取值 |
| --- | --- | --- | --- | --- |
| `bucket` | string | 否 | `auto` | `auto` `1h` `6h` `12h` `24h` |

`bucket=auto` 时按时间范围派生桶大小（1d→1h、7d→4h、1m→8h）；其余值为固定序列桶间隔。

## `GET /api/picotera/admin/overview/series`

同上，新增同样的 `bucket` 参数。

两个端点的响应体（`OverviewSeriesView`）结构不变。

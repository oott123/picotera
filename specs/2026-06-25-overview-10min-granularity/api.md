# API 变更

仅扩展现有序列端点的 `bucket` 查询参数枚举，无新端点、无新字段、无破坏性变更。

## `GET /api/picotera/overview/series`

`bucket` 参数枚举新增 `10m`：

```
bucket  query  enum: auto,10m,1h,6h,12h,24h   default: auto
```

- `bucket=10m` 且 `range=1m` → `400 Bad Request`。
- `bucket=10m` 且 `range` 为 `1d` 或 `7d` → 返回 10 分钟粒度序列。

## `GET /api/picotera/admin/overview/series`

同上，`bucket` 枚举新增 `10m`，校验规则一致。

## 响应

响应结构（`OverviewSeriesView`：`window` / `dimension` / `groups` / `buckets` / `points`）不变。`buckets` 在 10m 粒度下为 10 分钟步长的时间戳序列；`points` 的 `metric`、`groupKey`、`currency` 语义不变。

`window.bucket` 字段回填**实际生效的桶宽**（如 `10m`、`1h`），不再恒为 `hour`。

## OpenAPI 同步

改动 `pkg/contract/overview.go`、`pkg/contract/admin_overview.go` 的枚举后，依次执行：

1. `mise run openapi` —— 重新生成 `openapi.yaml`。
2. `pnpm --dir dashboard generate-openapi` —— 重新生成 `dashboard/src/openapi-types.d.ts`。

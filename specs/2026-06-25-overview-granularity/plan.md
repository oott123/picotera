# 执行计划

## 1. 后端 contract

`pkg/contract/overview.go`

- 给 `GetOverviewSeriesRequest` 增加字段：
  `Bucket string `query:"bucket,omitempty" enum:"auto,1h,6h,12h,24h" default:"auto"``

`pkg/contract/admin_overview.go`

- 给 `GetAdminOverviewSeriesRequest` 增加同样的 `Bucket` 字段。

## 2. 后端处理器

`pkg/server/handle_overview.go`

- 新增辅助函数：
  ```go
  func overviewSeriesBucketIntervalFor(rangeKey, bucketKey string) (time.Duration, error) {
      switch bucketKey {
      case "", "auto":
          return overviewSeriesBucketInterval(rangeKey)
      case "1h":
          return time.Hour, nil
      case "6h":
          return 6 * time.Hour, nil
      case "12h":
          return 12 * time.Hour, nil
      case "24h":
          return 24 * time.Hour, nil
      default:
          return 0, fmt.Errorf("invalid bucket %q", bucketKey)
      }
  }
  ```
- `handleGetOverviewSeries`：把 `overviewSeriesBucketInterval(in.Range)` 改为 `overviewSeriesBucketIntervalFor(in.Range, in.Bucket)`。

`pkg/server/handle_admin_overview.go`

- `handleGetAdminOverviewSeries`（约 212 行）：同样改为 `overviewSeriesBucketIntervalFor(in.Range, in.Bucket)`。

## 3. 重新生成 OpenAPI 与 TS 类型

- `mise run openapi`
- `pnpm --dir dashboard generate-openapi`

## 4. 前端数据层

`dashboard/src/api/queryKeys.ts`

- 导出 `export type OverviewGranularity = 'auto' | '1h' | '6h' | '12h' | '24h'`。
- `overview.series` / `overview.speed` / `overview.cacheHitRate` 与
  `adminOverview.series` / `adminOverview.speed` / `adminOverview.cacheHitRate`
  各增加一个 `bucket: OverviewGranularity` 形参，并加入返回的 key 元组。

`dashboard/src/api/client.ts`

- `getOverviewSeries` / `getAdminOverviewSeries` 增加 `bucket: OverviewGranularity` 形参，
  作为 `query.bucket` 传入（`auto` 也照常传，后端默认即 `auto`，无害）。

## 5. 前端视图

`dashboard/src/views/OverviewView.vue` 与 `dashboard/src/views/AdminOverviewView.vue`

- 新增 `const granularity = ref<OverviewGranularity>('auto')`。
- 新增选项常量：
  ```ts
  const granularityOptions: { value: OverviewGranularity; label: string }[] = [
    { value: 'auto', label: '自动' },
    { value: '1h', label: '1h' },
    { value: '6h', label: '6h' },
    { value: '12h', label: '12h' },
    { value: '24h', label: '24h' },
  ]
  ```
- 控制栏在「时间范围」`SegmentedControl` 后新增「统计粒度」`SegmentedControl`，绑定 `granularity`。
- `seriesQuery` / `speedSeriesQuery` / `cacheHitRateSeriesQuery`：
  query key 与 `queryFn` 都传入 `granularity.value`。`speedBoxplotQuery` 不传。

## 6. 校验

- `pnpm --dir dashboard type-check`
- `pnpm --dir dashboard lint`
- `go build ./...`
- 手动验证：切换粒度时三个序列图表重新分桶刷新，汇总/分布/桑基/箱线图不刷新；`auto` 行为与改动前一致。

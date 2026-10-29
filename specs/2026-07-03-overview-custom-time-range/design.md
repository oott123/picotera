# Design — 概览页面自定义时间范围

## 概述

为概览与全局概览接口增加 `custom` 时间范围。选中 `custom` 时，由客户端传入 `startAt`/`endAt`（RFC3339）界定分析窗口；预设范围（`1d`/`7d`/`1m`）沿用现有「以当前时刻为终点、向前回看固定时长」的逻辑。

后端将窗口解析与桶宽选择改造为基于「窗口跨度」的统一逻辑：预设范围的跨度由 rangeKey 推导，自定义范围的跨度由 `endAt - startAt` 推导。这样自动桶宽与 `10m` 桶的可用性判断对两者一致。

## 后端设计

### 窗口解析

新增统一窗口解析函数（替换现有 `overviewWindow` / `overviewWindowAligned` 的调用点）：

```go
// overviewWindow 解析分析窗口 [start, end)。
// 预设范围：end 对齐到 align（不小于 1h），start = end - lookback。
// 自定义范围：直接使用 startAt/endAt，不做对齐（与请求/追踪页一致）。
func overviewWindow(rangeKey, startAt, endAt string, now time.Time, align time.Duration) (start, end time.Time, err error)
```

- 预设范围：保留现有 `overviewWindowAligned` 逻辑（改名为 `overviewPresetWindow`，行为不变）。
- 自定义范围：调用 `parseOverviewCustomWindow(startAt, endAt)`：
  - `startAt`、`endAt` 均必填，缺一返回错误。
  - 用 `time.Parse(time.RFC3339Nano, ...)` 严格解析，不做 trim / 时区猜测 / 格式补全。
  - 校验 `start.Before(end)`（start 必须严格早于 end），否则返回错误。
  - 解析结果转 UTC 后返回。

### 桶宽选择

将 `overviewSeriesBucketInterval` 与 `overviewSeriesBucketIntervalFor` 改为基于「窗口跨度」而非 rangeKey：

```go
// overviewAutoBucket 按窗口跨度选择自动桶宽，与预设默认值一致：
// span <= 36h -> 1h；span <= 8d -> 4h；否则 -> 8h。
func overviewAutoBucket(span time.Duration) time.Duration

// overviewSeriesBucketIntervalFor 按 bucketKey 选择桶宽。
// 10m 仅在 span <= 7d 时允许（覆盖原 1m 预设的禁用规则）。
func overviewSeriesBucketIntervalFor(bucketKey string, span time.Duration) (time.Duration, error)
```

- 预设跨度：`1d`→24h、`7d`→168h、`1m`→720h，与现有回看时长一致。
- 自动桶：`overviewAutoBucket(span)` 在预设跨度下产出 1h/4h/8h，与现有行为完全相同。
- `10m` 桶：`span > 168h` 时拒绝（`1m` 预设跨度 720h 被拒；`1d`/`7d` 与自定义 ≤7d 允许）。
- `1h`/`6h`/`12h`/`24h` 始终允许。

趋势（series）处理器调整调用顺序：先解析窗口得到 `start`/`end`，再以 `span = end.Sub(start)` 计算桶宽。对预设范围，仍先用 `presetSpan(rangeKey)` 计算桶宽以决定 `align`（保留 10m 桶的子小时对齐行为）；对自定义范围，`align` 无意义（用户给定精确边界，`bucket_origin = start`）。

封装为一个 helper 避免分支重复：

```go
// resolveOverviewSeriesWindow 一次性解析趋势处理器的窗口与桶宽。
func resolveOverviewSeriesWindow(rangeKey, startAt, endAt, bucketKey string, now time.Time) (start, end time.Time, bucketInterval time.Duration, err error)
```

### 契约变更

`pkg/contract/overview.go` 的 `OverviewCommonRequest` 与 `pkg/contract/admin_overview.go` 的 `AdminOverviewCommonRequest`：

- `Range` 枚举由 `1d,7d,1m` 改为 `1d,7d,1m,custom`。
- 新增 `StartAt string \`query:"startAt,omitempty"\`` 与 `EndAt string \`query:"endAt,omitempty"\``。

`range=custom` 时 `StartAt`/`EndAt` 必填，由处理器校验并返回 400。

### 处理器变更

概览与全局概览共 8 个处理器（summary、distribution、series、speed-boxplot ×2）将 `overviewWindow(in.Range, time.Now())` / `overviewWindowAligned(...)` 调用替换为新 `overviewWindow(in.Range, in.StartAt, in.EndAt, time.Now(), time.Hour)`，series 处理器改用 `resolveOverviewSeriesWindow(...)`。`windowView(in.Range, start, end)` 调用不变（`rangeKey="custom"` 时 Range 字段返回 `"custom"`）。

### 数据范围限制

连续聚合 `request_overview_bucketed` / `request_speed_bucketed` 的 `materialized_only = false`，`start_offset = 35 days`。超过 35 天的自定义窗口会实时回查 `request` 超表，功能可用但较慢。不强制设置上限。

## 前端设计

### 类型与数据层

- `dashboard/src/api/index.ts`：`OverviewRange` 改为 `'1d' | '7d' | '1m' | 'custom'`。
- `dashboard/src/api/queryKeys.ts`：`OverviewFilters` 与 `AdminOverviewFilters` 的 `range` 类型更新，新增可选 `startAt?: string`、`endAt?: string`。
- `dashboard/src/api/client.ts`：`overviewQuery` 与 `adminOverviewQuery` 在 `range === 'custom'` 时附带 `startAt`/`endAt`。

### 概览页面（`OverviewView.vue` / `AdminOverviewView.vue`）

两个页面做对称改动：

1. `filters` reactive 新增 `startAt: ''`、`endAt: ''`。
2. `rangeOptions` 增加 `{ value: 'custom', label: '自定义' }`。
3. 控件栏在时间范围分段控件后，当 `filters.range === 'custom'` 时渲染 `TimeRangeFilter`（复用现有组件，传入 `{ startAt, endAt }`），与请求/追踪页一致。
4. `overviewFilters` / `adminOverviewFilters` 计算属性在 `range === 'custom'` 时附带 `startAt`/`endAt`；预设范围不带。
5. 统计粒度选项：`10m` 仅在窗口跨度 ≤ 7 天时可选。自定义范围下由 `startAt`/`endAt` 计算跨度；未填齐两端时先允许（避免选时间过程中选项闪烁）。增加 watch：当前为 `10m` 且跨度超限（含切到 `1m` 预设）时回落到 `auto`。
6. 切换到预设范围时不清空 `startAt`/`endAt`（保留用户已选值，便于切回自定义），但不发送给后端。

`TimeRangeFilter` 组件本身无需改动——它已提供 `datetime-local` 输入、RFC3339↔本地时间转换与「开始晚于结束」校验。

## 生成产物

实现后运行：

```bash
mise run openapi
pnpm --dir dashboard generate-openapi
```

`openapi.yaml` 与 `dashboard/src/openapi-types.d.ts` 必须随接口变化更新。

## 依赖

不新增依赖。`TimeRangeFilter` 已存在并已在请求/追踪页使用。

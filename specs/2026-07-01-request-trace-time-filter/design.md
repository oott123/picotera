# Design — 请求与追踪页面时间筛选器

## Scope

本改动为请求列表和追踪列表增加同一套时间范围筛选能力。筛选器支持可选的开始时间和结束时间，两个端点均为闭区间。

- 请求列表按 `request.created_at` 筛选。
- 追踪列表按 `traces.last_request_at` 筛选，列表仍展示整条追踪的汇总数据。
- 筛选条件通过 URL query 持久化为 `startAt` 和 `endAt`，便于分页、刷新和分享链接。

## Backend Design

### Query Parameters

`GET /api/picotera/requests` 和 `GET /api/picotera/request-traces` 增加两个可选 query 参数：

- `startAt`: RFC3339/RFC3339Nano 时间戳，包含时区偏移或 `Z`。
- `endAt`: RFC3339/RFC3339Nano 时间戳，包含时区偏移或 `Z`。

服务端严格解析这两个参数：

- 空字符串表示未设置。
- 非空值必须被 `time.Parse(time.RFC3339Nano, value)` 接受。
- 不进行 trim、大小写修正、时区猜测或格式补全。
- 同时设置时，`startAt` 必须早于或等于 `endAt`。
- 服务端将解析结果转换为 UTC 后写入 `pgtype.Timestamp`，与数据库中的 UTC `timestamp` 字段比较。

### Request List Filtering

`ListRequests` 的 SQL 在现有用户隔离、类型、渠道、端点、模型、追踪和项目过滤基础上增加：

```sql
AND (sqlc.narg('start_at')::timestamp IS NULL OR r.created_at >= sqlc.narg('start_at')::timestamp)
AND (sqlc.narg('end_at')::timestamp IS NULL OR r.created_at <= sqlc.narg('end_at')::timestamp)
```

分页排序保持 `ORDER BY r.created_at DESC, r.id DESC`。游标仍由请求 id 编码，并通过 id 还原 `created_at`，无需改变 cursor 格式。筛选变化时前端重置 cursor。

### Trace List Filtering

`ListRequestTraces` 的 SQL 在 `traces.user_id` 和 cursor 条件基础上增加：

```sql
AND (sqlc.narg('start_at')::timestamp IS NULL OR traces.last_request_at >= sqlc.narg('start_at')::timestamp)
AND (sqlc.narg('end_at')::timestamp IS NULL OR traces.last_request_at <= sqlc.narg('end_at')::timestamp)
```

追踪页的时间范围定义为“最近请求时间”范围，因为页面按 `last_request_at` 倒序排序，并且已有索引 `traces_user_id_idx (user_id, last_request_at DESC, id DESC)` 支持该访问模式。

追踪行的指标、成本、预览和项目仍基于整条追踪的请求窗口计算。时间筛选只决定追踪是否出现在列表中，不裁剪追踪汇总。

### Validation Helper

在 `pkg/server/handle_requests.go` 增加复用 helper：`parseTimeWindow(startAt, endAt string) (start pgtype.Timestamp, end pgtype.Timestamp, err error)`。

该 helper 返回 Huma 400 错误：

- `invalid startAt`：`startAt` 非空且不是 RFC3339/RFC3339Nano。
- `invalid endAt`：`endAt` 非空且不是 RFC3339/RFC3339Nano。
- `invalid time range`：开始时间晚于结束时间。

## Dashboard Design

### TimeRangeFilter Primitive

新增 `dashboard/src/ui/TimeRangeFilter.vue`，并从 `dashboard/src/ui/index.ts` 导出。该组件是本地 headless 风格组件，不增加第三方日期选择包。

组件结构：

- 触发器：与 `ColumnFilter` 的表头筛选按钮视觉一致，显示 `时间` 标签和当前范围摘要。
- 浮层：使用项目已有的 `@floating-ui/vue` 定位，包含两个原生 `input[type="datetime-local"]`。
- 操作：`应用`、`清除`。
- 校验：开始时间晚于结束时间时禁用应用并显示错误。

组件模型：

```ts
interface TimeRangeValue {
  startAt: string
  endAt: string
}
```

`startAt` 和 `endAt` 存储 RFC3339 UTC 字符串，格式为 `2026-07-01T09:30:00.000Z`。组件打开浮层时将 RFC3339 转换为本地 `datetime-local` 显示；点击应用时将本地输入转换为 UTC RFC3339 字符串。

组件不修正外部传入的非法 RFC3339 值。非法值显示为错误状态，并允许用户通过清除按钮移除。

### RequestsView Integration

请求页面顶部左侧改为一组筛选控件：

- `类型` 分段控件保持现有位置。
- `时间` 筛选器放在 `类型` 右侧。

`RequestsView.vue` 的 `filters` 增加 `startAt` 和 `endAt`。`requestFilters` 将非空时间值传给 `listRequests`。筛选变化时执行现有分页重置逻辑，并同步 `startAt`、`endAt` 到 URL query。

`activeFilterCount()` 将时间范围计为 1 个筛选条件。`clearAllFilters()` 清除时间范围。页面从 URL 读取 `startAt` 和 `endAt`，并监听路由变化保持状态一致。

### TracesView Integration

追踪页面顶部参考请求页面增加 `时间` 筛选器，放在列表数量和刷新按钮所在头部的左侧区域。

`TracesView.vue` 增加 `filters.startAt` 和 `filters.endAt`，并将其传给 `listRequestTraces` 和 `queryKeys.requestTraces.list`。筛选变化时清除 cursor 并同步 URL query。

从追踪页点击行跳转到请求页时继续只传递 `traceId`。时间范围不自动带入请求页，避免把“按追踪最近请求时间筛选”的语义错误套用到“按请求创建时间筛选”。

## Generated Code

实现后运行：

```bash
sqlc generate
mise run openapi
pnpm --dir dashboard generate-openapi
```

`openapi.yaml` 和 `dashboard/src/openapi-types.d.ts` 必须随接口变化更新。

## Dependencies

不新增依赖。日期输入使用浏览器原生 `datetime-local`，浮层定位使用项目已有的 `@floating-ui/vue`。

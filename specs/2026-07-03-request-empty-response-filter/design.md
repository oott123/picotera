# Design — 请求页面 Tokens 列「空回」筛选

## Scope

为请求列表（`RequestsView`，对应 `GET /api/picotera/requests`）增加一个二元筛选，位于 Tokens 列表头：`全部` / `空回`。选中「空回」时只返回满足以下两条的请求行：

1. 请求的端点是补全端点。
2. 该请求行的 `output_tokens` 为 `0` 或 `NULL`。

筛选为服务端实现，与现有分页、cursor 和其它列筛选项正交，可叠加 `类型`、`渠道`、`端点`、`模型`、`时间` 等筛选。

追踪列表（`GET /api/picotera/request-traces`）不在本次范围内。

## 补全端点的判定

请求行的 `endpoint_path` 有两种来源，对应两种判定方式：

- 路径网关请求：`endpoint_path` 是 `endpoint` 表中存在的端点路径。补全端点 = `endpoint.endpoint_type` 属于补全类型集合：
  - `OpenAIChatCompletions = 2`
  - `OpenAIResponses = 3`
  - `AnthropicMessages = 4`
  - `GeminiGenerateContent = 7`
  - `GeminiStreamGenerateContent = 8`

  非 `endpoint` 表行（如 `General = 1`、`AnthropicCountTokens = 5`、`ExaSearch = 9`、`ModelList = 10`、`Unknown = 0`）不算补全端点。

- 统一网关请求：`endpoint_path` 是运行时常量路由模式（见 `pkg/server/unified_routes.go`），不在 `endpoint` 表中。五个统一路由全部是补全端点：
  - `/api/unified/v1/messages`
  - `/api/unified/v1/responses`
  - `/api/unified/v1/chat/completions`
  - `/api/unified/v1beta/models/{model}:generateContent`
  - `/api/unified/v1beta/models/{model}:streamGenerateContent`

  统一网关的上游行记录的是上游配置端点路径（在 `endpoint` 表中），由上面的端点类型判定覆盖；统一网关的元行记录统一路由模式，由本列表覆盖。

补全类型常量来源于 `pkg/contract/endpoint.go`，统一路由路径来源于 `pkg/server/unified_routes.go`。SQL 中以字面量硬编码这两组值（与现有 `db/queries/routing.sql` 硬编码 `endpoint_type` 整数的模式一致），并在查询注释中标注来源文件，避免引入动态参数化列表的复杂度。

## Backend Design

### Query Parameter

`GET /api/picotera/requests` 增加一个可选 query 参数：

- `emptyResponse`：布尔值。缺省或 `false` 时不生效；`true` 时只返回「空回」请求。

服务端不对此参数做归一化：仅接受 `true` / `false`（Huma 对 bool query 参数的默认解析），其它值由框架以 400 拒绝，不做大小写或近义修正。

### Request List Filtering

`ListRequests` 的 SQL 在现有过滤条件后增加一段，使用 `sqlc.narg('empty_response')::bool` 以便缺省时整段为 no-op：

```sql
AND (
  sqlc.narg('empty_response')::bool IS NULL
  OR NOT sqlc.narg('empty_response')::bool
  OR (
    (r.output_tokens IS NULL OR r.output_tokens = 0)
    AND (
      r.endpoint_path = ANY(ARRAY[
        '/api/unified/v1/messages',
        '/api/unified/v1/responses',
        '/api/unified/v1/chat/completions',
        '/api/unified/v1beta/models/{model}:generateContent',
        '/api/unified/v1beta/models/{model}:streamGenerateContent'
      ]::text[])
      OR EXISTS (
        SELECT 1 FROM endpoint e
        WHERE e.path = r.endpoint_path
          AND e.endpoint_type = ANY(ARRAY[2,3,4,7,8]::int[])
      )
    )
  )
)
```

`endpoint.path` 有唯一约束（`UpsertEndpoint` 的 `ON CONFLICT (path)`），`EXISTS` 子查询走该索引，代价低。分页与排序不变，仍为 `ORDER BY r.created_at DESC, r.id DESC`，cursor 格式不变。

该筛选对 `type`（元/上游/全部）正交：元行与上游行都带 `output_tokens` 与 `endpoint_path`，SQL 不区分 `r.type`，由前端 `类型` 筛选决定可见行集合，「空回」在其上进一步裁剪。

### Contract and Handler

- `contract.ListRequestsRequest` 增加 `EmptyResponse bool \`query:"emptyResponse,omitempty"\``。
- `handleListRequests` 将 `input.EmptyResponse` 转为 `pgtype.Bool{Bool: input.EmptyResponse, Valid: true}` 传入 `db.ListRequestsParams`。由于 sqlc 的 `narg` 生成 `pgtype.Bool`，始终传 `Valid: true`（`true` 或 `false`），由 SQL 中的 `IS NULL OR NOT ...` 短路处理 `false`。不引入新的校验路径。

## Dashboard Design

### State and Data Flow

`RequestsView.vue` 的 `filters` 增加 `emptyResponse: 0`（`0` / `1`，数字类型）。`ColumnFilter` 的泛型约束为 `V extends string | number`，不支持 boolean，故沿用 `providerId` 的数字模式。

- `requestFilters` computed：当 `filters.emptyResponse` 为 `1` 时设置 `out.emptyResponse = true`（API query 参数为布尔）；`0` 时不写入。
- 现有 filter watcher 的依赖数组加入 `filters.emptyResponse`，变化时重置分页并触发列表刷新。
- `activeFilterCount()` 加入 `if (filters.emptyResponse) n++`。
- `clearAllFilters()` 加入 `filters.emptyResponse = 0`。

`emptyResponse` 不写入 URL query。它属于「列内即时筛选」（与 `providerId`/`endpointPath`/`model` 同类），这些列筛选项当前都不持久化到 URL，仅 `traceId`/`projectId`/`startAt`/`endAt` 因来自外部链接而持久化。保持一致。

### Column Header UI

Tokens 列表头当前只渲染 `Token` 文本。改为通过 `#header-tokens` slot 放置一个 `ColumnFilter`：

```vue
<template #header-tokens>
  <ColumnFilter
    v-model.number="filters.emptyResponse"
    label="Token"
    :options="[{ value: 1, label: '空回' }]"
    :empty-value="0"
    :searchable="false"
  />
</template>
```

`ColumnFilter` 自动在顶部渲染 `全部`（`allLabel`）选项，其值为 `emptyValue`（`0`）；列表中渲染 `空回` 选项，值为 `1`。`isActive` 在 `modelValue !== emptyValue` 时为真，选中「空回」时表头显示 `空回` 标签和清除按钮，与其它列筛选视觉一致。

同时给 Tokens 列定义加上 `headerClass`，在筛选激活时显示与其它列相同的下划线高亮：

```ts
{ key: 'tokens', headerClass: filters.emptyResponse ? 'shadow-[inset_0_-2px_0_var(--color-accent)]' : '' }
```

`#cell-tokens` 渲染保持不变。

### Type Definitions

`dashboard/src/api/queryKeys.ts` 的 `RequestsFilters` 类型增加 `emptyResponse?: boolean`。`listRequests` 经 `openapi-fetch` 透传 query 参数，无需改 `client.ts` 的函数签名。

## Generated Code

实现后运行：

```bash
sqlc generate
mise run openapi
pnpm --dir dashboard generate-openapi
```

`pkg/db/request.sql.go`（`ListRequestsParams` 增加 `EmptyResponse pgtype.Bool`）、`openapi.yaml` 与 `dashboard/src/openapi-types.d.ts` 必须随接口变化更新。

## Dependencies

不新增依赖。筛选 UI 复用现有 `ColumnFilter` 原语。

# API 设计

## 新增管理操作：重算模型历史费用

```
POST /api/picotera/models/recalculate-cost
```

admin 组（`is_admin` required，非管理员 403）。Huma operation id `recalculateModelCosts`，定义在 `pkg/contract/model.go`。

### 请求体

```json
{
  "name": "gpt-5.6-luna",
  "range": "168h"
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `name` | string（必填） | 模型名，精确匹配 `model` 表主键，同时是待重算请求行的 `request.model` |
| `range` | string（可省略） | 重算窗口的 Go duration（`time.ParseDuration`，如 `24h`、`168h`、`720h`）。空字符串或省略 = 无下界（全部历史）。解析失败或非正数 → 400。上界固定为服务端处理时刻 |

```go
type RecalculateModelCostsRequest struct {
    Body struct {
        Name  string `json:"name" required:"true" example:"gpt-5.6-luna"`
        Range string `json:"range,omitempty" example:"168h" doc:"Go duration parsed by time.ParseDuration; empty means the whole history"`
    }
}
```

`7d` / `30d` 会被 `time.ParseDuration` 拒绝（Go 没有 `d` 单位）：前端下拉框发送时自行换算成 `168h` / `720h`。

### 响应（200）

```json
{
  "model": "gpt-5.6-luna",
  "range": "168h",
  "startAt": "2026-09-12T07:57:27Z",
  "endAt": "2026-09-19T07:57:27Z",
  "updated": 1234,
  "tookMs": 812
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `model` | string | 回显模型名 |
| `range` | string | 回显请求里的 duration 字符串；「全部」时为空串 |
| `startAt` | string \| null | 窗口下界（RFC3339，UTC）；「全部」时为 null / 省略 |
| `endAt` | string | 窗口上界（RFC3339，UTC），即本次处理时刻 |
| `updated` | number | 被改写的请求行数（type 0 + type 1 合计） |
| `tookMs` | number | 服务端处理耗时（毫秒） |

效果：窗口内 `model = <name>` 且 `finish_reason IS NOT NULL` 的每一行，`model_cost` / `model_cost_currency` 被改写成「该行 token × 当前价格」；无价格档位或 `input_tokens IS NULL` 的行写成 NULL / NULL。随后 `request_overview_bucketed` 会用窗口 `[startAt, now]`（「全部」时是整表）重物化一次。

### 错误

| 状态 | 场景 |
| --- | --- |
| 400 `model has no pricing` | 模型存在但 `pricing` 为空/无档位（没有可写的值，拒绝执行） |
| 400 | `range` 不是合法 Go duration（例如 `7d`、`abc`）或非正数（`0`、`-24h`） |
| 404 | 模型不存在（`GetModelByName` 返回 `pgx.ErrNoRows`） |
| 403 | 非管理员 |
| 422 | `name` 缺失（Huma schema 校验） |
| 500 | 扫描 / 批量 UPDATE 失败。此时先前的批次已提交，重跑即完成剩余部分 |

## 仪表盘接入

`dashboard/src/api/index.ts` 追加类型再导出：

```ts
export type RecalculateModelCostsRequestBody = components['schemas']['RecalculateModelCostsRequestBody']
export type RecalculateModelCostsResponseBody = components['schemas']['RecalculateModelCostsResponseBody']
```

`dashboard/src/api/client.ts` 追加 fetcher 与失效助手：

```ts
export async function recalculateModelCosts(
  body: RecalculateModelCostsRequestBody,
): Promise<RecalculateModelCostsResponseBody> {
  const { data, error } = await api.POST('/api/picotera/models/recalculate-cost', { body })
  if (error) fail(error, '重算历史费用失败')
  return data
}

// Request rows, traces and both overviews all render model_cost.
export function invalidateRequestCosts(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.requests.all })
  client.invalidateQueries({ queryKey: queryKeys.requestTraces.all })
  client.invalidateQueries({ queryKey: queryKeys.overview.all })
  client.invalidateQueries({ queryKey: queryKeys.adminOverview.all })
}
```

无新增查询参数、无新增列表端点、无脚本 API 变更。

侧栏下拉框的取值到发送值的映射（前端自行换算，后端不认 `d`）：

| 下拉框 | 发送的 `range` |
| --- | --- |
| 24 小时 | `24h` |
| 7 天 | `168h` |
| 30 天 | `720h` |
| 全部 | `""`（空串） |
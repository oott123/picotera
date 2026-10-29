# API 设计

## 管理 API：`RequestView` 新增两字段

影响 `GET /api/picotera/requests`、`GET /api/picotera/requests/{id}`、
`GET /api/picotera/requests/{id}/spans` —— 三者共用 `contract.RequestView`。

```go
UsageRaw     map[string]any `json:"usageRaw,omitempty"`
ToolUsageRaw map[string]any `json:"toolUsageRaw,omitempty"`
```

OpenAPI 中生成为自由对象（`type: object`, `additionalProperties: true`），TS 侧是
`{ [key: string]: unknown }`。列为 NULL 时字段整体省略。

示例（OpenAI Responses 上游）：

```json
{
  "id": "dag9d1gs9a269lib21cg",
  "inputTokens": 100,
  "outputTokens": 50,
  "toolUsage": [{ "name": "web_search", "numRequests": 1 }],
  "usageRaw": {
    "input_tokens": 120,
    "input_tokens_details": { "cached_tokens": 20 },
    "output_tokens": 50,
    "output_tokens_details": { "reasoning_tokens": 12 },
    "total_tokens": 170
  },
  "toolUsageRaw": {
    "image_gen": { "input_tokens": 0, "output_tokens": 0, "num_images": 0 },
    "web_search": { "num_requests": 1 }
  }
}
```

注意示例里两处刻意的不一致，它们都是设计意图：

- `usageRaw.input_tokens` 是 120，而 `inputTokens` 列是 100 —— 归一化会把 `cached_tokens` 从输入里扣掉。
- `toolUsageRaw` 有 `image_gen` 而 `toolUsage` 没有 —— 全零条目在归一化时被丢弃。

无新增端点、无新增查询参数、无新增过滤条件。

## 脚本 API

### `getToolUsageCost` 输入

`ToolUsageCostView` 新增两个**只读**字段：

```ts
interface ToolUsageCost {
  toolUsage: ToolUsageEntry[]     // 归一化用量；上游没报告时为 []
  usageRaw: object | null         // 【新增，只读】上游原始 usage 对象
  toolUsageRaw: object | null     // 【新增，只读】上游原始 tool_usage 对象
  toolCost: number | null
  toolCostCurrency: string
}
```

返回值的校验规则不变：glue IIFE 只从返回对象里取 `toolUsage` / `toolCost` / `toolCostCurrency` 三项重新构造
结果，因此返回值里带上 `usageRaw` / `toolUsageRaw`（例如 `return { ...input, toolCost: 0.01, toolCostCurrency: 'USD' }`）
既不报错也不生效。`toolUsage` 条目键白名单仍然严格，拼错即报错。

用法示例 —— 按上游原始 web 搜索次数计价，并叠加图片生成的实际张数：

```js
picotera.hooks.getToolUsageCost.tap('price-from-raw', function (ctx, input) {
  const raw = input.toolUsageRaw
  if (!raw) return
  const searches = raw.web_search?.num_requests ?? 0
  const images = raw.image_gen?.num_images ?? 0
  const cost = searches * 0.01 + images * 0.04
  if (!cost) return
  return { toolUsage: input.toolUsage, toolCost: cost, toolCostCurrency: 'USD' }
})
```

### `requestFinished` 输入

`RequestFinishedView` 新增同名的两个字段：

```ts
interface RequestFinishedView {
  // …既有字段不变…
  toolUsage: ToolUsageEntry[]     // 恒为数组
  usageRaw: object | null         // 【新增】上游原始 usage 对象
  toolUsageRaw: object | null     // 【新增】上游原始 tool_usage 对象
}
```

两者与 `toolUsage` 一样取自内存快照，不回读数据库。失败请求没有上游响应，两者恒为 `null`。

用法示例 —— 把上游报告的推理 token 数打成请求注解：

```js
picotera.hooks.requestFinished.tap('record-reasoning-tokens', function (ctx, input) {
  const n = input.usageRaw?.output_tokens_details?.reasoning_tokens
  if (!n) return
  picotera.request.setAnnotation(input.requestId, 'reasoningTokens', String(n))
})
```

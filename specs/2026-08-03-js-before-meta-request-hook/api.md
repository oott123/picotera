# 脚本 API：beforeMetaRequest

本次改动不涉及 HTTP 管理 API，只新增脚本侧 hook。

## 注册

```js
picotera.hooks.beforeMetaRequest.tap(name, fn, priority)
```

`fn(ctx, input)`：`input` 为 `undefined`（首个 tap）或前一个 tap 返回的 `ResponseShape`。返回 `undefined` 表示不干预。

## 时机

每次网关 / 统一网关请求执行一次，位于 `sortProviders` 之后、第一次上游尝试之前。`sortProviders` 返回空数组时同样执行。「获取模型列表」链路不执行。

执行时 `ctx.provider` / `ctx.providerModel` / `ctx.attempt` / `ctx.upstreamRequest` 均为 `null`（尚未进入尝试循环），其余字段（`endpoint`、`request`、`apiKey`、`user`、`routedModel`、`annotations`、`metaRequest`、`stream`、`sourceFormat`、`format`）已就绪。

## 返回值

```ts
type BeforeMetaRequestResult = undefined | ResponseShape

interface ResponseShape {
  statusCode: number                              // 整数，100–599
  headers?: Record<string, string | string[]>     // 可选
  body?: string | object | Array<unknown> | null  // 可选
  tokens?: ResponseTokens                         // 可选，用量记账
}

interface ResponseTokens {
  inputTokens?: number
  outputTokens?: number
  cacheReadTokens?: number
  cacheWriteTokens?: number
  cacheWrite1hTokens?: number
}
```

- 返回 `undefined` / `null`：正常继续后续流程。
- 返回 `ResponseShape`：网关直接把它写给客户端，不发起任何上游请求。
  - `body` 为字符串：原样写出。
  - `body` 为对象/数组（包含 `ctx.request.body` 这类 Proxy）：`JSON.stringify` 后写出。
  - `body` 缺省 / `null`：空响应体。
  - `Content-Type`：响应体非空且 `headers` 未指定时默认 `application/json`。
  - `tokens`：填哪个记哪个，未填的列保持空。填了 `tokens` 时费用按当前模型的 pricing 自动计算。

## 校验规则（严格，违反即请求失败）

- 返回值必须是 `undefined`、`null` 或普通对象；数组、字符串、数字均报错。
- `statusCode` 必须是 `[100, 599]` 内的整数。
- `headers` 若存在必须是普通对象，值必须是 `string` 或 `string[]`。
- 不允许设置 `Content-Length` / `Transfer-Encoding`（大小写不敏感），由 Go 层计算。
- `body` 只接受 `string` / 对象 / 数组 / `null` / `undefined`；number、boolean、function 报错。
- `tokens` 若存在必须是普通对象，键只能是上述五个之一（拼错即报错），值必须是 `[0, 2147483647]` 内的整数。

校验失败与 tap 抛错同等对待：请求立即失败（502；hook 超时 503），不再尝试任何渠道。

## 记录语义

- 不产生上游请求记录；`provider_id`、`upstream_model`、ttft 均为空。
- `status_code` 为脚本返回值；`finish_reason`：2xx → `3`（正常结束），其余 → `1`（内部错误）。
- 非 2xx 时 `error_message` 记为响应体文本；2xx 时不记。
- token 列按 `tokens` 里提供的键写入，未提供的保持空；提供了 `tokens` 时按当前模型 pricing 计算并记录费用。
- `requestFinished` 照常触发，其输入中未发生的字段为 0（填了 `tokens` 则能读到）。
- 成功率统计要求 `finish_reason = 3` 且 `outputTokens` 非零，因此 2xx 快速响应只有填了非零 `outputTokens` 才计成功，否则计入「空回复」。

## 示例

```js
// 无可用渠道时返回兜底响应（需在 sortProviders 里自行记录状态）
picotera.hooks.sortProviders.tap('mark-empty', function (ctx, candidates) {
  ctx.noCandidates = candidates.length === 0
})

picotera.hooks.beforeMetaRequest.tap('fallback', function (ctx) {
  if (!ctx.noCandidates) return
  return {
    statusCode: 503,
    body: { error: { type: 'no_provider', message: '当前模型没有可用渠道' } },
  }
})
```

```js
// 命中缓存直接返回（Anthropic Messages 格式）
picotera.hooks.beforeMetaRequest.tap('cache', function (ctx) {
  if (ctx.stream) return
  const hit = picotera.kv.get('resp:' + ctx.routedModel.name + ':' + ctx.request.body.messages.at(-1).content)
  if (!hit) return
  return {
    statusCode: 200,
    headers: { 'X-PicoTera-Cache': 'hit' },
    body: hit,
    tokens: {
      inputTokens: hit.usage.input_tokens,
      outputTokens: hit.usage.output_tokens,
    },
  }
})
```

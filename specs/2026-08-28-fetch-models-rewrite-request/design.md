# 设计：fetchModels 链路接入 rewriteRequest

## 目标

「获取模型列表」向上游 `provider.modelsEndpointUrl` 发起的那次 GET 请求，在发出前经过 `rewriteRequest` hook，脚本可改写 URL、方法、请求头与请求体。改写与其后的 `rewriteProviderModels` 共用同一个 JS 会话，`ctx` 上的自定义字段可以在两个 hook 之间传递状态。

## 执行顺序

`handleFetchModels`（`pkg/server/handle_provider_endpoint.go`）重排为：

1. 取 provider；`modelsEndpointUrl` 为空 → 400（不变，早于任何脚本执行）。
2. **创建 JS 会话**（原先在拉取之后创建，现在提前到拉取之前），`defer sess.Close()`。
3. **PatchContext 第一次**：`endpointType = "fetchModels"`、`provider`、`annotations`（渠道层合并注解）。
4. 构建 GET 请求：`applyCredentials` + `anthropic-version` 头（不变）。
5. **`RunRewriteRequest`**：入参为 `serializePendingRequest(req)` 与 `nil` body。
6. 用 hook 返回的 pending 重建请求（`buildRequestFromPending`），随后发出、解码、`parseModelsResponse`、`aggregateProviderModels`（不变）。
7. **PatchContext 第二次**：`upstreamResponse`。
8. `RunRewriteProviderModels` → 清洗去重 → 返回（不变）。

10 秒的上游超时 `fetchCtx` 移到 hook 之后创建，只覆盖真正的上游往返；hook 自身由引擎的 `PICOTERA_JS_HOOK_TIMEOUT` 约束。重建后的请求绑定 `fetchCtx`。

## ctx 契约

| 字段 | 值 |
| --- | --- |
| `endpointType` | `"fetchModels"` |
| `provider` / `annotations` | 当前渠道与渠道层注解，`rewriteRequest` 与 `rewriteProviderModels` 中都可见 |
| `upstreamResponse` | 仅 `rewriteProviderModels` 中存在 |
| 其余字段 | `null` / 零值 |

## pending 契约

请求无 body，`pending.body` 为 `undefined`，与网关侧非 JSON 请求一致。就地给 `pending.body` 赋值不生效（`bodyState` 在无 body 时恒为 `none`）；脚本要携带请求体必须返回新对象，例如把拉取改成 POST：

```js
picotera.hooks.rewriteRequest.tap('models-post', function (ctx, pending) {
  if (ctx.endpointType !== 'fetchModels') return
  return { ...pending, method: 'POST', headers: { ...pending.headers, 'content-type': ['application/json'] }, body: { scope: 'all' } }
})
```

上游凭证已由 `applyCredentials` 注入到 `pending.headers`，脚本可覆盖或删除，与网关链路一致。

## 错误行为

`rewriteRequest` 抛错或超时 → 不发上游请求，直接失败：超时 503、其余 502，与本 handler 已有的 `PatchContext` / `rewriteProviderModels` 失败行为一致。三处状态码判断统一改用包内已有的 `gatewayHookStatus`。hook 返回的 URL 无法构建请求（`buildRequestFromPending` 失败）同样按 502 返回。

## 影响面

REST 契约、数据库、dashboard 均无改动，`openapi.yaml` 无需重新生成。行为变化是：已有的 `rewriteRequest` 脚本会被动地在「获取模型列表」时执行一次——这是需求本身，脚本用 `ctx.endpointType` 判别。

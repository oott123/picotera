# afterUpstreamError JS Hook

给 JS hook 新增一个 hook 点 `afterUpstreamError`，执行时机在上游响应报错之后，包括流内报错（in-stream error）和 HTTP 报错。

## Hook 输出

```js
{ break: false, statusCode: 400, message: '' }
```

- `break=true` 时中断处理，按 `statusCode` 和 `message` 响应下游；如果 `statusCode` / `message` 未指定，则 follow 上游原本的（原样透传上游 status code 与错误 body）。
- `break` 仅在 HTTP 类报错时生效。流内报错因为已经 stream 到下游了，`break` 不生效（hook 仍会执行，仅供观测）。

## 触发范围

所有上游 attempt 失败都触发该 hook，包括：HTTP 非 200 响应、连接/网络失败、读取超时、解码失败、bridge 转换失败、流内错误。对于没有有效上游响应的失败（如连接失败），`statusCode` 可能为 0。

## ctx.attempt.lastError

`ctx.attempt` 增加（沿用现有）`lastError` 对象，包含 status code、message。要求：

- `lastError` 必须在 `afterUpstreamError` hook 执行之前就设置进 `ctx.attempt`。
- 如果没有 `break`，`lastError` 要带到同 session（同一网关请求）的下一个 attempt 的上下文里（现有 `beforeRequest` 已消费该字段）。

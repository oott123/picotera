# 设计

## 目标

上游返回 HTTP `400` 时，网关默认把这次响应透传回客户端并停止重试其余 provider；该默认行为通过 `afterUpstreamError` hook 的 `break` 种子值实现，因此脚本可完全改写它。

## 现状

`afterUpstreamError` 是一个 waterfall hook，在每次上游尝试失败后运行。决策 `break` 的初值由传入 hook 的 `UpstreamErrorView.Break` 种子决定（`pkg/jsx/session.go` 的 `RunAfterUpstreamError`），目前所有网关调用方（`pkg/server/gateway_flow_attempts.go` 的 `runAfterUpstreamError`）都把种子固定为 `false`。

当 `break=true && streamed=false` 时，`respondUpstreamErrorBreak` 把响应写回客户端：

- `dec.StatusCode<=0` → 沿用上游原始状态码（再回退 502）；
- `dec.Message==""` → 透传上游原始 body 与 `Content-Type`；否则用 `dec.Message` 覆盖 body 并强制 `application/json`。

## 方案

### 1. 网关层种入默认 break

在 `runAfterUpstreamError` 中，根据本次失败的状态码计算 `break` 种子：状态码恰好为 `400` 且 `streamed=false` 时种入 `true`，否则 `false`。状态码取自 `state.LastJSErr.StatusCode`（非 200 上游响应由 `updateAttemptState` 写入真实状态码）。

```go
defaultBreak := statusCode == http.StatusBadRequest && !streamed
dec, err := f.session.RunAfterUpstreamError(jsx.UpstreamErrorView{
    Break: defaultBreak, StatusCode: statusCode, Message: message, Streamed: streamed,
})
```

hook 因此能在输入 `input.break` 中看到默认已是 `true`，并通过返回 `{ break: false }` 改写为继续尝试。

### 2. 修正 passthrough 的回显语义，保证忠实透传

passthrough 在 `RunAfterUpstreamError` 中其实有两条路径，二者都必须修：

1. **Go 侧 `zero`**：仅在 waterfall 显式返回 `undefined` / `null` / `ctx` 时才使用。当前 `zero` 回显 `initial.StatusCode` / `initial.Message`，需改为只携带 `Break`：

   ```go
   zero := AfterUpstreamErrorDecision{Break: initial.Break}
   ```

2. **JS 侧 wrapper（关键）**：waterfall 的语义是「tap 返回 `undefined` 则沿用上一个值」，因此 **no-op tap 或完全没有 tap 时，`runWaterfall` 返回的是种子输入对象本身**（identity），并不会落到 `zero`。当前 wrapper 只判 `r === ctx / undefined / null`，于是种子的 `statusCode` / `message` 被原样规范化回显。一旦默认种子 `break=true`，回显的 `message`（上游错误 body 文本）会让 `respondUpstreamErrorBreak` 误判为「覆盖」，丢失上游原始 `Content-Type`，无法忠实透传。

   修法：把种子存入变量 `input`，在 passthrough 判定中加入 `r === input`：

   ```js
   var input = <seed>;
   var r = picotera.hooks.afterUpstreamError.runWaterfall(globalThis.ctx, input);
   if (r === globalThis.ctx || r === input || typeof r === 'undefined' || r === null) return undefined;
   ```

   这样 no-op / 无 tap 都判为 passthrough → 返回 `undefined` → Go 落到 `zero = {Break: initial.Break}`。

两处合并后语义统一为「passthrough = 沿用初始 break，不做任何覆盖」，与脚本显式返回 `{ break: true }`（不带 statusCode/message）的「follow upstream」语义一致。结果：400 + 无 hook（或 hook passthrough）→ `dec = {break:true, statusCode:0, message:""}` → 透传上游原始状态码、body、`Content-Type`，即忠实透传。

`UpstreamErrorView.StatusCode` / `Message` 仍然原样传入 hook 供脚本读取（脚本主动返回的新对象不受影响），仅不再回显进 passthrough 决策。

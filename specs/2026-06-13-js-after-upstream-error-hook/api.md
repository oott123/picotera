# API — afterUpstreamError JS Hook

## JS waterfall

```js
picotera.hooks.afterUpstreamError.tap(name, fn, priority)
```

`fn(ctx, value)` 与其它 waterfall 一致：返回值成为下一个 tap 的输入；返回 `undefined` 透传。

### 输入 value

```ts
{
  break: boolean      // 初始 false
  statusCode: number  // 上游原始 status code；连接/构建失败时为 0
  message: string     // 上游原始错误 body / 错误文本
  streamed: boolean   // true=响应已 stream 到下游，break 将被忽略
}
```

### 输出 value

```ts
{
  break: boolean      // true 且 streamed=false 时中断并响应
  statusCode: number  // <=0 表示沿用上游原始 status
  message: string     // 空字符串表示沿用上游原始 body
}
```

非对象、`null`、`undefined`、或返回 `ctx` 本身均视为 passthrough（等价 `break=false`）。`statusCode` 按整数取值，`message` 非字符串时取空。

### break 响应规则（仅 streamed=false 生效）

- status = `statusCode > 0 ? statusCode : 上游原始 status`；都不可用则 `502`。
- body = `message != "" ? message : 上游原始 body`。
- `message` 非空：`Content-Type: application/json`，写出 `message` 原始字节。
- `message` 为空：透传上游响应 body 字节与上游 `Content-Type`（无上游响应时用错误文本 + `application/json`）。

## ctx.attempt.lastError

`ctx.attempt.lastError`（已存在，结构不变），在 `afterUpstreamError` 执行前即被写入：

```ts
ctx.attempt.lastError: {
  providerId: number
  statusCode: number
  message: string
} | null   // 首个 attempt 之前为 null
```

无 `break` 时该 `lastError` 会带到同一网关请求下一个 attempt 的 `ctx.attempt`（`beforeRequest` 可读）。

## Go 类型（pkg/jsx/types.go）

```go
// UpstreamErrorView 是 afterUpstreamError waterfall 的输入。
type UpstreamErrorView struct {
    Break      bool   `json:"break"`
    StatusCode int    `json:"statusCode"`
    Message    string `json:"message"`
    Streamed   bool   `json:"streamed"`
}

// AfterUpstreamErrorDecision 是 afterUpstreamError waterfall 的输出。
type AfterUpstreamErrorDecision struct {
    Break      bool   `json:"break"`
    StatusCode int    `json:"statusCode"`
    Message    string `json:"message"`
}
```

## Session 接口（pkg/jsx/iface.go）

```go
RunAfterUpstreamError(initial UpstreamErrorView) (AfterUpstreamErrorDecision, error)
```

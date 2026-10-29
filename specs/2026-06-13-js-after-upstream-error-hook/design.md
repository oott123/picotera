# Design — afterUpstreamError JS Hook

## 背景

JS hook 当前有五个 waterfall（`sortProviders` / `beforeRequest` / `beforeTransform` / `rewriteRequest` / `rewriteModel` / `rewriteProviderModels`），全部在「请求发出之前」执行。没有任何 hook 在上游响应/失败之后执行。

`ctx.attempt.lastError`（`jsx.LastError`，含 `providerId` / `statusCode` / `message`）已经存在：`updateAttemptState`（`gateway_flow_attempts.go`）在每次 attempt 失败后把它写进 `attemptState.LastJSErr`，下一次 attempt 的 `runBeforeRequest` 通过 `PatchContext` 把它合并进 `ctx.attempt`。因此「lastError 带到下一个 attempt」的链路已存在，本特性复用它。

本特性新增第七个 waterfall `afterUpstreamError`，并把 `lastError` 的写入时机提前到该 hook 执行之前。

## Hook 语义

- 名称：`afterUpstreamError`。
- 输入 waterfall value：`{ break, statusCode, message, streamed }`（见 `api.md`）。
- 输出：`{ break, statusCode, message }`。passthrough（返回 `undefined` / `null` / `ctx`）等价于 `break=false`。
- 每次上游 attempt 失败触发一次。一个网关请求若有 N 次失败 attempt，则 hook 触发 N 次，任意一次都可 `break`。

### break 生效条件

`break` 仅对「客户端响应尚未开始写出」的失败生效（HTTP 非 200、连接/网络失败、解码/bridge 构建失败）。这些失败发生在 `runSingleAttempt` 内、写出客户端响应之前。

`break` 对「响应已经开始 stream 到下游」的失败不生效——即流内错误（HTTP 200 + SSE error 事件）。此时 hook 仍执行（供脚本观测、写 kv、打日志），但返回的 `break` 被忽略。输入里的 `streamed=true` 让脚本能区分这两种情形。

### break 响应

`break=true` 且可生效时，按下列规则写出下游响应并结束整个网关请求（不再尝试后续 provider，不走 `failAllProviders`）：

- status code：`dec.statusCode > 0 ? dec.statusCode : 上游原始 status`；若两者都不可用（如连接失败原始 status 为 0），回退 `502`。
- body：`dec.message != "" ? dec.message : 上游原始错误 body`。
  - passthrough（`message` 为空）时：HTTP 失败用上游真实响应 body 字节 + 上游 `Content-Type` 原样透传；无上游响应的失败（连接/构建失败）用记录的错误文本，`Content-Type: application/json`。
  - 覆盖（`message` 非空）时：写出 `message` 原始字节，`Content-Type: application/json`。
- meta 行标记为 failed 并上传 meta artifact（upstream 行已在记录失败时完成）。

不引入兼容层、不做输入宽松化：脚本返回的 `statusCode` 按数值取整，`message` 非字符串则丢弃为空（passthrough）。

## 触发点（pkg/server）

所有触发点共用 `gatewayFlow.runAfterUpstreamError`，它先 `PatchContext` 把当前 `attemptState` 的计数与 `lastError` 合并进 `ctx.attempt`，再跑 waterfall。`lastError` 在跑 hook 前已由 `updateAttemptState` 写入 `state.LastJSErr`，满足「hook 之前就设置进去」。

1. `gateway_flow_attempts.go::runSingleAttempt` 内的全部上游失败分支：
   - `insertUpstreamAttempt` 失败（status 0）
   - `buildRewrittenUpstreamRequest` 非 hook 错误（bridge/构建失败，status 0）
   - `forwardRequest` 失败（连接/网络，status 0）
   - `handleUpstreamNonOK`（HTTP 非 200，带上游 body + header）
   这些分支 `break` 生效。
   - **例外**：`buildRewrittenUpstreamRequest` 返回的 `gatewayHookError`（`rewriteRequest` / `beforeRequest` 等 JS hook 自身报错，含 hook 超时导致 session tainted）**不触发** `afterUpstreamError`——它是 hook 内部错误而非上游失败，且 tainted session 无法再跑 hook。维持现有 `failHook` 行为。

2. 流内错误（`break` 被忽略，`streamed=true`）：
   - path 路由：`gateway_flow_success.go::completeGatewaySuccess`，`streamErr != ""` 时。
   - unified 路由：`gateway_unified_helpers.go::unifiedStreamSuccess`，`streamErr != ""` 时。

### hook 失败的处理

`afterUpstreamError` 自身执行失败（如超时、抛异常）时：记录日志，按 `break=false` 处理（继续重试 / 忽略），不因 advisory 的错误 hook 拖垮整个代理。若是超时导致 session tainted，后续 attempt 的 `beforeRequest` 会照常以 `ErrHookTimeout` 失败并由 `failHook` 收敛，行为不变。

## 计数语义

`afterUpstreamError` 看到的 `ctx.attempt.currentRetryCount` / `totalAttemptCount` 为「截至本次失败已发生的 attempt 数」（即 `updateAttemptState` 自增后的值，与下一次 `beforeRequest` 看到的一致）。需求只要求 `lastError`，计数为附带信息。

## 不改动项

- `jsx.LastError` 结构不变（已含所需字段）。
- `lastError` 带到下一个 attempt 的链路（`state.LastJSErr` → `runBeforeRequest` 的 `PatchContext`）不变。
- 非流式 200 + 错误 body 的检测不在范围内（现有未检测）。

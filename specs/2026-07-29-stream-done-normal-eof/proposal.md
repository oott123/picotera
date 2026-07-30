# Proposal: 流已发送终止事件后客户端断开，不记为「取消」

流式传输场景下，如果上游已经把流的终止事件发完（例如 OpenAI Chat Completions 的 `data: [DONE]`），客户端随后主动断开连接（不再读取剩余字节 / 直接 close），记录 finish reason 时不要记成「取消」（`FinishReasonCancelled`），而应当当作正常 EOF（`FinishReasonEOF`）。

## 背景

`classifyStreamFinishReason`（`pkg/server/gateway_flow_success.go`）在 read 循环结束后判定 finish reason：读到 `io.EOF` → EOF；空闲超时 → ReadTimeout；否则若客户端请求 context 已取消 → Cancelled。

客户端在收到 `[DONE]` 后立刻断开是很常见的正常行为（很多 SDK 拿到终止事件就不再读到连接关闭）。此时请求 context 被取消，向上游的读取返回 `context.Canceled`，于是整条请求被记为「取消」——这是误报，流其实已经完整传输。

## 需求

1. 在流式响应中识别「上游流已到达终止事件」这一状态。
2. 该状态成立时，客户端取消导致的读取中断记为 `FinishReasonEOF`，而不是 `FinishReasonCancelled`。
3. 路径网关（`/v1/...` 等 endpoint 匹配路由）与 `/api/unified` 统一路由都适用。

## 澄清（planning 期间与用户确认）

- **终止事件的识别范围：覆盖四种上游格式**，不局限于 `data: [DONE]`：
  - OpenAI Chat Completions（及兼容实现）：`data: [DONE]`
  - Anthropic Messages：`message_stop`
  - OpenAI Responses：`response.completed` / `response.incomplete`
  - Gemini GenerateContent：末帧的 `candidates[].finishReason`
- **看到终止事件后再发生「读空闲超时」（上游不关闭连接）仍记为 `FinishReasonReadTimeout`**，不改判为 EOF。本次只把「客户端取消」这一种情形改判。

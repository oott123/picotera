# 执行计划：把 Anthropic `refusal` 记为流式错误

## 1. 新增 refusal 判定方法

文件：`pkg/server/response_extractor.go`

- 在 `detectStreamError` 之后新增方法：

  ```go
  func (e *ResponseExtractor) detectRefusal(stopReason, stopDetails gjson.Result)
  ```

- 逻辑：
  - `e.streamError != ""` 时直接返回（首个错误优先，与 `detectStreamError` 一致）；
  - `stopReason.Type != gjson.String` 或 `stopReason.String() != "refusal"` 时返回；
  - `stopDetails.Get("explanation")` 是非空字符串则写入 `e.streamError`；
  - 否则写入字面量 `"refusal"`。
- 补文档注释，说明两个调用点各自传入的是 `delta.*` 还是顶层字段。
- 更新 `streamError` 字段的注释（当前写的是「first in-stream error.message seen in an SSE data payload」），改为覆盖 error payload 与 refusal 两个来源、SSE 与非流式 JSON 两条路径。

## 2. 接入 SSE 路径

文件：`pkg/server/response_extractor.go`，函数 `detectStreamError`

- 在两个 `*error.message` 分支之后、`choices.0.finish_reason` 分支（带 early return）之前插入：

  ```go
  e.detectRefusal(gjson.Get(payload, "delta.stop_reason"), gjson.Get(payload, "delta.stop_details"))
  if e.streamError != "" {
  	return
  }
  ```

- 更新 `detectStreamError` 的文档注释，把 Anthropic refusal 加进它逐格式枚举的错误形状表。
- 不按事件类型 gating：顶层 `delta` 是 Anthropic 独有形状，不会误伤 OpenAI Chat（`delta` 在 `choices[]` 下）或 Gemini（无 `delta`）。
- 不改 `extractAnthropicSSE`、`processSSEEvent`、`detectStreamCompletion`——refusal 流仍以 `message_stop` 正常收尾，`streamCompleted` 保持 `true` 是正确的；finish reason 的覆盖发生在 `completeGatewaySuccess`。

## 3. 接入非流式 JSON 路径

文件：`pkg/server/response_extractor.go`，函数 `extractJSONMetrics`

- 在 Anthropic 格式 token 提取一段之后调用：

  ```go
  e.detectRefusal(result.Get("stop_reason"), result.Get("stop_details"))
  ```

- 只加这一处 refusal 检测，**不**把整个 `detectStreamError` 搬进非流式路径——`error.message` / `choices.0.finish_reason` 在非流式体上的语义不在本次需求范围内。

## 4. 新增 fixture

文件：`fixtures/anthropic-refusal.sse`（新建）

- 内容：一个最小但真实的 Anthropic 拒答流——`message_start`（带 `usage.cache_read_input_tokens`）、原始需求中给出的 `message_delta` 事件（原样保留 `stop_reason` / `stop_details` / `usage`）、`message_stop`。
- 事件之间用空行分隔，保持 `event:` + `data:` 两行的标准 SSE 帧格式。

## 5. 新增测试

文件：`pkg/server/response_extractor_test.go`

沿用既有 `TestResponseExtractor_SSE_StreamError_*` 命名与表驱动风格。

- `TestResponseExtractor_SSE_StreamError_AnthropicRefusalFixture`
  - 读 `../../fixtures/anthropic-refusal.sse`，用 `NewResponseExtractor(strings.NewReader(...), "text/event-stream", time.Now())` 包裹后 `io.ReadAll`；
  - 断言 `StreamError()` 等于 fixture 里的 `explanation` 全文；
  - 断言 `Metrics().OutputTokens` 为 0、`CacheReadTokens` 为 244090（refusal 仍需计费）；
  - 断言 `StreamCompleted()` 为 `true`；
  - 断言透传字节与 fixture 原文完全一致。
- `TestResponseExtractor_SSE_StreamError_AnthropicRefusalWithoutExplanation`
  - `message_delta` 只有 `delta.stop_reason: "refusal"`，无 `stop_details` → `StreamError()` 为 `"refusal"`；
  - 有 `stop_details` 但 `explanation` 为空字符串 → 同样为 `"refusal"`。
- `TestResponseExtractor_SSE_StreamError_AnthropicStopReasonNonRefusal`
  - `end_turn`、`max_tokens`、`tool_use`、`stop_sequence` 均不设置 `StreamError()`；
  - `stop_reason: null` 与 `stop_reason: 1`（非字符串）均不设置 `StreamError()`。
- `TestResponseExtractor_JSON_StreamError_AnthropicRefusal`
  - 用 `Content-Type: application/json` 构造 Anthropic 非流式响应体，顶层 `"stop_reason":"refusal"` + `stop_details.explanation`；
  - 断言 `StreamError()` 等于 explanation，且 `Metrics()` 的 token 仍被正确提取；
  - 负向：顶层 `"stop_reason":"end_turn"` 时 `StreamError()` 为空。
- `TestResponseExtractor_SSE_StreamError_FirstWins` 的语义要保持：补一条用例，先出现 `error.message` 事件、后出现 refusal `message_delta` 时，`StreamError()` 保留前者。

## 6. 更新 CLAUDE.md

文件：`CLAUDE.md`

- `afterUpstreamError` 那一段现在写的触发面是「HTTP 非 200、连接/网络失败、解码/bridge 失败，以及 in-stream SSE errors」。补上：HTTP 200 且响应体（SSE 或非流式 JSON）带 Anthropic `stop_reason: "refusal"` 时同样触发，`streamed=true`，finish reason 记为 `FinishReasonStreamError`。
- 不改 `README.md`。

## 7. 验证

```bash
go test ./pkg/server -run 'ResponseExtractor'
go test ./pkg/server
go build ./...
```

无需运行：

- `sqlc generate`：无 SQL 或生成代码变化。
- `mise run openapi` + `pnpm --dir dashboard generate-openapi`：无 contract 变化。
- dashboard 构建 / type-check / lint：无前端代码变化。

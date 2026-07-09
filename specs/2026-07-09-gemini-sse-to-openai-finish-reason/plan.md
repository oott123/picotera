# Plan

## 1. 修复 vendored 入站流过滤

文件：`third_party/axonhub/llm/transformer/openai/inbound.go` 的 `isReasoningSignatureEvent`（约 154 行）。

- 在 `hasContent/hasReasoningContent/hasToolCalls/hasRefusal` 之后新增：
  ```go
  hasFinishReason := resp.Choices[0].FinishReason != nil && *resp.Choices[0].FinishReason != ""
  ```
- 将返回改为：
  ```go
  return !hasContent && !hasReasoningContent && !hasToolCalls && !hasRefusal && !hasFinishReason
  ```
- 更新函数注释，说明携带 `finish_reason` 的纯签名事件必须保留。

## 2. 端到端回归测试

文件：`pkg/llmbridgeimpl/bridge_stream_test.go`（已存在）`TestBridgeStreamGeminiToOpenAIChatFinishReason`。

- 读 `../../fixtures/gemini.sse`。
- `BridgeStream(ctx, FormatOpenAIChatCompletions, FormatGeminiStreamGenerateContent, body, "text/event-stream", mustProfile(t, FormatGeminiStreamGenerateContent))`。
- 解析 SSE：`parseSSEData` 按 `\n\n` 切块抽 `data:`；跳过 `[DONE]`；`gjson` 取 `choices.0.finish_reason`（仅字符串）。
- 断言：`sawChunk` 为真；至少收集到一个 `finish_reason`；且全部等于 `"stop"`。

## 3. 单元回归测试

文件：`third_party/axonhub/llm/transformer/openai/inbound_reasoning_test.go` 的 `TestIsReasoningSignatureEvent`。

- 新增用例：单个 choice，`Delta.ReasoningSignature` 非空、无 content/tool/refusal，但 `FinishReason = lo.ToPtr("stop")` → 期望 `want: false`（不被跳过）。

## 4. 验证

- `go test ./pkg/llmbridgeimpl/`（全绿，含新端到端用例）。
- `go test github.com/looplj/axonhub/llm/transformer/openai`（全绿，含新单测；`openai/codex` 因无关的 `go.sum` 缺失而 setup failed，不在本次改动路径，忽略）。
- `go build -o /tmp/picotera-llmbridge-plugin ./cmd/picotera-llmbridge-plugin`（插件二进制构建通过，确认 vendored 修复可编译进插件）。

## 5. 清理

- 不提交调试用测试文件；不改动 `fixtures/gemini.sse`、不新增依赖。
- 更新 `specs/2026-07-09-gemini-sse-to-openai-finish-reason/{proposal,design,plan}.md` 为修复计划。

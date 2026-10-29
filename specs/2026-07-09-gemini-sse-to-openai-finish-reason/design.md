# Design

复用现有 `pkg/llmbridgeimpl.BridgeStream` 入口，仅修复 vendored axonhub 中 OpenAI Chat Completions 入站流的“纯推理签名事件”过滤逻辑。

## 转换链路（Gemini SSE → OpenAI Chat Completions）

`OpenStream(src=OpenAIChatCompletions, upstream=GeminiStreamGenerateContent)`：

1. `outboundFor(Gemini)` 的 `TransformStream` 把 Gemini SSE 解析为内部 `llm.Response` 流（`gemini/outbound_stream.go` → `convertGeminiToLLMResponseWithState`）。第二个事件产出一个 `Choice`：`Delta` 仅含 `ReasoningSignature`、空 `content`，`FinishReason = "stop"`，`Usage` 来自 `usageMetadata`。
2. `inboundFor(OpenAIChatCompletions)` 的 `TransformStreamChunk`（`openai/inbound.go`）把 `llm.Response` 转成 OpenAI SSE chunk。它先用 `isReasoningSignatureEvent` 过滤“纯签名事件”，命中则返回 `nil` 被 `streams.NoNil` 丢弃。

## 根因定位

`isReasoningSignatureEvent` 仅检查 `content / reasoning_content / tool_calls / refusal`，未考虑 `finish_reason`。第二个 Gemini 事件四项皆空，被判为纯签名事件 → 整段丢弃 → `finish_reason` 与 `usage` 丢失。

## 修复方案

在 `isReasoningSignatureEvent` 中读取 `resp.Choices[0].FinishReason`，当非空时视为“有其它内容”，不再跳过：

```go
hasFinishReason := resp.Choices[0].FinishReason != nil && *resp.Choices[0].FinishReason != ""
return !hasContent && !hasReasoningContent && !hasToolCalls && !hasRefusal && !hasFinishReason
```

### 为什么安全

- 只有“纯签名且带 finish_reason”的事件从 跳过 → 保留 改变。`usageMetadata` 现随终端 chunk 正常带出。
- `ResponseFromLLM` → `MessageFromLLM` 不会把 `ReasoningSignature` 写进 OpenAI `Message`（该结构无此字段），故终端 chunk 为干净空 `delta` + `finish_reason:"stop"`，无签名泄漏。
- 其余纯签名事件（无 finish_reason）仍被跳过，行为与修复前一致。

## 测试

- 端到端（`pkg/llmbridgeimpl/bridge_stream_test.go`）：基于 `fixtures/gemini.sse`，断言 `finish_reason == "stop"`。
- 单元（`third_party/axonhub/llm/transformer/openai/inbound_reasoning_test.go`）：新增“纯签名 + finish_reason”用例，期望不被跳过。

不引入第三方库、不改 `go.mod` 依赖、不新增 fixture。

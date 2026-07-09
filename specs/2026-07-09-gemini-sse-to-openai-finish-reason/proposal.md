# Proposal: 修复 Gemini SSE → OpenAI Chat Completions 流式转换丢失 finish_reason

基于 `fixtures/gemini.sse`（Gemini `streamGenerateContent` 真实 SSE）的桥接测试发现一个转换 bug：Gemini 流式的**最后一个事件**被整体丢弃，导致 OpenAI Chat Completions 流完全没有 `finish_reason`，且 `usage` 也一并丢失。

## 现象

`fixtures/gemini.sse` 的第二个 Gemini 事件：

```json
{"candidates":[{"content":{"role":"model","parts":[{"text":"","thoughtSignature":"AAAAAAA=="}]},"finishReason":"STOP"}],
 "usageMetadata":{"promptTokenCount":2101,"candidatesTokenCount":60,"totalTokenCount":2389,"thoughtsTokenCount":228,...}}
```

转换后只产出一段文本 chunk + `[DONE]`，`finish_reason` 缺失（用例 `TestBridgeStreamGeminiToOpenAIChatFinishReason` 失败）。

## 根因

`third_party/axonhub/llm/transformer/openai/inbound.go` 的 `isReasoningSignatureEvent` 会跳过“只包含 ReasoningSignature”的事件（OpenAI 格式无法表示推理签名）。该 Gemini 事件空文本 + 仅 `thoughtSignature` + `finishReason:STOP`，被判定为“纯签名事件”而整段丢弃，于是 `finish_reason`（和 `usage`）随之丢失。

注：`third_party/axonhub/llm` 是本仓库通过 `go.mod` replace 维护的 vendored 副本（见 `THIRD_PARTY_NOTICES.md`），可直接在此修复，不引入新依赖。

## 修复

在 `isReasoningSignatureEvent` 中增加判定：携带 `finish_reason` 的纯签名事件**不再跳过**，使 OpenAI 终端 chunk 正常携带 `finish_reason:"stop"`（及其 `usage`）。空 delta 经 `ResponseFromLLM` 转换时不会泄漏 `reasoning_signature`（OpenAI `Message` 结构无此字段）。

## 验证

- 新增/保留 `pkg/llmbridgeimpl/bridge_stream_test.go::TestBridgeStreamGeminiToOpenAIChatFinishReason`：基于 `fixtures/gemini.sse` 桥接到 OpenAI Chat Completions，断言流中存在 `chat.completion.chunk` 且所有非空 `finish_reason` 均为 `"stop"`。
- 在 vendored `openai/inbound_reasoning_test.go` 增加单测：纯签名 + `finish_reason:"stop"` 不应被跳过。
- 运行：`go test ./pkg/llmbridgeimpl/` 与 `go test github.com/looplj/axonhub/llm/transformer/openai`（全绿）。

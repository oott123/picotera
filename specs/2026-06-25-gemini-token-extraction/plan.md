# 执行计划：Gemini 响应 token 用量提取

仅涉及 `pkg/server/response_extractor.go` 及其测试。不改 bridge、不改 axonhub、无 API / DB / 前端变更。

## 步骤 1：写测试（红）

在 `pkg/server/response_extractor_test.go` 末尾新增 Gemini 用例，复用既有 `sseFromLines` / 直接构造 `strings.NewReader` 的写法（参照文件内现有 helper）：

1. `TestResponseExtractor_SSE_Gemini_Usage`
   - 输入：三段 `data:` SSE。前两块 `usageMetadata` 仅 `{"trafficType":"ON_DEMAND"}`，第三块带 `promptTokenCount:8,candidatesTokenCount:11,totalTokenCount:19` 与 `finishReason:"STOP"`，每块 `modelVersion:"google/gemini-2.5-flash-lite"`。
   - 断言：`*InputTokens==8`、`*OutputTokens==11`、`TTFTMs!=nil`、`InferredModel=="google/gemini-2.5-flash-lite"`、`InferredModelSource==db.InferredModelSourceResponse`。
2. `TestResponseExtractor_SSE_Gemini_EarlyUsageMetadataIgnored`
   - 输入：仅前两块（无计数）。
   - 断言：`InputTokens==nil` 且 `OutputTokens==nil`（不被写 0）。
3. `TestResponseExtractor_JSON_Gemini`
   - 输入：非流式 Gemini JSON（`promptTokenCount:8,candidatesTokenCount:10`）。
   - 断言：`*InputTokens==8`、`*OutputTokens==10`、`InferredModel` 正确。
4. `TestResponseExtractor_SSE_Gemini_CachedAndThoughts`
   - 末块 `usageMetadata` 含 `promptTokenCount:100,cachedContentTokenCount:40,candidatesTokenCount:11,thoughtsTokenCount:5`。
   - 断言：`*InputTokens==60`、`*OutputTokens==16`、`*CacheReadTokens==40`。

运行 `go test ./pkg/server/ -run TestResponseExtractor_.*Gemini -v`，确认全部失败（token 为 nil）。

## 步骤 2：实现 Gemini SSE 提取

在 `response_extractor.go` 新增 `func (e *ResponseExtractor) extractGeminiSSE(payload string)`：

- TTFT：`if !e.ttftRecorded && result.Get("candidates.0.content.parts").Exists()` → 记录 `time.Since(e.startTime)`。
- Usage：取 `usageMetadata`；存在时：
  - `promptTokenCount` 存在 → `in := promptTokenCount`；若 `cachedContentTokenCount` 存在且 > 0 → `in -= cached`，并设 `CacheReadTokens=cached`；设 `InputTokens=&in`。
  - `candidatesTokenCount` 存在 → `out := candidatesTokenCount + thoughtsTokenCount`（`thoughtsTokenCount` 不存在按 0）；设 `OutputTokens=&out`。
- 仅对 `Exists()` 的字段赋值；不存在不写。

在 `processSSEEvent` 的 `extractAnthropicSSE(payload)` 之后追加 `e.extractGeminiSSE(payload)`。

## 步骤 3：实现 Gemini JSON 提取

在 `extractJSONMetrics` 现有 Anthropic 回退分支之后追加 Gemini 回退（仅在指标仍为 `nil` 时）：

- `e.metrics.InputTokens == nil`：读 `usageMetadata.promptTokenCount`，减 `usageMetadata.cachedContentTokenCount`（存在时）。
- `e.metrics.OutputTokens == nil`：读 `usageMetadata.candidatesTokenCount`，加 `usageMetadata.thoughtsTokenCount`（存在时）。
- `e.metrics.CacheReadTokens == nil`：读 `usageMetadata.cachedContentTokenCount`（> 0 时）。

可抽一个 `setGeminiUsage(usage gjson.Result)` 辅助函数供 SSE 与 JSON 复用，减少重复。

## 步骤 4：模型推断加入 modelVersion

`inferModelField` 的路径数组改为 `[]string{"model", "message.model", "modelVersion"}`。

## 步骤 4.5：修 CRLF 事件分隔（根因 B，线上请求暴露）

> 此步在用线上请求 `d8u8avgs9a2fohr5kevg` 验证时新增：仅做步骤 1–4，该请求仍提取不到 token，因 Google Gemini 用 CRLF（`\r\n\r\n`）框架，`processSSEBuffer` 只认 `"\n\n"`，事件永远切不出来。

在 `Read` 的 `case "sse"` 分支，把 `chunk` 写入 `lineBuf` 时剥除裸 `\r`（逐字节过滤，仅作用于解析缓冲；转发给客户端的 `p` 不动）。`processSSEBuffer` / `processSSEEvent` 不改。

新增回归测试：
- `TestResponseExtractor_SSE_Gemini_CRLFFraming`：CRLF 框架 Gemini 流，断言 token / TTFT / 模型提取成功。
- `TestResponseExtractor_SSE_CRLF_BytesForwardedUnchanged`：断言转发字节含 CR 原样不变。

## 步骤 5：验证

- `go test ./pkg/server/ -run TestResponseExtractor -v`：步骤 1 的 Gemini 用例转绿，既有 OpenAI/Anthropic 用例全绿（无回归）。
- `go build ./...`。
- `go test ./pkg/server/ ./pkg/llmbridgeimpl/`。

## 不做的事

- 不改 `pkg/llmbridge*` 与 `third_party/axonhub`（实验证明桥接输出已正确）。
- 不处理无 `alt=sse` 的 JSON 数组流（由 `2026-06-25-unified-gemini-alt-sse` 负责）。
- 不引入任何兼容层 / 宽松归一化（遵循 `CLAUDE.md` 工作约定）。

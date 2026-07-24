# 执行计划

全部改动集中在 `pkg/server/response_extractor.go` 与其测试 `pkg/server/response_extractor_test.go`。

## 1. 改造 `setOpenAIInputTokens`

`pkg/server/response_extractor.go`（当前 672–692 行）。改为：

- 选定前缀：`prompt_tokens` + `prompt_tokens_details`（Chat Completions），缺失回退 `input_tokens` + `input_tokens_details`（Responses）；两者皆无则 `return`。
- 读 `cached_tokens`：存在则设 `e.metrics.CacheReadTokens` 并从 total 扣除。
- 读 `cache_write_tokens`：存在则设 `e.metrics.CacheWriteTokens` 并从 total 扣除。
- 将净 total 写入 `e.metrics.InputTokens`。

保持“字段不存在即不写、不做 0 兜底”的语义。

## 2. 清理 SSE 两个入口的重复 CacheRead 提取

- `extractOpenAISSE`（当前 257–260 行）：删除 `prompt_tokens_details.cached_tokens` → `CacheReadTokens` 块。
- `extractOpenAIResponsesSSE`（当前 291–294 行）：删除 `input_tokens_details.cached_tokens` → `CacheReadTokens` 块。

两处的 `setOpenAIInputTokens(usage)` 调用保留，`OutputTokens` 提取保留。

## 3. 清理 `extractJSONMetrics` 的重复提取

`pkg/server/response_extractor.go`（当前 480–567 行）：

- 删除 Chat Completions 分支的 `usage.prompt_tokens_details.cached_tokens` → `CacheReadTokens` 块（495–498 行）。
- 删除 Responses 分支中第二次冗余的 `setOpenAIInputTokens(usage)` 调用（501–503 行）与 `input_tokens_details.cached_tokens` → `CacheReadTokens` 块（510–515 行）。
- 保留开头唯一一次 `setOpenAIInputTokens(usage)`、`OutputTokens` 提取，以及 Anthropic / Gemini 的 `CacheReadTokens == nil` 回退分支。

## 4. 补充测试

`pkg/server/response_extractor_test.go`：

- Chat Completions（SSE 与非流式 JSON 各一）：`usage` 含 `prompt_tokens`、`prompt_tokens_details.{cached_tokens, cache_write_tokens}`，断言 `InputTokens == prompt_tokens - cached - cache_write`、`CacheReadTokens`、`CacheWriteTokens`。
- Responses（SSE `response.completed` 与非流式 JSON 各一）：`usage` 含 `input_tokens`、`input_tokens_details.{cached_tokens, cache_write_tokens}`，断言同上（net input、read、write）。
- 回归：现有不含 `cache_write_tokens` 的 OpenAI 用例（如 `TestResponseExtractor_SSE_OpenAIResponses_UsageAndTTFT`、`TestResponseExtractor_JSON_OpenAIResponses`）断言不变，`CacheWriteTokens` 应仍为 `nil`——按需补一条对 `nil` 的显式断言。

## 5. 验证

- `go test ./pkg/server/` 通过。
- 无需 `sqlc generate` / `mise run openapi` / 前端类型再生：不涉及 SQL、契约或 API 变更。

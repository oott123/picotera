# 设计

## 背景

`pkg/server/response_extractor.go` 的 `ResponseExtractor` 在响应流经时提取 token 用量。`ResponseMetrics` 已含 `CacheWriteTokens *int64` 字段，且下游通路（`gateway_flow_success.go` → `requestUpdate.CacheWriteTokens` → DB；`pricing.go` 的 `computeCost` 按 `tier.CacheWrite` 单独计费）均已就绪。当前仅 Anthropic 会填充 `CacheWriteTokens`（`cache_creation`），OpenAI 两种格式均未提取缓存写入。

计费模型确认：`computeCost` 中 input tokens 按 `tier.Input` 计费、cache write 按 `tier.CacheWrite` 单独计费，因此 input tokens 应为**净值**（既扣除 cache read，也扣除 cache write）。这与现有 `setOpenAIInputTokens` 已从 input 中扣除 `cached_tokens` 的做法一致。

## OpenAI token 提取现状

三个入口处理 OpenAI 用量：

- `extractOpenAISSE`（Chat Completions SSE）：调 `setOpenAIInputTokens` 设 input；单独读 `prompt_tokens_details.cached_tokens` 设 `CacheReadTokens`。
- `extractOpenAIResponsesSSE`（Responses SSE，`response.completed` 事件）：调 `setOpenAIInputTokens`；单独读 `input_tokens_details.cached_tokens` 设 `CacheReadTokens`。
- `extractJSONMetrics`（非流式 JSON 缓冲）：调 `setOpenAIInputTokens`；分别为 Chat Completions（`prompt_tokens_details.cached_tokens`）与 Responses（`input_tokens_details.cached_tokens`）设 `CacheReadTokens`。

`setOpenAIInputTokens` 是唯一同时知道该用 `prompt_tokens*` 还是 `input_tokens*` 前缀的函数：它先取 `prompt_tokens`（Chat Completions），缺失则回退到 `input_tokens`（Responses），并从中减去对应的 `cached_tokens` 得到净 input。

## 方案

将缓存读取/写入的提取统一收敛进 `setOpenAIInputTokens`，让它成为唯一负责 OpenAI 输入侧 token（input / cache read / cache write）的地方，消除三个入口里重复的 `CacheReadTokens` 读取。

改造后的 `setOpenAIInputTokens`：

1. 确定前缀：先试 `prompt_tokens` + `prompt_tokens_details`（Chat Completions），缺失则回退 `input_tokens` + `input_tokens_details`（Responses）。`prompt_tokens`/`input_tokens` 均不存在则直接返回（不写任何字段，保持对 Anthropic/Gemini JSON 的无害回退）。
2. 从 details 读取 `cached_tokens` → 设 `CacheReadTokens`，并从 total 中减去。
3. 从 details 读取 `cache_write_tokens` → 设 `CacheWriteTokens`，并从 total 中减去。
4. 将净值写入 `InputTokens`。

字段缺失即不写：上游未返回 `cache_write_tokens` 时 `CacheWriteTokens` 保持 `nil`；未返回 `cached_tokens` 时 `CacheReadTokens` 保持 `nil`。均不做 0 兜底。

相应地：

- `extractOpenAISSE` / `extractOpenAIResponsesSSE`：删除各自单独的 `CacheReadTokens` 提取块（已并入 `setOpenAIInputTokens`）。
- `extractJSONMetrics`：删除 Chat Completions 与 Responses 两处单独的 `CacheReadTokens` 提取块，以及第二次冗余的 `setOpenAIInputTokens` 调用（该函数本身已覆盖两种前缀，一次调用即可）。Anthropic / Gemini 的 `CacheReadTokens == nil` 回退分支保持不变——OpenAP 前缀不存在时 `setOpenAIInputTokens` 不写任何字段，回退分支照常生效。

## 不做的事

- 不改 `ResponseMetrics` 结构、DB schema、计费逻辑、`request_update` 通路——`CacheWriteTokens` 通路已存在。
- 不触碰 Anthropic（`cache_creation` / `CacheWrite1HTokens`）与 Gemini 的提取逻辑。
- 不引入兼容层或对旧字段名的宽松匹配。

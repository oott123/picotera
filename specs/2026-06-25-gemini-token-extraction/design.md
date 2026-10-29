# 设计：Gemini 响应 token 用量提取

## 根因分析（已实验验证）

### 记录侧 token 的来源

网关记录到 `request` 行的 token 列（`input_tokens` / `output_tokens` / `cache_read_tokens` / `cache_write_tokens` / TTFT）来自 `pkg/server/response_extractor.go` 的 `ResponseExtractor`：

- path 路由：`pipePathResponse`（`gateway_flow_success.go:182`）用 extractor 包裹**上游原始响应**。
- unified 路由：`unifiedStreamSuccess`（`gateway_unified_helpers.go:444`）同样用 extractor 读**上游原始格式**字节，注释明确："metrics 来自上游 native 格式，与是否桥接无关"。unified 的流式与非流式都走这一个入口（`handle_unified_gateway.go:55` 的 `HandleSuccess` 恒调用 `unifiedStreamSuccess`），都用 `extractor.Metrics()`（`gateway_unified_helpers.go:571`）。

### `ResponseExtractor` 缺 Gemini 支持

`response_extractor.go` 只实现了 OpenAI Chat、OpenAI Responses、Anthropic 三种格式：

- SSE：`extractOpenAISSE` / `extractOpenAIResponsesSSE` / `extractAnthropicSSE`，均读 `usage.*`。
- JSON：`extractJSONMetrics`，依次尝试 `usage.prompt_tokens` / `usage.input_tokens` / `usage.cache_*`。
- 模型推断 `inferModelField` 只看 `model` / `message.model`。

Gemini 的 token 在 `usageMetadata.{promptTokenCount,candidatesTokenCount,totalTokenCount,cachedContentTokenCount,thoughtsTokenCount}`，模型名在 `modelVersion`，都不被任何现有分支命中。实验直接喂 Gemini SSE 与 JSON 给 extractor，`InputTokens`/`OutputTokens`/`TTFTMs`/`InferredModel` 全为 `nil`/空。

**这是问题 1 与问题 2 记录侧的根因 A。**

### `processSSEBuffer` 不识别 CRLF 事件分隔（根因 B，线上请求暴露）

仅修根因 A 仍不够。用线上真实请求 `d8u8avgs9a2fohr5kevg` 的上游原生响应字节（从 S3 artifact 取出）验证，修复后仍全为空。`cat -A` 显示 Google 的 Gemini 接口用 **CRLF**（`\r\n\r\n`）做 SSE 事件分隔：

```
data: {...}^M$
^M$
```

而 `processSSEBuffer`（`response_extractor.go`）用 `bytesIndex(e.lineBuf, "\n\n")` 切事件。`\r\n\r\n` 的字节是 `0d 0a 0d 0a`，其中**没有** `0a 0a` 子串，故事件边界永远找不到，整条流堆在 `lineBuf` 里从未被 `processSSEEvent` 处理。即使后续加了 Gemini 字段识别，解析器也走不到那一步。

- 把同一段字节 `\r\n` → `\n` 后，根因 A 的代码立即提取出 `Input=9 / Output=10 / TTFT / Model=gemini-2.5-flash-lite`，证明两个根因正交。
- 这不是 Gemini 专属 bug：任何 CRLF 框架的上游 SSE 都受影响。OpenAI / Anthropic 上游用 LF，所以既有用例从未触发。

**根因 A（字段）与根因 B（框架）都必须修，缺一则该请求仍提取失败。**

### bridge 输出本身正确（无需改动）

实验用本节给出的真实 Gemini SSE，经 `BridgeStream` 转换到三种源格式，客户端收到的 usage 均正确：

- → OpenAI Chat：末块 `usage:{prompt_tokens:8,completion_tokens:11,total_tokens:19}`。
- → OpenAI Responses：`response.completed` 的 `usage:{input_tokens:8,output_tokens:11,total_tokens:19}`。
- → Anthropic：`message_delta` 的 `usage:{input_tokens:8,output_tokens:11}`。
- 非流式三种源格式 usage 同样正确；`AggregateStream` 聚合后的 Gemini 体 `totalTokenCount=19` 正确。

axonhub 的 Gemini outbound 在带 `finishReason` 的末块上调用 `convertGeminiToLLMResponseWithState`，`resp.Usage` 被正确填充（`outbound_convert.go:662`），下游各源格式 inbound 把 usage 透传到末事件。因此**桥接链路不需要修改**。

### 旁注：无 `alt=sse` 的 JSON 数组流

实验显示上游若以 JSON 数组（无 `alt=sse`）返回流，bridge 仅输出 `data: [DONE]`，内容与 usage 全丢。该问题由既有的 `2026-06-25-unified-gemini-alt-sse`（注入 `alt=sse`）解决，**不属于本次范围**。

## 方案

仅改 `pkg/server/response_extractor.go`，同时修两个根因。不改 bridge、不改 axonhub。

### 修根因 B：CRLF 框架（`Read` 的 SSE 分支剥除裸 CR）

在 `Read` 把字节写入 `lineBuf` 时剥除裸 `\r`（仅作用于**解析缓冲**，转发给客户端的 `p` 字节完全不动，原样含 CR 透传）。依据 SSE 规范：行分隔符可为 CR / LF / CRLF，且 data 字段值不能含裸 CR，故从解析缓冲剥除 CR 是无损的——`\r\n\r\n` 归一为 `\n\n`，行内 `data: {...}\r` 归一为干净 `data: {...}`（`[DONE]` 等精确比较也因此不再被尾随 `\r` 破坏）。这样 CRLF 与 LF 统一，`processSSEBuffer` / `processSSEEvent` 无需再改。

### Token 语义（与 axonhub `convertToLLMUsage` 及 picotera 既有约定对齐）

Gemini `promptTokenCount` 含缓存命中部分；picotera 既有约定（`setOpenAIInputTokens`）是 `InputTokens = 总输入 - 缓存`，缓存单列。因此 Gemini 映射为：

| picotera 指标 | Gemini 字段 |
| --- | --- |
| `InputTokens` | `promptTokenCount - cachedContentTokenCount` |
| `OutputTokens` | `candidatesTokenCount + thoughtsTokenCount` |
| `CacheReadTokens` | `cachedContentTokenCount`（存在且 > 0 时） |
| `InferredModel` | `modelVersion`（来源 `response`） |
| `TTFTMs` | 首个含 `candidates.0.content.parts` 的 SSE 块 |

Gemini 无缓存写入概念，`CacheWriteTokens` / `CacheWrite1HTokens` 不设置。

### SSE 提取（`extractGeminiSSE`）

新增方法，在 `processSSEEvent` 中与其它 `extract*SSE` 并列调用：

- TTFT：未记录且 `candidates.0.content.parts` 存在（含文本或任意 part）时记录。
- Usage：`usageMetadata` 存在时，仅对**实际存在**的字段赋值（`gjson` `Exists()` 判定）。Gemini 早期块的 `usageMetadata` 只有 `trafficType` 没有 token 计数，必须跳过、不得写 0；末块带计数时覆盖写入（与 Anthropic `message_delta` 覆盖 `output_tokens` 的既有行为一致，末值生效）。

### JSON 提取（`extractJSONMetrics` 增加 Gemini 分支）

在现有 OpenAI/Anthropic 分支之后，对仍为 `nil` 的指标尝试 `usageMetadata.*`：

- `InputTokens == nil` 时读 `usageMetadata.promptTokenCount`（减 `cachedContentTokenCount`）。
- `OutputTokens == nil` 时读 `usageMetadata.candidatesTokenCount`（加 `thoughtsTokenCount`）。
- `CacheReadTokens == nil` 时读 `usageMetadata.cachedContentTokenCount`。

保持"已被前面格式命中则不覆盖"的既有惯例，确保对 OpenAI/Anthropic 响应零行为变化。

### 模型推断（`inferModelField` 增加 `modelVersion`）

把 `modelVersion` 加入候选路径：`["model", "message.model", "modelVersion"]`，首个非空命中并锁定。对非 Gemini 响应无影响（它们没有 `modelVersion`）。

### 错误检测

`detectStreamError` 已覆盖 Gemini 的 `error.message` 形态，无需改动。

## 测试

新增 Gemini 用例到 `pkg/server/response_extractor_test.go`：

1. `TestResponseExtractor_SSE_Gemini_Usage`：用本设计的三段式 SSE（前两块 `usageMetadata` 无计数、末块有计数 + `finishReason`），断言 `InputTokens=8`、`OutputTokens=11`、`TTFTMs` 已记录、`InferredModel="google/gemini-2.5-flash-lite"`。
2. `TestResponseExtractor_SSE_Gemini_EarlyUsageMetadataIgnored`：仅含前两块（无计数），断言 token 仍为 `nil`（不被写 0）。
3. `TestResponseExtractor_JSON_Gemini`：非流式体，断言 `InputTokens=8`、`OutputTokens=10`、`InferredModel` 正确。
4. `TestResponseExtractor_SSE_Gemini_CachedAndThoughts`：构造含 `cachedContentTokenCount` 与 `thoughtsTokenCount` 的末块，断言 `InputTokens = prompt - cached`、`OutputTokens = candidates + thoughts`、`CacheReadTokens = cached`。
5. `TestResponseExtractor_SSE_Gemini_CRLFFraming`（根因 B 回归）：CRLF（`\r\n\r\n`）框架的 Gemini 流、每块重复累计 `usageMetadata`，断言 token / TTFT / 模型都提取成功。
6. `TestResponseExtractor_SSE_CRLF_BytesForwardedUnchanged`：断言剥除 CR 仅作用于解析，转发给客户端的字节（含 CR）byte-for-byte 不变。

这些用例在修复前应失败（token 为 `nil`），修复后通过。既有 OpenAI/Anthropic 用例必须保持通过，验证无回归。另用线上真实请求 `d8u8avgs9a2fohr5kevg` 的上游 artifact 原始 CRLF 字节跑过当前 extractor，确认提取出 `Input=9 / Output=10 / Model=gemini-2.5-flash-lite`。

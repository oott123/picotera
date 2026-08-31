# 设计：把 Anthropic `refusal` 记为流式错误

## 背景

Anthropic Messages 在拒绝出内容时会返回 HTTP 200，并在 `message_delta` 事件里给出 `stop_reason: "refusal"`：

```text
event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"reasoning_extraction","explanation":"This request was blocked as it seems to violate Anthropic's Terms of Service ..."}},"usage":{...,"output_tokens":0}}
```

`ResponseExtractor.detectStreamError`（`pkg/server/response_extractor.go`）目前只认三种形状：`response.error.message`、`error.message`，以及 `choices.0.finish_reason ∈ {network_error, model_context_window_exceeded}`。上面这个 payload 一种都不命中，于是 `StreamError()` 为空，`classifyStreamFinishReason` 按读循环正常收尾返回 `db.FinishReasonEOF`，meta 行与上游行都被记成「正常结束」，`error_message` 为 NULL。

非流式路径更彻底：`extractJSONMetrics` 根本不做任何错误检测，Anthropic 非流式响应体顶层的 `stop_reason: "refusal"` 同样被记成正常结束。

## 设计

`detectStreamError` 是「什么算流式错误」的唯一权威——它的文档注释逐格式枚举错误形状。refusal 是这张表上的又一行，因此 SSE 侧的判定就加在 `detectStreamError` 里，紧跟两个 `*error.message` 分支之后、`choices.0.finish_reason` 分支（带 early return）之前。

判定本身抽成一个共享方法，因为非流式路径要用同一套语义但字段路径不同：

```go
// detectRefusal records an Anthropic refusal stop_reason as a stream error.
// stopReason / stopDetails are the payload's stop_reason field and its sibling
// stop_details object — delta.* in a streaming message_delta, top-level in a
// non-stream body. First error wins, matching detectStreamError.
func (e *ResponseExtractor) detectRefusal(stopReason, stopDetails gjson.Result) {
	if e.streamError != "" {
		return
	}
	if stopReason.Type != gjson.String || stopReason.String() != "refusal" {
		return
	}
	if v := stopDetails.Get("explanation"); v.Type == gjson.String && v.String() != "" {
		e.streamError = v.String()
		return
	}
	e.streamError = "refusal"
}
```

两个调用点：

- **SSE**：`detectStreamError` 内部，`e.detectRefusal(gjson.Get(payload, "delta.stop_reason"), gjson.Get(payload, "delta.stop_details"))`。不按事件类型 gating：顶层 `delta` 是 Anthropic 独有形状（OpenAI Chat 的 `delta` 嵌在 `choices[]` 下，Gemini 没有 `delta`），不会误伤其他格式。`detectStreamError` 也被 Gemini JSON 数组路径调用，多两次落空的 `gjson.Get` 无害。
- **非流式 JSON**：`extractJSONMetrics` 里，用顶层 `result.Get("stop_reason")` / `result.Get("stop_details")` 调用同一方法。这里**只**加 refusal 判定，不整个调 `detectStreamError`——`error.message` / `choices.0.finish_reason` 在非流式体上的语义不在本次需求范围内。

匹配严格：`stop_reason` 必须是 JSON 字符串且严格等于 `refusal`，不做大小写折叠、不做空白修剪、不接受近似值。`stop_details.category` 不参与判断——任意 refusal 都算。

`error_message` 取 `stop_details.explanation`；`stop_details` 缺失或 `explanation` 为空时写字面量 `refusal`（`stop_details` 在 Anthropic 契约里是可选的，这是输出兜底，不是对输入的宽容解析）。

## 记录链路（无需改动）

`streamError` 非空后，路径网关 `completeGatewaySuccess`（`gateway_flow_success.go`）与统一网关 `unifiedStreamSuccess`（`gateway_unified_helpers.go`）两处已有的同一段分支自动生效：

- meta 行与上游行的 `finish_reason` 被覆盖为 `db.FinishReasonStreamError`（6）；
- `error_message` 写入 refusal 说明文本；
- `status_code` 保持上游真实值 200；
- token / TTFT / 成本 / 模型推断 / artifact 聚合全部照常记录（refusal 依然消耗了 cache read token，必须计费）；
- `runStreamErrorHook` 以 `streamed=true` 执行 `afterUpstreamError` hook。

`streamed=true` 对两条路径都成立：`StreamError()` 是在响应体已经全量转发给下游之后才被读取的，此时 `break` 无论如何都无法生效，hook 纯观测。

统一网关里 extractor 包的是上游原生字节，所以无论下游是 Anthropic、OpenAI 还是 Gemini 格式，只要上游是 Anthropic 就能识别。

## 统计口径

`finish_reason = 6` 不等于 `3`，`request_outcome_bucketed` 支撑的成功率统计自动把这类请求计为失败。refusal 响应的 `output_tokens` 为 0，此前它已经落在「空回复」里；本次改动后它同时带上正确的完成原因，两个口径不再矛盾。

dashboard 侧 `finishReasonLabel` 与请求列表筛选项已经包含「流式错误」，无需改动。

## 不做的事

- 不新增 finish reason 常量，不改 `db/migrations/`、`pkg/db/`、OpenAPI 契约或 dashboard 代码。
- 不把 refusal 当作可重试失败——响应已经流给下游，故障转移在这个时点不可能发生。
- 不解析 `fallback_credit_token`、`fallback_has_prefill_claim`、`context_management` 等其他 refusal 字段。
- 不识别其他厂商的拒答形状（本次只覆盖 Anthropic `stop_reason`）。
- 不新增兼容层、配置开关或第三方依赖。

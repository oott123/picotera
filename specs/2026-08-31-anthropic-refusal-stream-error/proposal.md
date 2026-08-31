# 原始需求

对如下这种响应，完成原因也需要记录为服务器错误

```text
event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"refusal","stop_sequence":null,"stop_details":{"type":"refusal","category":"reasoning_extraction","explanation":"This request was blocked as it seems to violate Anthropic's Terms of Service restrictions on reverse engineering or duplicating model outputs. To learn more, visit https://www.anthropic.com/legal/commercial-terms.","fallback_credit_token":"EuM....=","fallback_has_prefill_claim":false}},"usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":244090,"output_tokens":0,"output_tokens_details":{"thinking_tokens":0},"iterations":[{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":244090,"cache_creation_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0},"type":"message"}]},"context_management":{"applied_edits":[]}}
```

现在是记录为正常结束的。

# 规划期澄清

- **落到哪个 finish reason**：复用既有的 `db.FinishReasonStreamError`（6，dashboard 显示「流式错误」）。不新增 finish reason 常量，不改 dashboard 标签与筛选项。
- **识别范围**：SSE 与非流式 JSON 两条路径都要识别。
  - SSE：Anthropic `message_delta` 事件的 `delta.stop_reason == "refusal"`。
  - 非流式 JSON：Anthropic 响应体顶层 `stop_reason == "refusal"`。
  - 不看 `stop_details.category`——任意 `refusal` 都算，包括模型正常拒答。

# 为请求表增加推测供应商、推测服务模型字段

为 `request` 表增加两个字段：**推测供应商**（inferred provider）、**推测服务模型**（inferred model）。两者从上游响应字节中推测得出。

## 推测供应商规则

按以下规则推测，任一命中即可，先命中者为准（一旦得出不再覆盖）：

1. 若响应 chunk 中有 `provider` 字段，取该字段值为推测供应商。例如：

   ```
   data: {"id":"gen-1781532803-NteerNkhKXSBOuOvJtfj","object":"chat.completion.chunk","created":1781532803,"model":"nvidia/nemotron-3-ultra-550b-a55b-20260604:free","provider":"Nvidia","choices":[{"index":0,"delta":{"content":"","role":"assistant","reasoning":"The","reasoning_details":[{"type":"reasoning.text","text":"The","format":"unknown","index":0}]},"finish_reason":null,"native_finish_reason":null}]}
   ```

   推测供应商为 `Nvidia`。

2. 若 message id 以 `msg_bdrk_` 为前缀，取 `Amazon Bedrock` 为供应商。例如：

   ```
   data: {"message":{"content":[],"id":"msg_bdrk_7xxvzn3y5guhlrkyxno2katnow5gsvlkk7hu3x3ddo6nwhily4ra","model":"claude-opus-4-7","role":"assistant",...,"type":"message"},"type":"message_start"}
   ```

3. 若 `message_stop` 事件中有 `amazon-bedrock-invocationMetrics` 字段，取 `Amazon Bedrock` 为供应商。例如：

   ```
   event: message_stop
   data: {"type":"message_stop","amazon-bedrock-invocationMetrics":{"inputTokenCount":100,"outputTokenCount":91,"invocationLatency":3419,"firstByteLatency":2464,"cacheReadInputTokenCount":50122,"cacheWriteInputTokenCount":210}}
   ```

## 推测服务模型规则

1. 若响应中有思维签名，例如：

   ```
   data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"..."}}
   ```

   尝试将思维签名经 base64 解码后过 protobuf 解码，检查 `[2][1][6]` 字段（字段号 2 → 1 → 6 的嵌套路径）。若该字段为 string 且解码出的内容全为 ASCII 字符，取该字符串为推测服务模型。

2. 若响应 chunk 中有 `model` 字段，取该字段值为推测服务模型。

当两个来源都存在时，**以签名解码结果优先**，签名缺失或解码失败时回退到 `model` 字段。

## 确认的设计决策

- **推测模型优先级**：签名解码结果优先于 `model` 字段。
- **写入范围**：推测供应商/模型与现有 token、finish_reason 等指标一致，同时写入 meta 行与 upstream 行。
- **前端展示**：在请求详情页展示这两个新字段。

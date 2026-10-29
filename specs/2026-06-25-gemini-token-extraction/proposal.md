# Gemini 响应 token 用量提取

## 原始需求

Gemini 流式响应形如（`alt=sse` 形式）：

```
data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}],"usageMetadata":{"trafficType":"ON_DEMAND"},"modelVersion":"google/gemini-2.5-flash-lite","createTime":"...","responseId":"cAY8..."}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":" there! How"}]}}],"usageMetadata":{"trafficType":"ON_DEMAND"},"modelVersion":"google/gemini-2.5-flash-lite","createTime":"...","responseId":"cAY8..."}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":" can I help you today? 😊"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":11,"totalTokenCount":19,"trafficType":"ON_DEMAND","promptTokensDetails":[{"modality":"TEXT","tokenCount":8}],"candidatesTokensDetails":[{"modality":"TEXT","tokenCount":11}]},"modelVersion":"google/gemini-2.5-flash-lite","createTime":"...","responseId":"cAY8..."}
```

非流式响应形如：

```json
{
  "candidates": [{"content": {"role": "model","parts": [{"text": "Hello there! How can I help you today?"}]},"finishReason": "STOP","avgLogprobs": -0.0417}],
  "usageMetadata": {"promptTokenCount": 8,"candidatesTokenCount": 10,"totalTokenCount": 18,"trafficType": "ON_DEMAND","promptTokensDetails": [{"modality": "TEXT","tokenCount": 8}],"candidatesTokensDetails": [{"modality": "TEXT","tokenCount": 10}]},
  "modelVersion": "google/gemini-2.5-flash-lite","responseId": "iwY8..."
}
```

存在两个问题：

1. 记录请求时无法从 Gemini 响应中提取出 token 用量。
2. 经过 llm bridge 跨格式转换后，流式响应记录到的 usage token 为 0 而非正确数量；非流式情况下 token 是对的。

## 任务

1. 写测试验证这两个问题。
2. 给出修复方案。

## 验证结论（补充）

经实验验证，token 提取失败有**两个独立根因**，缺一不可修复：

### 根因 A：`ResponseExtractor` 缺 Gemini 格式支持

`pkg/server/response_extractor.go` 的 `ResponseExtractor` 完全没有 Gemini 格式支持，对 Gemini 的 SSE 与 JSON 响应不识别 `usageMetadata` / `modelVersion`。

- 网关记录的 token 列（`input_tokens` / `output_tokens` 等）来自 `ResponseExtractor`，它读取的是**上游原始格式**（path 路由与 unified 路由皆然），因此上游为 Gemini 时记录恒为空 —— 对应问题 1。
- unified 桥接路由的流式与非流式都走同一入口 `unifiedStreamSuccess` 并都用 `extractor.Metrics()` 取 token，因此记录侧的流式与非流式**都**为 0 —— 问题 2 的"流式 0"即此。问题 2 中"非流式 token 是对的"指的是**客户端收到的桥接响应体**：实验证明 bridge 在正确 SSE 下对 OpenAI Chat / OpenAI Responses / Anthropic 三种源格式的流式与非流式输出 usage 均正确，无需改动 bridge。

### 根因 B：SSE 事件分隔符不识别 CRLF（实测线上请求才暴露）

事后用线上真实请求 `d8u8avgs9a2fohr5kevg`（从 postgres + S3 取出上游原生响应字节）验证，发现即便加了根因 A 的修复，仍提取不到任何 token / TTFT / 模型。原因：Google 的 Gemini 接口用 **CRLF**（`\r\n\r\n`）做 SSE 事件分隔，而 `processSSEBuffer` 只扫描 `"\n\n"`。`\r\n\r\n` 的字节序列 `0d 0a 0d 0a` 中不含 `0a 0a`，故**一个事件都切不出来**，整条流堆在解析缓冲里从未被处理 —— TTFT / 模型 / token 全部为空。

- 这是更靠前的一层：CRLF 框架使解析器根本走不到 usage 逻辑，根因 A 的字段处理因此永远不触发。把同一段字节的 `\r\n` 改成 `\n` 后，根因 A 的代码立即提取出 `Input=9 / Output=10 / Model=gemini-2.5-flash-lite`，证明两个根因正交、都必须修。
- 该 bug 不限于 Gemini：任何 CRLF 框架的上游 SSE 都会受影响，只是 Google（Gemini）用 CRLF 才使其暴露。OpenAI / Anthropic 上游用 LF，故既有用例一直正常。

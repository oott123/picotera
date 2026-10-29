# Gemini JSON 数组流式响应的 token 提取

## 原始需求

Gemini 的 `streamGenerateContent` 在**不带 `?alt=sse`** 时，返回的不是 SSE，而是一个完整的 JSON 数组（每个元素是一段 `GenerateContentResponse`），形如 `fixtures/d8u8kj0s9a291pp7cakg.json`：

```json
[{
  "candidates": [{"content": {"parts": [{"text": "Hello"}], "role": "model"}, "index": 0}],
  "usageMetadata": {"promptTokenCount": 9, "candidatesTokenCount": 1, "totalTokenCount": 10, ...},
  "modelVersion": "gemini-2.5-flash-lite",
  "responseId": "TIo8aqjCJOKl0-kPiYjbiA8"
}
,
{
  "candidates": [{"content": {"parts": [{"text": " there! How can I help you today? 😊"}], "role": "model"}, "finishReason": "STOP", "index": 0}],
  "usageMetadata": {"promptTokenCount": 9, "candidatesTokenCount": 11, "totalTokenCount": 20, ...},
  "modelVersion": "gemini-2.5-flash-lite",
  "responseId": "TIo8aqjCJOKl0-kPiYjbiA8"
}
]
```

希望我们也能从这种响应中正确提取 `usageMetadata`（token 用量），但**不要把整个响应体一次性读进内存**——响应体可能非常大。最好能在流式过程中边读边提取。

参考 google 官方 SDK `google.golang.org/genai` 的做法。

## 任务

1. 在流式过程中增量提取 Gemini JSON 数组流的 `usageMetadata`（及 TTFT、模型名），不缓存整个响应体。
2. 写测试覆盖。

## 调研结论（补充）

### 官方 SDK 的做法

`google.golang.org/genai`（`googleapis/go-genai`）流式时**无条件**给路径追加 `?alt=sse`（`models.go` 中 `"{model}:streamGenerateContent?alt=sse"` 是硬编码字面量），用 `bufio.Scanner` 按 SSE 事件（空行 `\n\n` / `\r\n\r\n` 分隔）解析 `data:` 行。它**完全不处理**裸 JSON 数组形式——喂它裸数组会落到错误分支。

因此官方 SDK 无法直接借鉴其解析裸数组的逻辑（它根本不解析）。但 Go 解析「来自 `io.Reader` 的 JSON 对象数组而不缓存整体」的标准惯用法是 `json.Decoder` 的 `Token()` / `More()` / `Decode()`：先 `Token()` 吃掉开头的 `[`，循环 `More()`+`Decode(&elem)` 逐个解出元素，最后 `Token()` 吃掉 `]`。`json.Decoder` 同一时刻只缓存一个元素。

### 上游 Content-Type

- `streamGenerateContent` **不带** `alt=sse`：单个（pretty-printed）JSON 数组，`Content-Type: application/json`。
- `streamGenerateContent` **带** `alt=sse`：`Content-Type: text/event-stream`，`data: {...}` 行。

### 与既有工作的关系

- `2026-06-25-gemini-token-extraction` 已为 `ResponseExtractor` 加了 Gemini 的 SSE 与单对象 JSON 提取，并修了 CRLF 框架。本提案补的是它显式排除的「无 `alt=sse` 的 JSON 数组流」。
- `2026-06-25-unified-gemini-alt-sse` 仅在**桥接**（源格式 ≠ 上游 Gemini）时注入 `alt=sse`。**identity 透传**（Gemini 源 → Gemini 上游）时客户端控制 `alt`；若客户端未带 `alt=sse`，上游就返回 JSON 数组，无法强行改写（会破坏客户端期望的响应格式）。所以这条路径必须能直接消费裸数组。

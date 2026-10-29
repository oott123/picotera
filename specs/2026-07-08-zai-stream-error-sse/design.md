# 设计：识别 Z.ai SSE 错误 `finish_reason`

## 背景

`fixtures/zai-stream-error.sse` 和 `fixtures/zai-context-window-error.sse` 都是 OpenAI Chat Completions 风格的 SSE 响应。二者没有 `error.message` 字段，而是在 OpenAI Chat chunk 的 `choices[].finish_reason` 中返回错误原因。

`zai-stream-error.sse`：

```text
data: {"id":"20260708110634e6c57cfb3edd4758","created":1783480011,"model":"glm-5.2","choices":[{"index":0,"finish_reason":"network_error","delta":{"role":"assistant","content":""}}]}

data: [DONE]
```

`zai-context-window-error.sse`：

```text
data: {"id":"2026070419013475159563fd1245d0","created":1783162897,"model":"glm-5v-turbo","choices":[{"index":0,"finish_reason":"model_context_window_exceeded","delta":{"role":"assistant","content":""}}]}

data: [DONE]
```

当前 `ResponseExtractor` 已在 SSE 模式下识别 `error.message` 与 `response.error.message`，并通过既有完成路径把请求记录为 `failed`、`finish_reason = FinishReasonStreamError`。因此需要扩展现有 `detectStreamError`，把这两个严格的 `finish_reason` 错误值也纳入流式错误检测。

## 设计

在 `pkg/server/response_extractor.go` 的 `detectStreamError(payload string)` 中新增 OpenAI Chat chunk 检测逻辑：

- 仅检查 `choices` 数组中的元素。
- 当任一元素的 `finish_reason` 是字符串且严格等于下列值之一时，记录流式错误：
  - `network_error`
  - `model_context_window_exceeded`
- 错误文本使用命中的 `finish_reason` 原始值，用于写入 `request.error_message` 并传入 `afterUpstreamError` hook。
- 继续遵循首个错误优先：如果 `streamError` 已存在，不再覆盖。

这个实现不改变 SSE 原始字节回写，不影响 token、TTFT、模型推断和 artifact 聚合。现有路径网关与统一网关已经从 `extractor.StreamError()` 分支记录失败，因此无需改动完成记录链路、数据库 schema、OpenAPI 或 dashboard。

## 不做的事

- 不把所有非空 `finish_reason` 都视为错误；`stop`、`length`、`tool_calls` 等正常 OpenAI 结束原因不触发流式错误。
- 不做大小写折叠、空白修剪或近似匹配；只接受严格字符串 `network_error` 与 `model_context_window_exceeded`。
- 不新增兼容层或配置开关。
- 不修改 `fixtures/zai-stream-error.sse`。
- 不引入第三方依赖。

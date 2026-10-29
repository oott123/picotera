# 执行计划：识别 Z.ai SSE 错误 `finish_reason`

## 1. 扩展流式错误检测

文件：`pkg/server/response_extractor.go`

- 在 `detectStreamError(payload string)` 中保留现有 `response.error.message` 与 `error.message` 检测顺序。
- 在两种 message 路径之后增加 `choices` 数组扫描。
- 使用 `gjson.Get(payload, "choices")` 读取数组；若不存在或不是数组则直接返回。
- 对每个 choice 严格读取 `finish_reason`：
  - 必须存在；
  - 必须是 JSON string；
  - 值必须严格等于 `network_error` 或 `model_context_window_exceeded`。
- 命中后设置 `e.streamError = <finish_reason 原始值>` 并停止扫描。

## 2. 增加 fixture 回归测试

文件：`pkg/server/response_extractor_test.go`

- 新增测试 `TestResponseExtractor_SSE_StreamError_OpenAIChoiceFinishReasonFixtures`。
- 用表驱动覆盖两个 fixture：
  - `../../fixtures/zai-stream-error.sse`，期望 `StreamError()` 为 `network_error`，期望 `Metrics().InferredModel` 为 `glm-5.2`；
  - `../../fixtures/zai-context-window-error.sse`，期望 `StreamError()` 为 `model_context_window_exceeded`，期望 `Metrics().InferredModel` 为 `glm-5v-turbo`。
- 用 `NewResponseExtractor(strings.NewReader(string(data)), "text/event-stream", time.Now())` 包裹并 `io.ReadAll`。
- 断言：
  - `StreamError()` 返回对应的错误 `finish_reason`；
  - `Metrics().InferredModel` 返回对应模型；
  - 原始响应体透传不变。

## 3. 增加负向测试

文件：`pkg/server/response_extractor_test.go`

- 新增测试覆盖正常 OpenAI Chat SSE `finish_reason`：
  - `stop` 不设置 `StreamError()`；
  - `length` 不设置 `StreamError()`；
  - `tool_calls` 不设置 `StreamError()`。
- 新增测试覆盖非字符串 `finish_reason`：
  - `finish_reason: null` 不设置 `StreamError()`；
  - `finish_reason: 1` 不设置 `StreamError()`。

## 4. 验证既有记录链路无需改动

文件：

- `pkg/server/gateway_flow_success.go`
- `pkg/server/gateway_unified_helpers.go`
- `pkg/db/request_constants.go`
- `dashboard/src/utils/requestLabels.ts`
- `dashboard/src/views/RequestsView.vue`

确认现有行为：

- `extractor.StreamError() != ""` 时，上游行与 meta 行已经记录为失败；
- `finish_reason` 已经使用 `db.FinishReasonStreamError`；
- `afterUpstreamError` hook 已经以 `streamed=true` 执行；
- dashboard 已经显示 `流式错误`，请求列表筛选也包含该原因。

这些文件只需复核，不需要修改。

## 5. 运行验证

- `go test ./pkg/server -run 'ResponseExtractor.*StreamError'`
- `go test ./pkg/server`

无需运行：

- `sqlc generate`：没有 SQL 查询或生成代码变化。
- `mise run openapi` 与 `pnpm --dir dashboard generate-openapi`：没有 API contract 变化。
- dashboard type-check：没有前端代码变化。

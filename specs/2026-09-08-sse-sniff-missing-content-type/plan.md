# 执行计划

## 1. `pkg/server/sse_sniff.go`（新增）

- `sseLinePrefixes = []string{"event:", "data:", "id:", "retry:", ":"}`。
- `sniffSSE(p []byte) (sse, decided bool)`：命中任一前缀 → `(true, true)`；`p` 仍是某个候选的严格前缀 → `(false, false)`；否则 `(false, true)`。
- `bodyIsSSE(body []byte) bool`：对完整正文调用 `sniffSSE`，未判定视为非 SSE。

## 2. `pkg/server/response_extractor.go`

- 把 `mode string` 换成 `extractorMode` 枚举：`modeSniff` / `modeSSE` / `modeJSON`，更新所有 switch。
- `NewResponseExtractor`：`text/event-stream` → `modeSSE`；`contentType == ""` → `modeSniff`；其余 → `modeJSON`。
- 新增字段 `sniffBuf []byte`。
- 抽出 `feed(chunk []byte)`：`modeSniff` 下追加到 `sniffBuf`，`sniffSSE` 未判定则返回；判定后设置模式、把 `sniffBuf` 整体作为 chunk 回放、清空 `sniffBuf`；随后按模式分发到原有的 SSE 行缓冲（含去 `\r`）或 `feedJSON`。
- `Read`：`n > 0` 时调用 `feed(p[:n])`；`err == io.EOF` 时若仍是 `modeSniff`，强制判定（`decided` 视为 true）并回放剩余 `sniffBuf`，再执行原有的 `mode == modeJSON && jsonShape != '[' && len(jsonBuf) > 0` 收尾。

## 3. `pkg/server/response_aggregation.go`

- `buildAggregatedArtifact` 开头：`if contentType == "" && bodyIsSSE(body) { contentType = "text/event-stream" }`，其后逻辑不变（同一个值同时用于 `StreamAggregationKind` 和 `bridge.AggregateStream`）。

## 4. 测试

`pkg/server/sse_sniff_test.go`（新增）：表驱动覆盖 `event:` / `data:` / `id:` / `retry:` / `:` 命中，`{` / `[` / `"data"` / 前导空格 / 空正文放弃，以及 `"da"`、`"retr"` 等未判定分支。

`pkg/server/response_extractor_test.go`（扩充，复用现有 `chunkReader`）：

- contentType `""` + OpenAI Responses SSE 片段（`response.created` → `response.output_text.delta` → 带 usage 的 `response.completed`）：断言 input/output tokens、TTFT、`StreamCompleted`，以及转发字节与输入完全一致。
- 同上但首块切在 `"ev"` / `"ent: ..."`：验证跨 Read 判定与回放。
- contentType `""` + 单个 JSON 对象正文：断言仍走 JSON 路径解析出 usage。
- contentType `""` + Gemini JSON 数组正文：断言数组流路径在回放后仍工作。
- contentType `"application/json"` + SSE 正文：断言维持 JSON 模式、不做探测。

`pkg/server/response_aggregation_test.go`（新增）：用 `handle_unified_gateway_test.go` 里的 `fakeLLMBridge`，对 `FormatOpenAIResponses` + contentType `""` + SSE 正文断言生成了非空 `aggregated.Body` 且无 error；对同格式的普通 JSON 正文断言返回 `nil`。

## 5. 前端

`dashboard/src/composables/useSSEParser.ts`：删除 `isSSEContentType`，新增 `SSEContentTypeState` 类型与 `sseContentTypeState(headers)`（`content-type` 键缺失或值 join 后为空串 → `absent`；含 `text/event-stream` → `sse`；否则 `other`）。

`dashboard/src/components/ResponseArtifactView.vue`：

- 引入 `const ctState = computed(() => sseContentTypeState(props.payload.headers))`。
- `sseEvents` 改为不依赖 `isSSE`：`isBinary || !props.payload.body || ctState === 'other'` 时返回 `[]`，否则 `parseSSEEventsForDisplay(body, timings)`。
- `isSSE = ctState === 'sse' || (ctState === 'absent' && sseEvents.length > 0)`。
- `subViewOptions` / 模板不变。

## 6. 验证

- `go build ./... && go test ./pkg/server/...`。
- `pnpm --dir dashboard type-check`、`pnpm --dir dashboard lint`。
- 起 `mise run server` + `mise run web`，打开请求 `dafsmhos9a202ci1jl60`：响应区出现 “Events” tab 并列出 15 个事件，Raw / 渲染 tab 行为不变。
- 通过 codex provider 重跑一次真实请求，确认新行的 input/output tokens 与 TTFT 落库。
- 无 sqlc / openapi / 迁移改动。

# Design

在 `ResponseExtractor` 上增加一个「上游流已到达终止事件」的粘性标志，并把它作为 `classifyStreamFinishReason` 的第三个输入：当标志为真时，客户端 context 取消不再判为 `FinishReasonCancelled`，而是落到 `FinishReasonEOF`。

不新增 DB 列、不新增 finish reason 枚举、不改 API contract、不改 dashboard、不引入第三方库。

## 为什么放在 ResponseExtractor

`ResponseExtractor`（`pkg/server/response_extractor.go`）已经是唯一逐字节扫描上游响应的组件：它按 SSE 事件边界（`\n\n`）切分 payload、按 Gemini JSON array 元素边界切分元素，并已在这两处做了 `detectStreamError` 等语义判定。终止事件的识别与 `StreamError()` 完全同构——同一个扫描循环、同一个「首次命中即锁定」语义、同一个「read 循环结束后读取结果」的生命周期。

两条流式链路都已经持有 extractor 且都包裹**上游原生格式**的字节：

- 路径网关：`pipePathResponse`（`gateway_flow_success.go:197`）— extractor 直接包 `internalBody`。
- 统一路由：`handleUnifiedSuccess`（`gateway_unified_helpers.go:446`）— extractor 包 `internalBody`，再经 tee 喂给 bridge。

因此终止判定始终基于上游格式，与是否跨格式桥接无关；这与 `StreamError()` 的既有语义一致（「错误来自上游，就按上游格式检测」）。

## 终止事件的判定规则

新增字段 `streamCompleted bool` + 只读方法 `StreamCompleted() bool`。一旦置真不再回退（粘性）。

### SSE 模式（`mode == "sse"`，在 `processSSEEvent` 中）

按上游格式各自的终止事件判定，命中任一即置真：

| 上游格式 | 判定 |
| --- | --- |
| OpenAI Chat Completions 及兼容实现 | data payload 恰为 `[DONE]` |
| Anthropic Messages | `type == "message_stop"` |
| OpenAI Responses | `type == "response.completed"` 或 `type == "response.incomplete"` |
| Gemini（`alt=sse`） | `candidates.0.finishReason` 存在且为非空字符串 |

`[DONE]` 的判定必须写在现有 `if payload == "[DONE]" { return }` 提前返回**之前**，否则该分支会跳过标记。其余三项放在既有的各 `extract*SSE` 之后、与 `detectStreamError` 同一层，读取 `gjson.Parse(payload)` 的结果。

`response.failed` 不在表内：它必然携带 `response.error.message`，会被 `detectStreamError` 捕获，最终 finish reason 被上层覆盖成 `FinishReasonStreamError`，加不加都不影响结果。

### Gemini JSON array 流（`mode == "json"` 且 `jsonShape == '['`）

这是 Gemini `streamGenerateContent` 不带 `alt=sse` 的形态，同样是流。两处置真：

- `processGeminiArrayElement` 中元素带非空 `candidates.0.finishReason`；
- `feedJSONArray` 消费到顶层收尾的 `]`（数组正常闭合本身就是流结束）。

### 非流式 JSON（`jsonShape == '{'`）

不置真。单体 JSON 响应只有整体读完才算完整，客户端中途断开是真的截断，应当继续记为取消。

## classifyStreamFinishReason 的改动

签名从 `(readErr error, reqCtx context.Context)` 变为 `(readErr error, reqCtx context.Context, streamCompleted bool)`：

```go
func classifyStreamFinishReason(readErr error, reqCtx context.Context, streamCompleted bool) int32 {
	if errors.Is(readErr, io.EOF) {
		return db.FinishReasonEOF
	}
	if errors.Is(readErr, errReadIdleTimeout) {
		return db.FinishReasonReadTimeout
	}
	if reqCtx.Err() != nil && !streamCompleted {
		return db.FinishReasonCancelled
	}
	return db.FinishReasonEOF
}
```

空闲超时的判定保持在取消判定之前，所以「终止事件之后上游不关闭连接导致空闲超时」仍然记为 `FinishReasonReadTimeout`（proposal 已确认）。

两个调用点传入 `extractor.StreamCompleted()`：

- `gateway_flow_success.go:233`（`pipePathResponse` 的返回值）；
- `gateway_unified_helpers.go:571`。

## 与其它 finish reason 语义的关系

- **Dashboard 中断**（`FinishReasonDashboardCancelled`）不受影响：它由 `gatewayFlow.finishReasonFor` 在 `classifyStreamFinishReason` 的结果之上覆盖，优先级更高。从 dashboard 打断一条已发完 `[DONE]` 的流，仍记为 dashboard 中断。
- **In-stream error**（`FinishReasonStreamError`）不受影响：`streamErr != ""` 时上层直接把 fr 覆盖为 `FinishReasonStreamError`。即使 `[DONE]` 在错误事件之后到达，错误判定仍然优先。
- **请求转发阶段的取消**（`classifyForwardError`）不涉及：那条路径在拿到响应头之前，压根没有流。

## 已知行为：web search 循环

`/api/unified` 的 web search emulation（`newWebSearchSSELoopDriver`）会在首个上游响应之后追加更多上游请求，而 extractor 只包裹**首个**上游响应体。因此首轮上游发出终止事件后标志即为真，若客户端在后续轮次中断开，会记为 EOF 而非取消。这与该路径既有的度量语义一致（TTFT / token 也只反映首轮上游），不额外处理。

## 并发

`StreamCompleted()` 与既有 `StreamError()` 的读写时序完全相同：由驱动 extractor 的那一侧写入，由 read 循环结束后的持久化代码读取。不引入新的 goroutine、不新增同步原语。

# Plan

## 1. `pkg/server/response_extractor.go`：新增终止事件标志

1. 在 `ResponseExtractor` struct 中，紧邻 `streamError` 字段后新增：

   ```go
   // streamCompleted records whether the upstream stream reached its
   // terminating event (OpenAI [DONE], Anthropic message_stop, OpenAI
   // Responses response.completed/incomplete, Gemini finishReason). Sticky.
   streamCompleted bool
   ```

2. 新增只读方法，紧邻 `StreamError()`：

   ```go
   // StreamCompleted reports whether the upstream stream reached its terminating
   // event. Call after the Read loop. Always false for non-stream JSON bodies.
   func (e *ResponseExtractor) StreamCompleted() bool { return e.streamCompleted }
   ```

3. 新增判定 helper：

   ```go
   // detectStreamCompletion marks the stream complete when an SSE data payload
   // carries the terminating event of any supported upstream format.
   func (e *ResponseExtractor) detectStreamCompletion(payload string) {
       if e.streamCompleted {
           return
       }
       result := gjson.Parse(payload)
       switch result.Get("type").String() {
       case "message_stop", "response.completed", "response.incomplete":
           e.streamCompleted = true
           return
       }
       if v := result.Get("candidates.0.finishReason"); v.Exists() && v.Type == gjson.String && v.String() != "" {
           e.streamCompleted = true
       }
   }
   ```

4. 在 `processSSEEvent` 中，把 `[DONE]` 提前返回改成先置标志：

   ```go
   // [DONE] sentinel: terminating event for OpenAI Chat Completions and
   // compatible providers. Mark completion before skipping the payload.
   if payload == "[DONE]" {
       e.streamCompleted = true
       return
   }
   ```

5. 在 `processSSEEvent` 中，紧随 `e.detectStreamError(payload)` 之后调用 `e.detectStreamCompletion(payload)`。

6. 在 `processGeminiArrayElement` 中，紧随 `e.detectStreamError(payload)` 之后调用 `e.detectStreamCompletion(payload)`（复用同一 helper：Gemini array 元素与 SSE payload 是同一 JSON 形状）。

7. 在 `feedJSONArray` 的数组闭合分支置标志：

   ```go
   if e.jaBuf[cur] == ']' {
       // Array closed; the stream ended normally. Ignore any trailing bytes.
       e.streamCompleted = true
       e.jaBuf = nil
       return
   }
   ```

## 2. `pkg/server/gateway_flow_success.go`：分类函数与路径网关调用点

1. `classifyStreamFinishReason` 增加第三参数 `streamCompleted bool`，取消分支改为 `if reqCtx.Err() != nil && !streamCompleted`（见 design.md 的完整实现）。补充函数注释说明：上游已发出终止事件后客户端断开算正常 EOF；空闲超时仍优先。
2. `pipePathResponse` 的返回语句改为
   `return extractor, progress, classifyStreamFinishReason(finalReadErr, input.Flow.ctxs.Request, extractor.StreamCompleted())`。

## 3. `pkg/server/gateway_unified_helpers.go`：统一路由调用点

第 571 行改为
`finishReason := classifyStreamFinishReason(finalReadErr, r.Context(), extractor.StreamCompleted())`。

## 4. 测试

### `pkg/server/response_extractor_test.go`（新增）

- `TestStreamCompletedOpenAIDone`：SSE 内容 `data: {...}\n\ndata: [DONE]\n\n`，读完后 `StreamCompleted() == true`。
- `TestStreamCompletedAnthropicMessageStop`：`event: message_stop\ndata: {"type":"message_stop"}\n\n` → true。
- `TestStreamCompletedOpenAIResponses`：`data: {"type":"response.completed",...}` → true；另加 `response.incomplete` 子用例 → true。
- `TestStreamCompletedGeminiSSE`：首帧只有 `candidates.0.content.parts` → false；末帧带 `"finishReason":"STOP"` → true。
- `TestStreamCompletedGeminiJSONArray`：JSON array 流分块喂入，末元素带 `finishReason` → true；另加「只喂到 `]` 但元素无 finishReason」子用例 → true。
- `TestStreamNotCompletedTruncatedSSE`：SSE 缺终止事件（只有若干 delta）→ false。
- `TestStreamNotCompletedNonStreamJSON`：`application/json` 单体响应体读完 → false。

### `pkg/server/gateway_flow_test.go`（新增）

`TestClassifyStreamFinishReason` 表驱动，覆盖：

| readErr | reqCtx | streamCompleted | 期望 |
| --- | --- | --- | --- |
| `io.EOF` | 正常 | false | `FinishReasonEOF` |
| `io.EOF` | 已取消 | false | `FinishReasonEOF` |
| `errReadIdleTimeout` 包装 | 正常 | false | `FinishReasonReadTimeout` |
| `errReadIdleTimeout` 包装 | 已取消 | true | `FinishReasonReadTimeout` |
| `context.Canceled` | 已取消 | false | `FinishReasonCancelled` |
| `context.Canceled` | 已取消 | true | `FinishReasonEOF` |
| 其它 error | 正常 | false | `FinishReasonEOF` |

## 5. 验证

```bash
go build ./... && go test ./pkg/server/
```

不需要 `sqlc generate`、`mise run openapi`、dashboard 类型再生成——本次不触碰 DB 查询、contract 与前端。

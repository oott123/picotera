# 执行计划：Gemini JSON 数组流式响应的 token 提取

仅涉及 `pkg/server/response_extractor.go` 及其测试。不改 bridge、不改 axonhub、无 API/DB/前端变更。

## 步骤 1：写测试（红）

在 `pkg/server/response_extractor_test.go` 末尾新增用例，沿用既有 `strings.NewReader` + `NewResponseExtractor(inner, "application/json", start)` + `io.ReadAll` 写法：

1. `TestResponseExtractor_JSONArray_Gemini_Usage`
   - 输入：`fixtures/d8u8kj0s9a291pp7cakg.json` 的内容（两元素数组，两元素都带 `usageMetadata`：元素 1 `promptTokenCount:9,candidatesTokenCount:1`；元素 2 `promptTokenCount:9,candidatesTokenCount:11` + `finishReason:"STOP"`），Content-Type `application/json`。
   - 断言：`*InputTokens==9`、`*OutputTokens==11`（末值生效）、`TTFTMs!=nil`、`InferredModel=="gemini-2.5-flash-lite"`、`InferredModelSource==db.InferredModelSourceResponse`。
2. `TestResponseExtractor_JSONArray_Gemini_BytesForwardedUnchanged`
   - 断言 `io.ReadAll(extractor)` 得到的字节与输入 byte-for-byte 相等（透传不变）。
3. `TestResponseExtractor_JSONArray_Gemini_AcrossReadCalls`
   - 用逐字节 / 小块切分的 reader（参照既有 `TestResponseExtractor_SSE_EventsAcrossReadCalls` 的分块手法）喂入，断言跨 `Read` 边界仍提取出正确 token（验证状态机持久性）。
4. `TestResponseExtractor_JSONArray_Gemini_StringBraces`
   - 元素文本含 `{`、`}`、`,`、`]` 等字符（放进 `parts[].text`），断言不被结构状态机误判、token 仍正确。
5. `TestResponseExtractor_JSON_Gemini_SingleObjectStillWorks`（回归）
   - 顶层单对象 `{…usageMetadata…}`，断言走既有 `extractJSONMetrics` 仍正确（确认 `jsonShape=='{'` 分支无回归）。

运行 `go test ./pkg/server/ -run 'TestResponseExtractor_JSONArray' -v`，确认数组用例失败（当前对数组取不到 `usageMetadata`）。

## 步骤 2：形态探测 + feedJSON

在 `ResponseExtractor` 加字段：`jsonShape byte`、`jaBuf []byte`。

`Read` 的 `case "json"` 改为 `e.feedJSON(chunk)`。新增 `feedJSON`：

- `jsonShape == 0`：扫首个非空白字节；`[` → `jsonShape='['` 且 `feedJSONArray(chunk[i+1:])`；其它 → `jsonShape='{'` 且 `jsonBuf = append(jsonBuf, chunk...)`；全空白 → `jsonBuf = append(jsonBuf, chunk...)` 后返回。
- `jsonShape == '['` → `feedJSONArray(chunk)`。
- `jsonShape == '{'` → `jsonBuf = append(jsonBuf, chunk...)`。

`Read` 的 EOF 条件改为 `if err == io.EOF && e.mode == "json" && e.jsonShape != '[' && len(e.jsonBuf) > 0 { e.extractJSONMetrics() }`。

## 步骤 3：增量数组扫描器 feedJSONArray

新增 `func (e *ResponseExtractor) feedJSONArray(data []byte)`，用 `jsontext`（`import "github.com/go-json-experiment/json/jsontext"`、`"bytes"`）按 design.md 算法：

1. `e.jaBuf = append(e.jaBuf, data...)`；`cur := 0`。
2. 循环：
   - 跳过 `e.jaBuf[cur:]` 前导空白（` \t\n\r`）；若随后是 `,` 则跳过它再跳过空白。
   - `cur >= len(e.jaBuf)` → break。
   - `e.jaBuf[cur] == ']'` → 数组结束，`e.jaBuf = nil`、return。
   - `dec := jsontext.NewDecoder(bytes.NewReader(e.jaBuf[cur:]))`；`val, err := dec.ReadValue()`：
     - `err == nil` → `e.processGeminiArrayElement(val)`；`cur += int(dec.InputOffset())`；continue。
     - `err == io.ErrUnexpectedEOF || err == io.EOF` → break（等更多字节）。
     - 其它 → break（停止扫描该流）。
3. `e.jaBuf = append(e.jaBuf[:0], e.jaBuf[cur:]...)`（保留未消费尾部）。

`jsontext.Value` 即 `[]byte`，直接传给 `processGeminiArrayElement`。

## 步骤 4：元素提取 processGeminiArrayElement

新增 `func (e *ResponseExtractor) processGeminiArrayElement(elem []byte)`：

- `result := gjson.ParseBytes(elem)`；`payload := string(elem)`（既有 `infer*`/`detectStreamError` 收 `string`）。
- TTFT：`if !e.ttftRecorded && result.Get("candidates.0.content.parts").Exists()` → 记录。
- `e.setGeminiUsage(result.Get("usageMetadata"))`（既有函数）。
- `e.inferModelField(payload)`、`e.detectStreamError(payload)`、`e.inferProvider(payload)`。

## 步骤 5：验证

- `go test ./pkg/server/ -run TestResponseExtractor -v`：步骤 1 新增用例全绿，既有 SSE/JSON（OpenAI/Anthropic/Gemini 单对象）用例全绿（无回归）。
- `go build ./...`。
- `go test ./pkg/server/ ./pkg/llmbridgeimpl/`。

## 不做的事

- 不改 `pkg/llmbridge*` 与 `third_party/axonhub`。
- 不强制注入 `alt=sse`（identity 透传不能改写客户端期望的响应格式）。
- 不引入兼容层 / 宽松归一化（遵循 `CLAUDE.md`）。

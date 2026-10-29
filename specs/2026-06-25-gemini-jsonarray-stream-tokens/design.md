# 设计：Gemini JSON 数组流式响应的 token 提取

## 现状与问题

`pkg/server/response_extractor.go` 的 `ResponseExtractor` 是一个**透传 `io.Reader`**：`Read` 把上游字节原样转发给客户端，同时旁路喂入解析缓冲。它按 Content-Type 选 `mode`：

- `text/event-stream` → `mode = "sse"`：`Read` 增量喂 `lineBuf`，`processSSEBuffer` 按 `\n\n` 切事件、逐事件提取、处理完即丢弃。**内存有界**。
- 其它 → `mode = "json"`：`Read` 把**整个 body** 累积进 `jsonBuf`，EOF 时 `extractJSONMetrics` 一次性 `gjson.ParseBytes` 解析。

Gemini 无 `alt=sse` 的流式响应是 `Content-Type: application/json` 的 JSON 数组 `[{…},{…}]`，因此落入 `"json"` 分支，触发两个问题：

1. **内存**：整个数组缓存进 `jsonBuf`，响应体大时占用大。
2. **正确性**：`extractJSONMetrics` 对累积体做 `gjson.ParseBytes(jsonBuf).Get("usageMetadata")`——顶层是**数组**，该路径取不到任何元素的 `usageMetadata`，所以即便缓存了也提取不到 token。

非流式单对象（`generateContent`，顶层 `{…}`）不受影响，仍走既有 `extractJSONMetrics`。

## 方案

仅改 `pkg/server/response_extractor.go` 及其测试。不改 bridge、不改 axonhub、无 API/DB/前端变更。

核心：在 `"json"` mode 下，按**首个非空白字节**区分两种形态——`[` 是 JSON 数组流（增量处理），`{` 是单对象（既有缓存处理）。

### 形态探测

新增字段 `jsonShape byte`（0 = 未定、`'['` = 数组流、`'{'` = 单对象）。`Read` 的 `"json"` 分支改为调用 `feedJSON(chunk)`：

- `jsonShape == 0`：扫描 `chunk` 找首个非空白字节（`' ' \t \r \n` 之外）。
  - 是 `[`：`jsonShape = '['`，把该 `[` 之后的字节交给数组扫描器 `feedJSONArray`。
  - 是其它：`jsonShape = '{'`，把整个 `chunk` 累积进 `jsonBuf`（既有路径）。
  - 整块全空白：累积进 `jsonBuf`（无害），留待下一块定型。
- `jsonShape == '['`：`feedJSONArray(chunk)`。
- `jsonShape == '{'`：累积进 `jsonBuf`。

顶层 `[` 唯一地表示数组流（非流式 `generateContent` 永不返回数组），所以按字节探测足够，且与具体 provider 无关。

EOF 触发 `extractJSONMetrics` 的条件加上 `jsonShape != '['`——数组流已在流式中处理完，不再做整体解析。

### 增量数组扫描器（`feedJSONArray`）

镜像 `processSSEBuffer` 的「累积→定界→处理→丢弃」模式：把上游字节累积进 `jaBuf`，每碰到一个**完整的顶层元素**就提取并把它从 `jaBuf` 前部切除。同一时刻只保留 `jaBuf` 中尚未构成完整元素的尾部，内存上界 = 单个最大元素（远小于整个数组）。

**元素定界用 `github.com/go-json-experiment/json/jsontext`（已是依赖，`pkg/jsonast` 在用），不手写括号/字符串/转义状态机。** `jsontext.Decoder.ReadValue()` 返回下一个完整 JSON 值的原始字节（`jsontext.Value` 即 `[]byte`），缓冲被截断时返回 `io.ErrUnexpectedEOF`（见库 `decode.go` 文档），正好用来判断「元素还没收完，等更多字节」。值边界探测（含字符串里的 `{}`/`,`、转义、嵌套）全部交给库，杜绝手写状态机的出错面。

不能让 `Decoder` 直接拉取 `inner`：`inner` 的字节同时要原样转发给客户端，`Decoder` 拉取会与透传争抢，且把推模型转成拉模型需要额外 `io.Pipe`+goroutine。因此每轮在**已累积的缓冲尾部**上开一个短生命周期 `Decoder` 解出一个元素，靠 `InputOffset()` 推进游标——同步、无并发、无第二个 reader。

状态字段（持久跨 `Read`）：仅 `jaBuf []byte`（尚未消费的字节）。`feedJSON` 探测到 `[` 时把其**之后**的字节交给 `feedJSONArray`，故 `jaBuf` 从不含开头的 `[`，起点即第一个元素前的空白/`{`。

`feedJSONArray(chunk)` 算法：`jaBuf = append(jaBuf, chunk...)`，然后维护游标 `cur` 循环：

1. 跳过 `jaBuf[cur:]` 的前导空白（` \t\n\r`）；若随后是单个 `,` 再跳过它及其后空白。（顶层元素之间只可能是空白与一个分隔逗号，不涉及字符串，安全。）
2. `cur` 到末尾 → 收尾退出（等更多字节）。
3. `jaBuf[cur]` 是 `]` → 数组结束，忽略其后字节，退出。
4. `dec := jsontext.NewDecoder(bytes.NewReader(jaBuf[cur:]))`；`val, err := dec.ReadValue()`：
   - `err == nil` → `processGeminiArrayElement(val)`；`cur += int(dec.InputOffset())`；继续循环。
   - `err == io.ErrUnexpectedEOF`（值被截断）或 `io.EOF`（无更多） → 退出，等更多字节。
   - 其它（语法错误） → 停止扫描该流（实践中不会发生在合法上游上）。

循环结束 `jaBuf = jaBuf[cur:]`（数组已结束则清空）。流中途截断时尾部那个未闭合元素不被发射，无副作用。字段提取仍走 `gjson`（与本文件其余部分一致）：`jsontext` 只负责切出元素原始字节，`gjson.ParseBytes(val)` 取字段。

### 元素提取（`processGeminiArrayElement`）

对每个完整元素，复用既有逻辑：

- TTFT：未记录且 `candidates.0.content.parts` 存在时记录 `time.Since(startTime)`（与 `extractGeminiSSE` 一致）。
- Usage：`setGeminiUsage(gjson.Get(payload, "usageMetadata"))`——既有函数，只对存在的字段赋值，**末值生效**（数组里靠后的元素覆盖靠前的）。
- 模型：`inferModelField(payload)`（已含 `modelVersion`）。
- 错误：`detectStreamError(payload)`、`inferProvider(payload)`（与 SSE 路径对齐）。

token 语义沿用既有 Gemini 约定：`InputTokens = promptTokenCount - cachedContentTokenCount`、`OutputTokens = candidatesTokenCount + thoughtsTokenCount`、`CacheReadTokens = cachedContentTokenCount`（>0 时）。

### 字节透传不变

`Read` 仍 `return n, err` 原始 `p`；数组扫描只读旁路。客户端收到的字节 byte-for-byte 不变（与 SSE 剥 CR 一样，扫描器不回写 `p`）。

## 实现选型

- **用 `jsontext`、不手写状态机**：定界 JSON 值要正确处理字符串里的 `{}`/`,`、`\"` 转义、嵌套——手写易错。`jsontext.Decoder.ReadValue` 是经过测试的实现，且已是仓库依赖（`pkg/jsonast`），优先复用。
- **不用标准库 `encoding/json.Decoder`**：仓库统一用 `go-json-experiment`，且 `jsontext` 的 `ReadValue` + `io.ErrUnexpectedEOF`（截断信号）比标准库 `Token/More/Decode` 更直接地支持「碰到不完整就停下等更多」。
- **不让 Decoder 拉 `inner`**：无论哪个库，让 Decoder 直接读 `inner` 都会与「同一份字节透传给客户端」冲突，需 `io.Pipe`+goroutine 把推模型转拉模型，徒增并发与生命周期复杂度。每元素在缓冲尾部开短生命周期 Decoder 是同步、无并发的最简解。
- **官方 `genai` SDK 无可直接借鉴**：它流式强制 `alt=sse`、用 `bufio.Scanner` 按 SSE 解析，根本不消费裸数组。

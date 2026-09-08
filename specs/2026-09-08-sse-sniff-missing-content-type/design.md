# 设计：缺失 Content-Type 时的 SSE 探测

## 现状

ChatGPT Codex 上游（`/backend-api/codex/responses`）会返回 HTTP 200 + 完整 SSE 正文，但响应头里完全没有 `Content-Type`。本地样本 `dafsmhos9a202ci1jl60`（上游行 `dafsmhos9a202ci1jl50`）的 artifact 证实：headers 里无 `Content-Type`，body 以 `event: response.created\n` 开头，末尾 `response.completed` 事件带 `usage.input_tokens` / `output_tokens`，共 15 个事件。

三处因此走错分支：

| 位置 | 现在的行为 |
| --- | --- |
| `pkg/server/response_extractor.go` `NewResponseExtractor` | `contentType` 不含 `text/event-stream` 即进 `json` 模式，整段 SSE 被当成一个 JSON 体，usage / TTFT / `streamCompleted` 全部落空 |
| `pkg/server/response_aggregation.go` `buildAggregatedArtifact` | `StreamAggregationKind(format, "")` 对 anthropic/openai 返回 `None`（不生成聚合），对 gemini 返回 `Unsupported`（生成带 error 的聚合） |
| `dashboard/src/composables/useSSEParser.ts` `isSSEContentType` | 返回 false，`ResponseArtifactView` 不出 “Events” tab |

## 探测规则

统一一条规则，三处共用：**仅当 `Content-Type` 完全缺失（无该 header，或其值为空串）时才探测**；header 存在但不是 `text/event-stream` 就照其字面处理，不做二次猜测。

Go 侧探测（新文件 `pkg/server/sse_sniff.go`）判断正文起始字节是否为 SSE 字段行或注释行：前缀属于 `event:` / `data:` / `id:` / `retry:` / `:` 即判为 SSE，否则放弃、按 JSON 处理。不容忍前导空白与 BOM。最长候选是 `retry:`，所以 6 个字节足以判定：

```go
// sniffSSE reports whether p starts an SSE stream. decided is false while p is
// still a strict prefix of a candidate field name and more bytes may arrive.
func sniffSSE(p []byte) (sse, decided bool)

// bodyIsSSE probes a complete body.
func bodyIsSSE(body []byte) bool
```

前端因为拿到的是完整 body，直接用现成的 `parseSSEEvents` 做真实解析：解析出 ≥1 个事件才算 SSE。

## 后端：流式 extractor

`ResponseExtractor` 增加第三种模式 `sniff`（`Content-Type` 缺失时的初始模式）。`Read` 里新增一层 `feed`：

- `sniff` 模式下把到达的字节追加进 `sniffBuf`，判定不出就先不喂给任何解析路径；
- 判定完成后把整个 `sniffBuf` 一次性回放给选中模式的 `processSSEBuffer` / `feedJSON`，两条路径都不会丢字节；
- `io.EOF` 时若仍未判定（正文短于 6 字节），强制判定并回放，再走原有的 `extractJSONMetrics` 收尾。

转发给客户端的字节不受影响：`Read` 仍原样返回本次读到的 `p[:n]`，被缓冲的只有解析副本，且最多滞留 6 字节。

`mode` 由字符串改为 `extractorMode` 枚举（`modeSniff` / `modeSSE` / `modeJSON`）。

## 后端：聚合 artifact

`buildAggregatedArtifact` 在计算 `StreamAggregationKind` 之前替换有效 content type：`contentType == "" && bodyIsSSE(body)` 时改用 `text/event-stream`，同一个值继续传给 `bridge.AggregateStream`，宿主与插件对格式的判断保持一致。`pkg/llmbridge` 与 `pkg/llmbridgeimpl` 不改。

这还不够：路径网关的 `aggregatePathResponse` 先要拿到一个格式才会调 `buildAggregatedArtifact`，而 `responseAggregationFormat` 对 `EndpointType_Codex` 返回 `false`，探测被短路在前一步。codex 是前缀端点，子路径开放，格式只能由子路径决定，所以 `responseAggregationFormat` 增加 suffix 参数，codex + `/responses` → `FormatOpenAIResponses`，其余返回 false —— 与 `codexUnifiedRoute` 的判断逐字对齐（`/responses/compact` 在 unified 侧也是 passthrough，同样不聚合）。suffix 由 `RecordedEndpointPath` 去掉 `Endpoint.Path` 得到，普通端点为空串、行为不变。

unified 侧无需改动：`/api/unified/codex/responses` 的 `upFormat` 本来就被解析成 `FormatOpenAIResponses`，缺 Content-Type 的探测直接生效。

## 前端

`useSSEParser.ts` 用三态判定替换 `isSSEContentType`：

```ts
export type SSEContentTypeState = 'sse' | 'other' | 'absent'
export function sseContentTypeState(headers: Record<string, string[]> | undefined): SSEContentTypeState
```

`ResponseArtifactView.vue` 反转两个 computed 的依赖方向，消除环：`sseEvents` 只在状态不是 `other` 且正文非二进制时解析；`isSSE = state === 'sse' || (state === 'absent' && sseEvents.length > 0)`。`absent` + JSON 正文的额外开销是一次 `split(/\n\n+/)`，正文本就在内存里。

“聚合” tab 的出现条件 `isSSE || payload.aggregated` 不变：codex 端点从不生成后端聚合，探测成功后该 tab 显示“无后端聚合结果”，与今天带 `Content-Type` 的 codex SSE 响应表现一致。

## 不在范围内

- `gateway_unified_helpers.go` 的 `streamMode`：它决定 llmbridge 走流式还是非流式转换，且必须在读到上游第一个字节之前连同客户端响应头一起提交，探测无法参与。
- `markSSENoBuffering`：同上，响应头提交早于正文。
- 历史数据不回填，后端改动只影响新请求。
- 正文为 JSON 数组 / JSONL 而 `Content-Type` 缺失的情况不做探测。

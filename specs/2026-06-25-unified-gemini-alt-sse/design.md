# 设计

## 注入点

注入逻辑放在 `pkg/server/gateway_flow_attempts.go` 的 `bridgeUnifiedRequest`。理由:

1. 该函数对 identity 场景(`input.Sidecar.UpstreamFormat == f.config.SourceFormat`)提前 `return`,天然把范围限定在 bridge 转换场景,满足"仅 bridge"决策。
2. 它在 `buildRewrittenUpstreamRequest` 中由 `PrepareAttempt`(即 `prepareUnifiedAttempt`)调用,执行时机早于 `f.session.RunRewriteRequest`,满足"钩子前注入、可被钩子覆盖"决策。
3. 此时 `req.URL` 已由 `buildUpstreamRequest` 完成路径变量替换与 `mergeClientQuery`,在其上改写查询参数是安全且最终的上游 URL 形态。

## 实现

在 `bridgeUnifiedRequest` 完成 body 桥接后,判断目标上游格式:当 `input.Sidecar.UpstreamFormat == llmbridge.FormatGeminiStreamGenerateContent` 时,对 `req.URL` 设置 `alt=sse`(覆盖式 `q.Set`),写回 `req.URL.RawQuery`。

```go
// 桥接到 Gemini streamGenerateContent 时,客户端(非 Gemini 源)不会携带
// alt=sse,Gemini 默认返回 JSON 数组流而非 SSE。强制写入 alt=sse,使上游返回
// text/event-stream,供 BridgeStream 正确解析。在 rewriteRequest 钩子前注入,
// 脚本可改写或删除。
if input.Sidecar.UpstreamFormat == llmbridge.FormatGeminiStreamGenerateContent {
    q := req.URL.Query()
    q.Set("alt", "sse")
    req.URL.RawQuery = q.Encode()
}
```

注入采用覆盖式 `q.Set`:若上游配置 URL 或 `mergeClientQuery` 已带其他 `alt` 值,统一以 `sse` 为准——这是桥接路径正确解析的硬性前提。

## 不在范围内

- identity(gemini-stream → gemini-stream)字节级透传:不改动,客户端自带 `alt` 经 `mergeClientQuery` 透传。
- path-based(非 unified)网关:客户端直连 Gemini stream 时自行携带 `alt=sse`,无需改动。
- 无新增第三方库、无数据库 / 契约 / OpenAPI 变更,故无 `api.md`。

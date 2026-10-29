# Unified 桥接转发到 Gemini streamGenerateContent 自动加 alt=sse

## 原始需求

在 unified 改写转发到 gemini stream content 的场景,应该自动加上 `alt=sse` 才能正确解析响应。

## 背景与问题

unified 路由把客户端请求按上游 endpoint 的格式做跨格式桥接(`pkg/llmbridge`)后转发。当源格式为 Anthropic / OpenAI,而被选中的上游 endpoint 是 Gemini `streamGenerateContent` 时,客户端请求中不会携带 Gemini 特有的 `alt=sse` 查询参数。

Gemini `streamGenerateContent` 在缺少 `alt=sse` 时返回的是 JSON 数组形式的分块流,而非 SSE(`text/event-stream`)。unified 的流式成功路径依据上游 `Content-Type` 判定 `streamMode`,并以此调用 `BridgeStream` 按 SSE 解析上游响应。缺少 `alt=sse` 会导致上游返回非 SSE 响应,桥接解析失败 / 响应无法正确转换回源格式。

## 决策

- **适用范围:仅 bridge 转换场景**。即源格式 != 上游格式,且上游格式为 Gemini `streamGenerateContent` 时,自动在上游请求 URL 注入 `alt=sse`。identity(gemini-stream → gemini-stream 1:1 透传)保持字节级透传,不改动客户端自带的 `alt` 参数。
- **钩子前注入**。在构建上游请求阶段(`rewriteRequest` JS 钩子运行之前)注入 `alt=sse`,使脚本可见并可改写 / 删除该参数。

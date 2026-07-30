# Proposal

移除 js hook 里对 ctx.metaRequest.annotations 的设置功能。新增 picotera.request.setAnnotation(requestId, key, value) 的 API；新增对 provider 和 apiKey 的 setAnnotation api。如果 value 为 null 或 undefined，则自动删除 annotation；否则如果 value 不为字符串则报错。增加 picotera.provider.get(providerId) 功能，获取 provider 信息。增加 picotera.apiKey.get(apiKeyId) 功能。最后增加一个 requestFinished 的 hook 点，metaRequest 更新完成原因之后调用。

## 澄清（与用户确认的结论）

- `picotera.provider.get` 的参数是 **providerId**（原文 requestId 为笔误）。
- `picotera.provider.get` / `picotera.apiKey.get` 返回**已有的 JS 边界 Summary 形状**（`jsx.ProviderSummary` / `jsx.ApiKeySummary`），不返回含 credentials / key 的 contract View——凭据不过 JS 边界的既有安全决策保持不变。
- `requestFinished` hook 的输入来自**内存中的完整终态值**（不回读 DB）：把 meta 行的各终态更新点在内存里累积成完整快照（statusCode、finishReason、errorMessage、timeSpentMs、ttftMs、各 token 数、cost、providerId、model、upstreamModel），在 meta 行完成原因更新之后、session 关闭之前调用 hook。
- `ctx.metaRequest` 和 `ctx.upstreamRequest` **只保留标识字段 `{ id, spanId, parentSpanId, traceId }`**：meta 与 upstream 两侧的 annotations 写入代理（Proxy 及其宿主函数、累积器、落库逻辑）全部移除；脚本统一改用 `picotera.request.setAnnotation(requestId, key, value)` 按 id 写注解。`parentSpanId` / `traceId` 不存在时为 `null`。

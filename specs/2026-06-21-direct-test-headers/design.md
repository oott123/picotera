# 设计：短路测试自定义请求头 + Anthropic 版本头

在已有「网关测试自定义 Headers」（spec `2026-06-21-test-headers-and-otr`）的基础上扩展，使自定义请求头同时作用于短路测试（direct），并把 `anthropic-version` 头纳入前端自动生成逻辑。

涉及三处：`dashboard/src/views/TestView.vue`、`dashboard/src/api/client.ts`、`pkg/server/handle_test_direct.go`。`/api/picotera/test/direct` 是裸 chi 路由而非 Huma op（请求体结构 `testDirectRequest` 手写在 handler 内），因此**无需** OpenAPI / contract 改动。

## 自定义请求头的作用范围

自定义请求头编辑器（`AnnotationsEditor`）对**两种模式**都渲染。OTR 选项仍只在网关模式渲染——短路测试绕过整个网关流水线（无 jsx、无 MPE 解析、无请求/工件记录），数据记录模式对它没有意义。

将原 gateway 专属的 `gatewayHeaders` / `headersManualOverride` / `onHeadersInput` / `rebuildHeaders` 提升为两种模式共用，状态变量更名 `gatewayHeaders` → `customHeaders`。

## 自动生成的请求头（`generatedHeaders`）

从结构化表单派生，按当前规则统一为两种模式生成：

- **Anthropic 版本头**：当 `baseFormat === 'anthropicMessages'` 时生成 `{ 'anthropic-version': '2023-06-01' }`。该规则对网关测试与短路测试都生效——真实 Anthropic 客户端本就携带此头，加上后更贴近真实请求。
- **OTR 头**：仅当 `mode === 'gateway'` 且 `otrOverride` 非空时生成 `{ 'X-PicoTera-OTR': otrOverride }`。

```ts
const generatedHeaders = computed<Record<string, string>>(() => {
  const h: Record<string, string> = {}
  if (baseFormat.value === 'anthropicMessages') h['anthropic-version'] = '2023-06-01'
  if (mode.value === 'gateway' && otrOverride.value) h['X-PicoTera-OTR'] = otrOverride.value
  return h
})
```

自动生成 → 手动覆盖的同步机制（`watch(generatedHeaders, …)` + `onHeadersInput` + `rebuildHeaders`）保持不变；`baseFormat` 或 `mode` 变化时若未手动覆盖则自动重算同步。

## 后端：短路测试转发自定义头

`testDirectRequest` 增加 `Headers map[string]string` 字段。`handleTestDirect` 在构造上游请求时按以下次序写入 header，与网关测试 `postGatewayTest` 的语义对齐：

1. 默认 `Content-Type: application/json`（可被自定义头覆盖）。
2. 写入自定义头（`in.Headers`）。
3. `applyCredentials(...)` **置于最后**，强制写入凭据，自定义头无法破坏鉴权。

**移除** handler 内对 `anthropic-version` 的硬编码注入：该头改由前端 `generatedHeaders` 生成并随自定义头转发，成为单一来源且可被用户覆盖。`/api/picotera/test/direct` 仅由仪表盘调用，前端始终会生成该头，不需要后端再兜底。同时更新「No client headers are copied」的注释。

（`handle_provider_endpoint.go` 中拉取模型流程的 `anthropic-version` 注入与本次无关，保持不动。）

## client.ts

`TestDirectPayload` 增加 `headers?: Record<string, string>`，`postTestDirect` 把它放入序列化的 payload。`postGatewayTest` 不变——自定义头已通过其 `headers` 形参流入 `fetch`。

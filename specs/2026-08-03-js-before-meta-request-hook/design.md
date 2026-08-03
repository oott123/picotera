# 设计：beforeMetaRequest Hook

## 目标

新增第九个脚本 hook `beforeMetaRequest`：在 `sortProviders` 之后、第一次上游尝试之前执行一次。脚本返回 `undefined` 时流程照旧；返回 `ResponseShape` 时，网关直接用该响应答复客户端，不发起任何上游请求。

## 执行位置

`gatewayFlow.run()`（`pkg/server/gateway_flow.go`）中插在 `resolveAndSortCandidates()` 与 `runAttempts()` 之间：

```
resolveAndRewriteModel → resolveAndSortCandidates（含 sortProviders）
  → beforeMetaRequest        ← 新增
  → runAttempts（beforeRequest / rewriteRequest / …）
  → failAllProviders
```

要点：

- `sorted` 为空数组时同样执行 hook；hook 未接管时仍走既有的 `runAttempts` → `failAllProviders`（502 `all providers failed`），行为不变。
- 路径网关与 `/api/unified` 共用 `gatewayFlow.run()`，一处插入即覆盖两条链路。
- 「获取模型列表」链路是独立的管理链路，不执行本 hook。
- hook 执行时 `ctx` 已经带上 `endpoint` / `request`（含 body Proxy）/ `apiKey` / `user` / `routedModel` / `annotations` / `metaRequest` / `stream` / `sourceFormat`；`ctx.provider` / `ctx.providerModel` / `ctx.attempt` / `ctx.upstreamRequest` 仍为初始值（`null`），因为尚未进入尝试循环。
- 按澄清结论，**不**向 `ctx` 写入候选列表或候选数量。

## ResponseShape

waterfall 初值为 `undefined`。任一 tap 返回对象后，后续 tap 拿到的输入即为该对象（waterfall 既有语义），最终值决定是否接管。

```ts
interface ResponseShape {
  statusCode: number                              // 整数，100–599
  headers?: Record<string, string | string[]>     // 可选
  body?: string | object | Array<unknown> | null  // 可选
  tokens?: ResponseTokens                         // 可选，用量记账
}

interface ResponseTokens {
  inputTokens?: number
  outputTokens?: number
  cacheReadTokens?: number
  cacheWriteTokens?: number
  cacheWrite1hTokens?: number
}
```

字段命名沿用现有 hook（`UpstreamErrorView`、`AfterUpstreamErrorDecision`、`RequestFinishedView` 均用 `statusCode`）。

**严格校验（fail fast，在 `sdk.js` 之外的 glue IIFE 内抛错，宿主侧再做一次防御性检查）：**

| 情况 | 处理 |
| --- | --- |
| 返回 `undefined` / `null` | 放行，正常继续流程 |
| 返回非对象（字符串、数字、数组） | 抛错 |
| `statusCode` 非整数或不在 `[100, 599]` | 抛错 |
| `headers` 存在且非普通对象 | 抛错 |
| header 值非 `string` 且非 `string[]` | 抛错 |
| header 名为 `Content-Length` / `Transfer-Encoding`（大小写不敏感） | 抛错（由 Go 层计算） |
| `body` 为 `string` | 原样作为响应体字节 |
| `body` 为对象/数组（含 body Proxy） | `JSON.stringify` 后作为响应体 |
| `body` 缺省 / `null` / `undefined` | 空响应体 |
| `body` 为其它类型（number、boolean、function） | 抛错 |
| `tokens` 存在且非普通对象 | 抛错 |
| `tokens` 含五个已知键之外的键 | 抛错（拦住 `input_tokens` 这类拼写错误） |
| `tokens` 的值非整数、为负、或超过 `int32` 上限 | 抛错 |

**Content-Type**：响应体非空且 `headers` 未提供 `Content-Type` 时，默认写 `application/json`；`headers` 提供则以脚本为准（流式场景脚本自行返回 `text/event-stream` 加手工拼接的 SSE 文本）。

**body 回传通道**：与 `rewriteRequest` 一致，body 字符串通过 `globalThis.__picotera_bmr_out` 带出，再由 `readGlobalString` 读取，避免大 body 在 meta 结果里被二次 JSON 转义。glue 里用普通 `JSON.stringify`（不带 markerReplacer），所以 `body: ctx.request.body` 这类 Proxy 会被完整物化为普通 JSON，与脚本自己调用 `JSON.stringify(proxy)` 的语义一致。

## 记录语义

hook 接管后**不插入任何上游 `request` 行**，只终结 meta 行：

| 字段 | 值 |
| --- | --- |
| `status_code` | 脚本返回的 `statusCode` |
| `finish_reason` | 2xx → `3`（EOF，正常结束）；其余 → `1`（内部错误） |
| `error_message` | 2xx → 不写（保持 NULL）；其余 → 响应体文本 |
| `time_spent_ms` | `time.Since(f.startedAt)` |
| `input_tokens` 等五个 token 列 | 脚本 `tokens` 里提供了哪个就写哪个，未提供的保持 NULL |
| `model_cost` / `model_cost_currency` | 脚本提供了 `tokens` 时按 `costsFor(ctx, f.model.Routed, …)` 计算；未提供则不写 |
| `provider_id` / `upstream_model` / `ttft_ms` | 不写（保持 NULL） |
| `model` / `user_id` / `project_id` / `api_key_id` | 保持流程前段已回填的值 |

token 列各自独立可选：脚本只填 `outputTokens` 时，其余四列保持 NULL。费用沿用与所有上游成功路径相同的 `costsFor`（按 `model` 表的 pricing 计算，缺定价则留空），这样脚本快速响应在费用统计里与真实请求同口径。

因为 2xx 也要走「不写 `error_message`」，这里不复用 `failMeta`（它总是写 `error_message`），而是直接用 `updateMeta` + `newRequestUpdate`，metaFinal 快照因此自动完整。

响应 artifact 走 `uploadMetaResponseArtifact`，带上脚本响应的状态码、最终响应头、`artifactBody(body)`（受 OTR 模式约束）与 `collectLogs()`。

`requestFinished` 仍由 `run()` 的 defer 触发（`finish_reason` 已写入，`metaFinal.set` 为 true）；`updateMeta` 会把 token 与费用一并合入 metaFinal，所以脚本填了 `tokens` 时 `requestFinished` 就能读到，没填则为 0 —— 即需求中的「当作是没有」。`providerId` 与 `ttftMs` 始终为 0。

**已知影响（不做补偿）**：成功率统计的口径是 `finish_reason = 3` 且 `output_tokens != 0`，所以 2xx 快速响应只有在脚本填了非零 `outputTokens` 时才计成功，否则落入「空回复」。请求列表中这类请求的特征是有 meta 行、无上游子行、`provider_id` 为空。

## 失败行为

`beforeMetaRequest` 不是观察性 hook（它能决定响应），因此与 `sortProviders` / `beforeRequest` 一致：tap 抛错、校验失败或超时时走 `failHook` —— 请求立即失败（502，超时 503），不再尝试任何渠道。这与 `afterUpstreamError` / `requestFinished` 的「出错只记日志」不同。

## 涉及改动面

| 层 | 改动 |
| --- | --- |
| `pkg/jsx/types.go` | 新增 `ResponseShape` |
| `pkg/jsx/session.go` | 新增 `RunBeforeMetaRequest()`，含 glue IIFE、严格校验、body 出参与 header 名黑名单检查 |
| `pkg/jsx/iface.go` | `Session` 接口新增方法 |
| `pkg/jsx/sdk.js` | `picotera.hooks` 新增 `beforeMetaRequest` waterfall |
| `pkg/server/gateway_flow.go` | `run()` 中插入调用 |
| `pkg/server/gateway_flow_script_response.go`（新文件） | `runBeforeMetaRequest()`、`writeScriptResponse()`（纯函数，便于测试）、`respondScriptResponse()` |
| 文档 | `docs/scripting.md`、`CLAUDE.md` 的 Scripts 小节 |

无 DB 迁移、无 sqlc 改动、无 API contract 改动，因此不需要重新生成 `openapi.yaml` 或前端类型；仪表盘无硬编码 hook 名列表，无需改动。

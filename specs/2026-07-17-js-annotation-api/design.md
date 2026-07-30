# Design

## 概述

本次改动把「脚本给请求打注解」从**会话内累积器 + 流程末尾落库**改为**显式的、按 id 直写 DB 的 SDK API**，并把注解能力扩展到 provider / api_key 两张配置表；同时新增 `requestFinished` hook，在 meta 行完成原因落库之后回调脚本，携带内存中累积的完整终态。

四块变更：

1. **移除 annotations 代理**：`ctx.metaRequest.annotations` 与 `ctx.upstreamRequest.annotations` 的 Proxy、`__picotera_anno_*` 宿主函数、Go 侧累积器（`metaAnnotations` / `upstreamAnnotations`）、`MetaAnnotations()` / `UpstreamAnnotations()` / `ResetUpstreamAnnotations()`、`persistRequestAnnotations` 及其调用点全部删除。`ctx.metaRequest` / `ctx.upstreamRequest` 只保留标识字段 `{ id, spanId, parentSpanId, traceId }`。
2. **新增 jsx.HostAPI**：jsx 包定义宿主能力接口，pkg/server 实现（包一层 db.Querier），注入 `jsx.NewEngine`。SDK 暴露 `picotera.request.setAnnotation`、`picotera.provider.{get,setAnnotation}`、`picotera.apiKey.{get,setAnnotation}`。
3. **新增 SQL**：三条按 key 原子读改写 annotations JSONB 的 UPDATE（request / provider / api_key），一条无用户过滤的 `GetApiKeyByID`；`UpdateRequest` 移除 `annotations` 列（该写路径随累积器一起删除）。
4. **requestFinished hook**：gateway flow 用 `metaOutcome` 累积器镜像 meta 行的每次更新，`run()` 的 defer 里（meta 终态已落库、session 关闭之前）以完整快照为输入运行 `picotera.hooks.requestFinished` waterfall。

## ctx 变化

- `ctxInit` 增加 `metaRequest: null, upstreamRequest: null` 两个零态字段。
- 新类型 `jsx.RequestRef`：

  ```go
  type RequestRef struct {
      ID           string  `json:"id"`
      SpanID       string  `json:"spanId"`
      ParentSpanID *string `json:"parentSpanId"` // 无入站 session 头时 null
      TraceID      *string `json:"traceId"`      // 无 trace（无 parentSpanId 或 upsert 失败）时 null
  }
  ```

  字段取值与 request 行的 DB 语义一致：meta 的 `spanId` 是 meta id 本身，upstream 的 `spanId` 也是 meta id；`parentSpanId` 来自 `extractParentSpanID`；`traceId` 是 `traces.id`（按 `(parent_span_id, user_id)` 定位，meta 与 upstream 共享同一个）。flow 上加辅助方法 `f.requestRef(id string) *jsx.RequestRef` 统一拼装。
- **traceId 的来源**：`Server.upsertTrace` 改为返回 trace id（`UpsertTrace` 本就是 `:one RETURNING id`，`ON CONFLICT DO UPDATE` 时返回既有行 id；跳过/失败时返回 `""`）。`authenticateAndBackfill` 把返回值存进 `f.meta.TraceID`（`gatewayMetaState` 新增字段）——时序先于 `MetaRequest` patch。`insertRequest` 内部的 upsertTrace 调用继续忽略返回值。
- `jsx.ContextPatch` 新增 `MetaRequest *RequestRef`。gateway flow 在 `resolveAndRewriteModel` 的首次 `PatchContext` 中带上 `MetaRequest: f.requestRef(f.meta.ID)`。fetch-models 会话（`handle_provider_endpoint.go`）不设置，`ctx.metaRequest` 保持 `null`——它的 session id 是合成串，不对应任何 request 行。
- `Session` 新增 `SetUpstreamRequest(ref *RequestRef) error`：ref 为 nil 时求值 `globalThis.ctx.upstreamRequest = null`，否则赋整个 ref 的 JSON。调用点：
  - `runSingleAttempt` 开头（原 `ResetUpstreamAnnotations` 位置）传 nil 复位——beforeRequest 阶段还没有 upstream 行，脚本看到 `null` 而不是上一轮的陈旧值；
  - `insertUpstreamAttempt` 成功后立即传 `f.requestRef(input.UpstreamID)`——rewriteRequest / afterUpstreamError / 流内错误 hook 都能拿到当前尝试的行标识。失败处理与既有 hook 错误一致：`recordAttemptFailure` + `failHook` + 终止流程。

## HostAPI（jsx ↔ server 边界）

jsx 包新增接口（保持 jsx 不依赖 contract 的既有约定，直接复用 jsx 已有的 Summary 类型）：

```go
type HostAPI interface {
    SetRequestAnnotation(ctx context.Context, requestID, key string, value *string) error
    SetProviderAnnotation(ctx context.Context, providerID int32, key string, value *string) error
    SetApiKeyAnnotation(ctx context.Context, apiKeyID int32, key string, value *string) error
    GetProvider(ctx context.Context, providerID int32) (*ProviderSummary, error) // (nil, nil) = 不存在
    GetApiKey(ctx context.Context, apiKeyID int32) (*ApiKeySummary, error)       // (nil, nil) = 不存在
}
```

`value == nil` 表示删除该 key。`jsx.NewEngine` 增加第四个参数 `hostAPI HostAPI`。

server 侧实现 `jsxHostAPI`（新文件 `pkg/server/jsx_host.go`），包 `db.Querier`：

- set 系列调用对应的 `:execrows` 查询，`0 rows` 返回 `xxx %d not found` 错误（写不存在的行必须报错，不静默吞掉）。
- `GetProvider` → `GetProviderByID`，`pgx.ErrNoRows` → `(nil, nil)`；行转 `jsx.ProviderSummary`（id/name/priority/annotations/disabled，凭据不出边界）。
- `GetApiKey` → 新查询 `GetApiKeyByID`（无 `user_id` 过滤——脚本是管理员编写的全局代码，与 `GetProviderByID` 同级），行转 `jsx.ApiKeySummary`（复用 `apiKeySummaryFromRow`）。
- **上下文脱离取消**：每个方法内部用 `context.WithTimeout(context.WithoutCancel(ctx), 5s)` 执行 DB 操作，与 `gatewayContexts.Persist()` 的惯例一致。这样 `requestFinished` 阶段（客户端可能已断开、dashboard 可能已 interrupt）的注解写入仍能落库。

会话把自己的 `s.ctx` 传给 HostAPI；宿主函数是同步阻塞调用，与 kv / fetch 相同（5s 超时是唯一的阻塞兜底）。

## SDK API 校验语义（fail fast）

JS 侧严格校验（Go 侧对 key 做防御性复查）：

- `requestId`：非空字符串，否则 `TypeError`；
- `providerId` / `apiKeyId`：`Number.isInteger`，否则 `TypeError`；
- `key`：非空字符串，否则 `TypeError`；
- `value`：`null` / `undefined` → 删除；字符串（含空串）→ 写入；其余类型 → `TypeError`（不做隐式 `String()` 转换）。

宿主边界用 JSON 编码传 value（`""` 表示删除、`"\"...\""` 表示字符串值），与原 `__picotera_anno_get` 区分空串/缺失的编码惯例相同。get 系列对不存在的 id 返回 `null`（与 `picotera.kv.get` 的缺失语义对齐）；set 系列对不存在的 id 抛错。

## SQL

三条 set 查询都是单语句原子 JSONB 读改写（`:execrows`）：

- `provider.annotations` / `api_key.annotations` 均为 `NOT NULL`：`annotations || jsonb_build_object(key, value)` / `annotations - key`。`api_key` 同时刷新 `updated_at = now()`。
- `request.annotations` 可空且有 partial GIN 索引（`WHERE annotations IS NOT NULL`）：写入用 `COALESCE(annotations,'{}') || ...`；删除用 `NULLIF(COALESCE(annotations,'{}') - key, '{}')`，保证删空后回到 `NULL`，索引体量继续只随打标行数增长。
- `request` 按 `id` 单条件 UPDATE（hypertable 主键 `(id, created_at)` 前缀即可走各 chunk 的 PK 索引）。
- `UpdateRequest` 删除 `annotations` CASE 行及 `requestUpdate.Annotations` builder——整列唯一写方变为 `SetRequestAnnotation`，读侧（列表过滤、视图）不变。

## requestFinished hook

- **注册**：`picotera.hooks.requestFinished`，waterfall 形状与其它 hook 一致；返回值忽略（纯观察，典型用途：用量统计、按终态打注解）。
- **输入**（`jsx.RequestFinishedView`，全部来自内存）：`requestId`、`statusCode`、`finishReason`（DB 数值码）、`errorMessage`、`timeSpentMs`、`ttftMs`、`inputTokens`、`outputTokens`、`cacheReadTokens`、`cacheWriteTokens`、`cacheWrite1hTokens`、`modelCost`（float64）、`modelCostCurrency`、`providerId`、`model`、`upstreamModel`。未发生的字段为零值（如纯失败路径没有 token/cost/providerId）。
- **终态累积**：`gatewayFlow` 增加 `metaOutcome` 快照与 `updateMeta(ctx, u *requestUpdate)` 包装——先 `h.updateRequest`，再按 `u.p` 的 `Set*` 标志把值合并进快照（`pgtype` 无效值合并为零值，`SetFinishReason` 同时置 `set=true`）。**所有** meta 行 update 调用点改走该包装（包括 model / provider 等非终态回填，正好为快照补齐 model 字段）：
  - `gateway_flow.go`：`authenticateAndBackfill` ×2、`updateMetaModel`；
  - `gateway_flow_errors.go`：`failMeta`；
  - `gateway_flow_success.go`：`markPathHeadersReceived`、`openPathInternalReader` ×2、`completeGatewaySuccess`；
  - `gateway_unified_helpers.go`：`unifiedStreamSuccess`（header 更新 + 终态更新）、`failUnifiedSuccess`、`failUnifiedSuccessCommitted`——`unifiedStreamArgs` 增加 `flow *gatewayFlow` 字段供这三处取到包装方法。
  - upstream 行的 update 不动，仍直接走 `h.updateRequest`。
- **触发点**：`run()` 的 session defer 改为「`runRequestFinished()` → `session.Close()`」。`runRequestFinished` 仅当 `metaOutcome.set`（完成原因已写过）时构造视图并调用 `session.RunRequestFinished`；错误（含 tainted session 的 `ErrHookTimeout`）记日志即止，绝不影响响应。所有终态写入（含 unified 路径）都在 `run()` 返回前同步完成（`updateRequest` 是同步调用），defer 时序天然满足「更新完成原因之后」。
- **已知边界**（记录为行为约定，不做补偿）：
  - session 在 `resolveAndRewriteModel` 才创建，之前失败的请求（认证失败、读体失败等）没有 VM，hook 不触发；
  - meta 响应 artifact 在终态更新前后就已上传，`requestFinished` 里的 `console.*` 输出会进 logx 但**不会**出现在 meta artifact 的日志里。

## 行为迁移说明

- 原来「beforeRequest 起写的 upstream 注解归属当前尝试、skip 的尝试写入被丢弃」的归属语义消失——脚本现在显式拿 `ctx.upstreamRequest.id` 写库，写到哪行由脚本自己负责；beforeRequest 阶段 `ctx.upstreamRequest` 为 `null`，天然写不了未创建的行。
- 注解写入从「会话末尾整 map 覆盖」变为「每 key 一次同步 DB 写」；热路径脚本应避免循环高频调用（与 kv/fetch 同样的代价模型）。
- provider / api_key 注解写入影响**后续**请求的 `ctx.annotations` 合并视图；当前会话已 Patch 进 ctx 的快照不回刷。

## 测试

- `pkg/jsx/annotations_test.go` 重写：用 fake `HostAPI` 断言五个 SDK 方法的透传参数、删除语义（null/undefined → `value==nil`）、校验抛错（非字符串 value / 非整数 id / 空 key）、get 的 `null`（not found）与对象返回、宿主错误转 JS 异常。
- 新增 `requestFinished` 用例：tap 收到完整输入字段；无 tap 时 `RunRequestFinished` 无副作用；tainted session 返回 `ErrHookTimeout`。
- `ctx.metaRequest` / `ctx.upstreamRequest`：断言 `{id, spanId, parentSpanId, traceId}` 形状（含 `parentSpanId`/`traceId` 为 `null` 的用例）、attempt 间复位为 `null`、`MetaRequest` patch。
- `pkg/server`：`metaOutcome.merge` 的纯单测（各 `Set*` 标志、pgtype 无效值、多次合并覆盖）；现有构造 `jsx.NewEngine` 的测试（`engine_test.go`、`large_body_test.go`、`gateway_flow_attempts_test.go`）补 stub HostAPI 参数。

不引入第三方库；不涉及管理 API / OpenAPI / dashboard 改动（openapi.yaml 无需重新生成）。

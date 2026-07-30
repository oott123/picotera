# 执行计划

按依赖顺序执行；每步完成后应能编译（`go build ./...`），除非步骤内标注了跨步依赖。

## 1. SQL 与 sqlc 再生成

- `db/queries/request.sql`：
  - 新增 `SetRequestAnnotation :execrows`（见 api.md）；
  - 从 `UpdateRequest` 删除 `annotations = CASE WHEN sqlc.arg('set_annotations') ...` 一行。
- `db/queries/provider.sql`：新增 `SetProviderAnnotation :execrows`。
- `db/queries/api_key.sql`：新增 `GetApiKeyByID :one` 与 `SetApiKeyAnnotation :execrows`。
- 运行 `sqlc generate`。
- `pkg/server/request_update.go`：删除 `Annotations` builder 方法（sqlc 再生成后 `SetAnnotations` 字段消失，此文件不删会编译失败——与第 5 步的调用点删除同批提交）。

## 2. jsx 类型与接口

- `pkg/jsx/types.go`：
  - 新增 `RequestRef`（`ID` / `SpanID` string，`ParentSpanID` / `TraceID` *string，JSON 为 null 表示缺失）、`RequestFinishedView`（字段见 api.md）；
  - `ContextPatch` 新增 `MetaRequest *RequestRef \`json:"metaRequest,omitempty"\``。
- `pkg/jsx/iface.go`：
  - 新增 `HostAPI` 接口；
  - `Session` 移除 `MetaAnnotations` / `UpstreamAnnotations` / `ResetUpstreamAnnotations`，新增 `SetUpstreamRequest(ref *RequestRef) error` 与 `RunRequestFinished(input RequestFinishedView) error`（注释说明：结果忽略、仅观察）。
- `pkg/jsx/engine.go`：`qjsEngine` 增加 `hostAPI HostAPI` 字段；`NewEngine` 增加第四参数。

## 3. jsx 会话与宿主函数

- `pkg/jsx/session.go`：
  - 删除 `metaAnnotations` / `upstreamAnnotations` / `upstreamAnnoInstalled` 字段、`annoSlot*` 常量、`annoSet/annoMap/annoGet/annoDel/annoHas/annoKeys/annoSnapshot`、`MetaAnnotations/UpstreamAnnotations/ResetUpstreamAnnotations`，以及 `newSession` 里 meta annotations Proxy 的安装 eval；
  - `ctxInit` 增加 `metaRequest: null, upstreamRequest: null`；
  - 新增 `SetUpstreamRequest(ref *RequestRef) error`：tainted 时返回 `ErrHookTimeout`；`ref==nil` 求值 `globalThis.ctx.upstreamRequest = null;`，否则用 `json.Marshal(ref)` 拼 `globalThis.ctx.upstreamRequest = {...};`；
  - 新增 `RunRequestFinished(input RequestFinishedView) error`：`mustJSON` 序列化后 evalJSON 运行 `picotera.hooks.requestFinished.runWaterfall(globalThis.ctx, <init>)`，IIFE 恒返回 `undefined`，丢弃结果只回传错误。
- `pkg/jsx/helpers.go`：
  - 删除 `registerAnnotations`；新增 `registerHostAPI(s)`（在 `registerHelpers` 中替换），注册五个宿主函数（签名见 api.md）：
    - set 系列：Go 侧防御性校验 key 非空；`valueJSON==""` → `value=nil`，否则 `json.Unmarshal` 到 string（失败即报错）；调用 `s.engine.hostAPI.SetXxx(s.ctx, ...)`；
    - get 系列：`(nil, nil)` → 返回 `("", nil)`；否则 `json.Marshal` Summary 返回。
- `pkg/jsx/sdk.js`：
  - `picotera.hooks` 增加 `requestFinished: new Waterfall()`；
  - 删除 `__picotera_makeAnnotationsProxy`；
  - 新增 `picotera.request` / `picotera.provider` / `picotera.apiKey` 三个命名空间，JS 侧校验按 design.md「fail fast」小节实现（校验辅助函数放在 IIFE 内部，不挂 globalThis）。

## 4. server 侧 HostAPI 实现与接线

- 新文件 `pkg/server/jsx_host.go`：
  - `type jsxHostAPI struct{ queries db.Querier }` + `newJSXHostAPI(q db.Querier) *jsxHostAPI`；
  - 每个方法先 `ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)`；
  - set 系列：`value *string` → `pgtype.Text{String: *value, Valid: value != nil}` 传给对应 `:execrows` 查询；0 行返回 `fmt.Errorf("request %q not found", id)`（provider/apiKey 同理）；
  - `GetProvider`：`GetProviderByID` + 行转 `jsx.ProviderSummary`（annotations JSONB 解码失败按 `apiKeySummaryFromRow` 惯例给空 map）；`pgx.ErrNoRows` → `(nil, nil)`；
  - `GetApiKey`：`GetApiKeyByID` + 复用 `apiKeySummaryFromRow`；`pgx.ErrNoRows` → `(nil, nil)`。
- `pkg/server/server.go`：`jsx.NewEngine(..., queries, kvStore, newJSXHostAPI(queries))`。

## 5. gateway flow 改造

- `pkg/server/gateway_helpers.go`：`upsertTrace` 改为返回 trace id string（`UpsertTrace` 的 RETURNING id；`parentSpanID` 无效 / `userID` 无效 / DB 错误时返回 `""`）；`insertRequest` 内部调用忽略返回值。
- `pkg/server/gateway_flow.go`：
  - `gatewayFlow` 增加 `metaFinal metaOutcome` 字段；`gatewayMetaState` 增加 `TraceID string` 字段；
  - 新增 `(f *gatewayFlow) requestRef(id string) *jsx.RequestRef`：`SpanID = f.meta.ID`，`ParentSpanID` 由 `f.meta.ParentSpanID`（空串 → nil）、`TraceID` 由 `f.meta.TraceID`（空串 → nil）转指针；
  - `run()` 的 session defer 改为：`f.runRequestFinished(); f.session.Close()`（删除 meta `persistRequestAnnotations` 调用）；
  - 删除 `persistRequestAnnotations` 方法；
  - `authenticateAndBackfill`：`f.meta.TraceID = f.h.upsertTrace(...)`；其两次 meta update 与 `updateMetaModel` 改走 `f.updateMeta`；
  - `resolveAndRewriteModel` 首次 `PatchContext` 增加 `MetaRequest: f.requestRef(f.meta.ID)`。
- 新文件 `pkg/server/gateway_flow_finish.go`：
  - `type metaOutcome struct{ set bool; statusCode, finishReason, timeSpentMs, ttftMs, inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens, cacheWrite1hTokens, providerID int32; errorMessage, modelCostCurrency, model, upstreamModel string; modelCost float64 }`；
  - `(o *metaOutcome) merge(p db.UpdateRequestParams)`：对每个 `Set*` 标志把对应值写入快照（pgtype 无效 → 零值；`ModelCost` 用 `Float64Value()`；`SetFinishReason` 时置 `o.set = true`）；
  - `(f *gatewayFlow) updateMeta(ctx context.Context, u *requestUpdate)`：`f.h.updateRequest(ctx, u)` + `f.metaFinal.merge(u.p)`（需要把 `requestUpdate.p` 暴露给同包读取，已同包无障碍）；
  - `(f *gatewayFlow) runRequestFinished()`：`f.session == nil || !f.metaFinal.set` 时直接返回；否则组装 `jsx.RequestFinishedView{RequestID: f.meta.ID, ...}` 调 `RunRequestFinished`，错误 `logx.Warn`。
- `pkg/server/gateway_flow_attempts.go`（`runSingleAttempt`）：
  - 开头的 `ResetUpstreamAnnotations()` 改为 `SetUpstreamRequest(nil)`（错误处理不变：`failHook` + 终止）；
  - 删除 upstream `persistRequestAnnotations` 的 defer；
  - `insertUpstreamAttempt` 成功后调用 `SetUpstreamRequest(f.requestRef(input.UpstreamID))`，失败按既有 hookErr 分支处理：`recordAttemptFailure(state, input, side.ProviderID, int32(gatewayHookStatus(err)), err, db.FinishReasonInternal)` + `failHook(err)` + `cancel()` + `return true, true`。
- `pkg/server/gateway_flow_errors.go`：`failMeta` 的 update 改走 `f.updateMeta`。
- `pkg/server/gateway_flow_success.go`：`markPathHeadersReceived` 的 meta update、`openPathInternalReader` 的两处 meta update、`completeGatewaySuccess` 的 meta update 改走 `input.Flow.updateMeta`；upstream 行 update 保持 `h.updateRequest`。
- `pkg/server/gateway_unified_helpers.go`：
  - `unifiedStreamArgs` 增加 `flow *gatewayFlow` 字段，`unifiedStreamArgsFromSuccess` 填 `input.Flow`；
  - `unifiedStreamSuccess`（header meta update + 终态 meta update）、`failUnifiedSuccess`、`failUnifiedSuccessCommitted` 的 meta 行 update 改走 `a.flow.updateMeta`。

## 6. 测试

- `pkg/jsx/engine_test.go`：`newTestEngine` 增加 fake HostAPI（记录调用参数的 stub，实现 `jsx.HostAPI`，可配置返回值/错误）；`large_body_test.go` 的 `NewEngine` 调用同步加参。
- `pkg/jsx/annotations_test.go` 重写为 HostAPI SDK 测试：
  - `picotera.request.setAnnotation`：字符串写入、空串写入、`null`/`undefined` 删除（断言 `value==nil`）、非字符串 value 抛 `TypeError`、空 key 抛 `TypeError`、非字符串 requestId 抛 `TypeError`、宿主错误转 JS `Error`；
  - `picotera.provider.get` / `picotera.apiKey.get`：命中返回对象（字段核对）、miss 返回 `null`、非整数 id 抛 `TypeError`；
  - `picotera.provider.setAnnotation` / `picotera.apiKey.setAnnotation`：透传与校验同上；
  - `ctx.metaRequest` / `ctx.upstreamRequest`：`MetaRequest` patch 后 `{id, spanId, parentSpanId, traceId}` 四字段可读（含 `parentSpanId`/`traceId` 为 `null` 的用例）；`SetUpstreamRequest(nil)` → `null`、非 nil → 完整对象；
  - `RunRequestFinished`：tap 能读到输入的全部字段；无 tap 不报错；tainted session 返回 `ErrHookTimeout`。
- `pkg/server`：
  - 新增 `metaOutcome.merge` 单测（gateway_flow_finish_test.go）：终态字段合并、多次合并覆盖、pgtype 无效值归零、`set` 标志仅随 `SetFinishReason` 置位、`ModelCost` Numeric→float64；
  - `gateway_flow_attempts_test.go:57` 的 `jsx.NewEngine` 调用补 stub HostAPI。

## 7. 文档

- `CLAUDE.md`：
  - Scripts 一节：waterfall 列表加 `requestFinished`（触发时机、输入形状、结果忽略）；
  - 整段替换「Request annotations」小节：删除 metaRequest/upstreamRequest annotations 代理的描述，改写为 `picotera.request/provider/apiKey.setAnnotation` + `get` 的语义（value 类型规则、not-found 行为、脱离取消的 5s 写库上下文）、`ctx.metaRequest`/`ctx.upstreamRequest` 的 `{id, spanId, parentSpanId, traceId}` 形状与 attempt 复位语义；
  - Database Schema 一节：`request.annotations` 的写方描述由「script layer 终态 UPDATE 整列写入」改为「`SetRequestAnnotation` 按 key 原子读改写、删空回 NULL」。

## 8. 验证

- `go build ./...`；
- `go test ./pkg/jsx/... ./pkg/server/...`；
- `grep -rn "anno_get\|anno_set\|makeAnnotationsProxy\|MetaAnnotations\|persistRequestAnnotations\|ResetUpstreamAnnotations" pkg/` 应无残留；
- 不需要 `mise run openapi` / dashboard 再生成（管理 API 无变化）。

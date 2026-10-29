# Plan — afterUpstreamError JS Hook

## 1. pkg/jsx — 新增 waterfall

1. `types.go`：新增 `UpstreamErrorView`（输入）与 `AfterUpstreamErrorDecision`（输出）结构（见 `api.md`）。`LastError` 不动。
2. `sdk.js`：在 `globalThis.picotera.hooks` 里加入 `afterUpstreamError: new Waterfall()`。
3. `session.go`：新增 `RunAfterUpstreamError(initial UpstreamErrorView) (AfterUpstreamErrorDecision, error)`，仿 `RunBeforeRequest`：
   - 用 `mustJSON(initial)` 生成初值；
   - waterfall 表达式：passthrough（`undefined`/`null`/返回 `ctx`）→ 返回 `undefined`；否则规范化为 `{ break: !!r.break, statusCode: r.statusCode|0, message: typeof r.message==='string'?r.message:'' }`；
   - `evalJSON` → 解码为 `AfterUpstreamErrorDecision`，passthrough 时返回 `initial`（`break=false`）。
4. `iface.go`：`Session` 接口加入 `RunAfterUpstreamError`。
5. `engine_test.go`（可选）：加一个 hook 注册 + 触发的小测试，覆盖 break / passthrough / streamed。

## 2. pkg/server — 触发与 break 响应

### 2.1 共用 helper（gateway_flow_attempts.go）

- 新增 `func (f *gatewayFlow) runAfterUpstreamError(state *attemptState, streamed bool) (jsx.AfterUpstreamErrorDecision, bool)`：
  - 前置条件：`state.LastJSErr` 已由 `updateAttemptState` 写好；
  - `PatchContext(jsx.ContextPatch{Attempt: &jsx.AttemptState{CurrentRetryCount: state.CurrentRetryCount, TotalAttemptCount: state.TotalAttemptCount, LastError: state.LastJSErr}})`；
  - 调 `f.session.RunAfterUpstreamError(jsx.UpstreamErrorView{StatusCode: state.LastJSErr.StatusCode, Message: state.LastJSErr.Message, Streamed: streamed})`；
  - hook 报错：`logx` 记录，返回 `(decision{}, false)`（不 break）；
  - 返回 `(dec, dec.Break && !streamed)`。

- 新增 `func (f *gatewayFlow) respondUpstreamErrorBreak(dec jsx.AfterUpstreamErrorDecision, origStatus int, origBody []byte, origHeader http.Header)`：
  - `status := dec.StatusCode; if status<=0 { status=origStatus }; if status<=0 { status=http.StatusBadGateway }`；
  - `body`/`contentType`：`dec.Message!=""` → `[]byte(dec.Message)` + `application/json`；否则 `origBody` + `origHeader.Get("Content-Type")`（空则 `application/json`）；
  - 写出 `f.w`（Set Content-Type → WriteHeader(status) → Write(body)）；
  - `f.failMeta(int32(status), <dec.Message 或 string(origBody)>, db.FinishReasonInternal)`；
  - `uploadMetaResponseArtifact`（含 `collectLogs()`）。

### 2.2 runSingleAttempt 失败分支接线（gateway_flow_attempts.go）

把现有「记录失败后 `return false,false` 继续」的分支改为「记录失败 → 跑 hook → 命中 break 则响应并 `return true,true`」。涉及分支：

- `insertUpstreamAttempt` 失败：`recordAttemptFailure(... status 0 ...)` 后，`if brk := f.runAfterUpstreamError(state,false); brk { f.respondUpstreamErrorBreak(dec, 0, []byte(err.Error()), nil); cancel(); return true,true }`。
- `buildRewrittenUpstreamRequest` **非** hook 错误分支（L135）：同上，`origStatus=0`，`origBody=[]byte(err.Error())`，`origHeader=nil`。
- `buildRewrittenUpstreamRequest` 的 `gatewayHookError` 分支（L128-134）：**不接 hook**，保持现状。
- `forwardRequest` 失败：同 insert 分支处理。
- `handleUpstreamNonOK`：改为返回 `(dec jsx.AfterUpstreamErrorDecision, breakNow bool, origStatus int, origBody []byte, origHeader http.Header)`，或在其内部直接调用 helper 并返回 `breakNow bool`。采用内部调用方案：`handleUpstreamNonOK` 末尾在 `updateAttemptState` 之后调用 `runAfterUpstreamError(state,false)`，命中 break 则调用 `respondUpstreamErrorBreak(dec, resp.StatusCode, respBody, resp.Header)` 并返回 `true`。`runSingleAttempt` 据返回值决定 `return true,true` 或 `return false,false`。

  > 因 `runAfterUpstreamError` 依赖 `state.LastJSErr`，`handleUpstreamNonOK` / 各失败分支必须先 `recordAttemptFailure` / `updateAttemptState` 再跑 hook。

由于多个分支需要 `dec` 与 break 标志，统一让 `runAfterUpstreamError` 返回 `(dec, breakNow)`，各分支据此调用 `respondUpstreamErrorBreak`。

### 2.3 successInput 计数透传

- `successInput` 增加 `CurrentRetryCount int` / `TotalAttemptCount int`，在 `runSingleAttempt` 构造 `successInput` 时从 `input.CurrentRetryCount` / `input.TotalAttemptCount` 填入（供流内错误分支构造 `AttemptState` 计数）。

### 2.4 流内错误触发（break 忽略，streamed=true）

两处在判定 `streamErr != ""` 后、写 DB 完成行附近，运行 hook（忽略返回的 break）：

- `gateway_flow_success.go::completeGatewaySuccess`：构造 `lastErr := &jsx.LastError{ProviderID: int(input.ProviderID), StatusCode: input.Response.StatusCode, Message: streamErr}`；`PatchContext` attempt（计数取 `input.CurrentRetryCount`/`input.TotalAttemptCount`，`LastError: lastErr`）；调 `f.session.RunAfterUpstreamError(jsx.UpstreamErrorView{StatusCode: input.Response.StatusCode, Message: streamErr, Streamed: true})`；错误仅记日志。封装为 `func (f *gatewayFlow) runStreamErrorHook(providerID int32, currentRetry, totalAttempt, statusCode int, message string)` 复用。
- `gateway_unified_helpers.go::unifiedStreamSuccess`：`streamErr != ""` 分支调用同一 `runStreamErrorHook`，`providerID=a.providerID`，计数取 `input.CurrentRetryCount`/`input.TotalAttemptCount`，`statusCode=resp.StatusCode`，`message=streamErr`。

  > `runStreamErrorHook` 内部自行 `PatchContext` + `RunAfterUpstreamError`，不依赖 `attemptState`（成功路径没有 `state`）。

## 3. 验证

- `go build ./...`、`go test ./pkg/jsx/... ./pkg/server/...`。
- 手动/单测覆盖：
  - HTTP 非 200 + `break=true`（透传上游 status+body）；
  - HTTP 非 200 + `break=true` + 自定义 statusCode/message；
  - `break=false` 时正常重试到下一个 provider，且下一个 `beforeRequest` 能读到 `ctx.attempt.lastError`；
  - 连接失败（status 0）+ `break=true` 回退 502；
  - 流内错误触发 hook 且 `break` 被忽略（响应已发出）。

## 4. 文档

- 更新 `CLAUDE.md` 的 Scripts 段，把 `afterUpstreamError` 加入 hook 列表与说明（触发时机、break 语义、streamed、lastError 时序）。

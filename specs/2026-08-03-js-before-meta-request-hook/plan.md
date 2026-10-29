# 执行计划：beforeMetaRequest Hook

## 1. `pkg/jsx/types.go`：新增 ResponseShape

在 `AfterUpstreamErrorDecision` 之后加入：

```go
// ResponseShape is the waterfall value for the beforeMetaRequest hook: a
// complete downstream response authored by a script, short-circuiting the
// upstream attempt loop. Body never crosses the JSON boundary (json:"-"): it is
// handed back out-of-band and carries the final response bytes (nil = empty
// body).
type ResponseShape struct {
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers"`
	Body       []byte              `json:"-"`
	Tokens     *ResponseTokens     `json:"tokens"`
}

// ResponseTokens is the optional usage block of ResponseShape. A nil field means
// the script did not report that counter and the column stays NULL.
type ResponseTokens struct {
	InputTokens        *int32 `json:"inputTokens,omitempty"`
	OutputTokens       *int32 `json:"outputTokens,omitempty"`
	CacheReadTokens    *int32 `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens   *int32 `json:"cacheWriteTokens,omitempty"`
	CacheWrite1hTokens *int32 `json:"cacheWrite1hTokens,omitempty"`
}
```

## 2. `pkg/jsx/sdk.js`：注册 waterfall

`globalThis.picotera.hooks` 中在 `sortProviders` 之后加入 `beforeMetaRequest: new Waterfall(),`。

## 3. `pkg/jsx/session.go`：实现 RunBeforeMetaRequest

新增方法（放在 `RunSortProviders` 之后，与执行顺序对齐）：

```go
// RunBeforeMetaRequest runs the beforeMetaRequest waterfall after sortProviders
// and before the first upstream attempt. It returns nil for passthrough
// (undefined / null) and a fully validated response otherwise; the contract is
// strict because the result is written straight to the client.
func (s *qjsSession) RunBeforeMetaRequest() (*ResponseShape, error)
```

glue IIFE（`internalFilename("hook-beforeMetaRequest.js")`，风格对齐既有 hook 表达式）：

```js
(function () {
  var r = picotera.hooks.beforeMetaRequest.runWaterfall(globalThis.ctx, undefined);
  if (r === globalThis.ctx || typeof r === 'undefined' || r === null) return undefined;
  if (typeof r !== 'object' || Array.isArray(r)) {
    throw new Error("jsx: beforeMetaRequest result must be an object");
  }
  if (!Number.isInteger(r.statusCode) || r.statusCode < 100 || r.statusCode > 599) {
    throw new Error("jsx: beforeMetaRequest statusCode must be an integer in [100, 599]");
  }
  var headers = {};
  if (typeof r.headers !== 'undefined' && r.headers !== null) {
    if (typeof r.headers !== 'object' || Array.isArray(r.headers)) {
      throw new Error("jsx: beforeMetaRequest headers must be an object");
    }
    var keys = Object.keys(r.headers);
    for (var i = 0; i < keys.length; i++) {
      var k = keys[i], v = r.headers[k];
      if (typeof v === 'string') { headers[k] = [v]; continue; }
      if (Array.isArray(v)) {
        var ok = true;
        for (var j = 0; j < v.length; j++) { if (typeof v[j] !== 'string') { ok = false; break; } }
        if (ok) { headers[k] = v.slice(); continue; }
      }
      throw new Error("jsx: beforeMetaRequest header " + k + " must be a string or string[]");
    }
  }
  var tokens = null;
  if (typeof r.tokens !== 'undefined' && r.tokens !== null) {
    if (typeof r.tokens !== 'object' || Array.isArray(r.tokens)) {
      throw new Error("jsx: beforeMetaRequest tokens must be an object");
    }
    var allowed = ['inputTokens', 'outputTokens', 'cacheReadTokens', 'cacheWriteTokens', 'cacheWrite1hTokens'];
    tokens = {};
    var tkeys = Object.keys(r.tokens);
    for (var m = 0; m < tkeys.length; m++) {
      var tk = tkeys[m];
      if (allowed.indexOf(tk) < 0) {
        throw new Error("jsx: beforeMetaRequest unknown tokens key " + tk);
      }
      var tv = r.tokens[tk];
      if (typeof tv === 'undefined' || tv === null) continue;
      if (!Number.isInteger(tv) || tv < 0 || tv > 2147483647) {
        throw new Error("jsx: beforeMetaRequest tokens." + tk + " must be an integer in [0, 2147483647]");
      }
      tokens[tk] = tv;
    }
  }
  globalThis.__picotera_bmr_out = '';
  var b = r.body, bodyState;
  if (typeof b === 'undefined' || b === null) {
    bodyState = 'none';
  } else if (typeof b === 'string') {
    bodyState = 'raw';
    globalThis.__picotera_bmr_out = b;
  } else if (typeof b === 'object') {
    bodyState = 'json';
    globalThis.__picotera_bmr_out = JSON.stringify(b);
  } else {
    throw new Error("jsx: beforeMetaRequest body must be a string, object, array, null, or undefined");
  }
  return { statusCode: r.statusCode, headers: headers, bodyState: bodyState, tokens: tokens };
})()
```

Go 侧：

- `evalJSON("beforeMetaRequest", ...)`；`undef` → 返回 `(nil, nil)`；错误原样上抛。
- 解码到局部 meta 结构（`statusCode` / `headers` / `bodyState` / `tokens *ResponseTokens`）。
- `bodyState == "raw" | "json"` 时用 `readGlobalString("__picotera_bmr_out")` 取回 body 字节；`"none"` 时 `Body` 为 nil；未知 `bodyState` 返回错误。
- 防御性再校验（宿主侧，glue 已拦过一遍）：`statusCode` 落在 `[100, 599]`；遍历 header 名，`strings.EqualFold` 命中 `Content-Length` / `Transfer-Encoding` 时返回 `fmt.Errorf("jsx: beforeMetaRequest: header %q is not allowed", k)`；各 token 值非负。

## 4. `pkg/jsx/iface.go`：Session 接口

在 `RunSortProviders` 之后加入带注释的 `RunBeforeMetaRequest() (*ResponseShape, error)`。

## 5. `pkg/server/gateway_flow_script_response.go`（新文件）

```go
// runBeforeMetaRequest runs the beforeMetaRequest waterfall after sortProviders
// and before the first upstream attempt (it runs even when no candidate
// survived sorting). It returns true when the flow is finished: either the hook
// authored the downstream response, or it failed and failHook wrote the error.
func (f *gatewayFlow) runBeforeMetaRequest() bool
```

- `resp, err := f.session.RunBeforeMetaRequest()`；`err != nil` → `f.failHook(err)` + 返回 true。
- `resp == nil` → 返回 false。
- 否则 `f.respondScriptResponse(*resp)` + 返回 true。

```go
// writeScriptResponse writes a script-authored response to w and returns the
// bytes written. Script headers replace any header of the same name; a non-empty
// body defaults to application/json when the script set no Content-Type.
func writeScriptResponse(w http.ResponseWriter, resp jsx.ResponseShape) []byte
```

- 对每个 header：`w.Header().Del(k)` 后逐值 `Add`。
- `len(resp.Body) > 0 && w.Header().Get("Content-Type") == ""` → `Set("Content-Type", "application/json")`。
- `w.WriteHeader(resp.StatusCode)`；body 非空则写出；返回 body。

```go
// scriptResponseFinishReason maps a script-authored status onto a finish reason:
// 2xx is a normal end, anything else is an internal error.
func scriptResponseFinishReason(status int) int32
```

```go
// respondScriptResponse writes the script's response and finalizes the meta row.
// No upstream row exists on this path, so provider/token/cost columns stay NULL —
// the same shape as a pure-failure request.
func (f *gatewayFlow) respondScriptResponse(resp jsx.ResponseShape)
```

- `body := writeScriptResponse(f.w, resp)`。
- `fr := scriptResponseFinishReason(resp.StatusCode)`；`errMsg` 在 `fr == db.FinishReasonInternal` 时为 `pgtype.Text{String: string(body), Valid: true}`，否则 `pgtype.Text{Valid: false}`（不复用 `failMeta`，它总会写 `error_message`）。
- `pctx, pcancel := f.ctxs.Persist()`；构造 `u := newRequestUpdate(f.meta.ID, f.meta.CreatedAt).StatusCode(...).ErrorMessage(errMsg).TimeSpentMs(time.Since(f.startedAt)).FinishReason(...)`。
- 用量：`resp.Tokens != nil` 时，用 `scriptResponseTokensToPG(resp.Tokens)`（新增纯函数，把五个 `*int32` 映射为 `pgtype.Int4`，nil → `Valid: false`）取值，链上 `.InputTokens(...)` 等五个 setter，再 `modelCost, modelCcy := f.h.costsFor(pctx, f.model.Routed, in, out, cr, cw, cw1h)` 并链上 `.ModelCost(modelCost).ModelCostCurrency(modelCcy)`；`resp.Tokens == nil` 时这些列一律不写。
- `f.updateMeta(pctx, u)`。
- `f.h.uploadMetaResponseArtifact(pctx, f.meta.ID, f.meta.CreatedAt, resp.StatusCode, f.w.Header().Clone(), f.artifactBody(body), f.collectLogs(), nil)`。

## 6. `pkg/server/gateway_flow.go`：接入 run()

```go
	sorted, sidecars, ok := f.resolveAndSortCandidates()
	if !ok {
		return
	}
	if f.runBeforeMetaRequest() {
		return
	}
	result := f.runAttempts(sorted, sidecars)
```

`run()` 的 defer 会照常执行 `runRequestFinished()` 与 `session.Close()`。

## 7. 测试

`pkg/jsx/engine_test.go`（沿用现有 `newTestSession` 之类的构造方式）：

1. 无 tap → `RunBeforeMetaRequest()` 返回 `(nil, nil)`。
2. tap 返回 `undefined` → `(nil, nil)`。
3. tap 返回 `{statusCode: 200, body: {a: 1}}` → `Body` 为 `{"a":1}`，`StatusCode` 200，`Headers` 为空 map。
4. tap 返回字符串 body → `Body` 原样，不做 JSON 包装。
5. tap 返回 `headers: {'X-A': 'b', 'X-B': ['c','d']}` → 归一为 `map[string][]string`。
6. 无 body → `Body` 为 nil。
7. 校验失败各分支均返回错误：非对象、`statusCode` 越界/非整数、`headers` 为数组、header 值为数字、`body` 为数字、header 名为 `content-length`、`tokens` 为数组、`tokens` 含未知键（`input_tokens`）、token 值为负数/小数/超 `int32`。
7b. tap 返回 `tokens: {outputTokens: 12}` → 只有 `OutputTokens` 非 nil，其余四个为 nil；不带 `tokens` → `Tokens` 为 nil。
8. waterfall 串联：低优先级 tap 能读到高优先级 tap 返回的 shape 并改写 `statusCode`。
9. tainted session（超时后）调用直接返回 `ErrHookTimeout`（对齐 `annotations_test.go` 中的写法）。
10. tap 内 `return { statusCode: 200, body: ctx.request.body }`（配合 `SetClientBody`）→ Proxy 被完整物化为 JSON。

`pkg/server/gateway_flow_test.go`（无 DB，纯函数）：

11. `writeScriptResponse` 对 `httptest.NewRecorder()`：默认补 `application/json`；脚本自带 `Content-Type` 时不覆盖；空 body 时不写 `Content-Type`；多值 header 全部落地；返回值等于写出的字节。
12. `scriptResponseFinishReason`：200/204/299 → `db.FinishReasonEOF`；199/300/404/500 → `db.FinishReasonInternal`。
13. `scriptResponseTokensToPG`：只填 `outputTokens` 时仅该 `pgtype.Int4` 为 `Valid`，其余四个 `Valid: false`；填 0 时 `Valid` 为 true 且值为 0（与「未提供」区分）。

运行：`go build ./... && go test ./pkg/jsx/... ./pkg/server/...`。

## 8. 文档

`docs/scripting.md`：

- 开头「八个 hook」改为「九个 hook」，表格新增一行（置于 `sortProviders` 与 `beforeRequest` 之间）。
- mermaid 流程图在 `sortProviders` 与「尝试循环」之间插入 `beforeMetaRequest` 节点，并画出「返回 ResponseShape → 直接响应客户端 → requestFinished」的分支。
- 顺序说明列表新增一条。
- 「各 Hook 详解」在 `sortProviders` 与 `beforeRequest` 之间新增 `### beforeMetaRequest` 小节：时机、`ResponseShape` / `ResponseTokens` 定义、校验规则、Content-Type 默认值、记录语义（无上游行、`finish_reason` 映射、token 与费用按 `tokens` 写入、未填 `outputTokens` 时计入「空回复」）、失败行为（抛错即 502/503），以及 api.md 中的两个示例。
- 「错误与失败行为」小节中的例外列表保持不变（本 hook 不是观察性 hook）。

`CLAUDE.md` 的 Scripts 小节：hook 列表由八个改为九个，在 `sortProviders` 之后插入 `beforeMetaRequest` 条目，简述时机、`ResponseShape` 契约、短路语义与记录语义。

`docs/example-scripts/`：不新增文件（api.md 的示例已随 `docs/scripting.md` 落地）。

## 9. 不需要做的事

- 无 DB 迁移 / sqlc / contract 改动 → 不跑 `sqlc generate`、`mise run openapi`、`pnpm generate-openapi`。
- 仪表盘无硬编码 hook 名列表 → 前端零改动。

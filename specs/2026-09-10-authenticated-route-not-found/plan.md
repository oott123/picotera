# 执行计划

## 1. 鉴权结果值类型与路由错误构造器（`pkg/server/gateway_helpers.go`）

在 `authenticateClient` 之后新增：

```go
// clientAuth is the outcome of the pre-flight API-key check. The HTTP entry
// point resolves it before deciding how to answer the request — notably whether
// an unmatched path may fall back to the dashboard SPA — and hands it to the
// flow, so the key is looked up exactly once per request.
type clientAuth struct {
	APIKey *db.ApiKey
	User   *db.AppUser
	// Err is the *gatewayError authentication failed with; nil on success.
	Err error
}

func (a clientAuth) ok() bool { return a.Err == nil }

func (s *Server) authenticateGatewayClient(ctx context.Context, r *http.Request) clientAuth {
	apiKey, user, err := s.authenticateClient(ctx, r)
	return clientAuth{APIKey: apiKey, User: user, Err: err}
}
```

同文件新增路由未命中错误的构造器，替换现有三处字面量（`resolveEndpoint` 的 miss 分支、`handle_gateway.go` 的 escaped-prefix 不一致分支、`handle_unified_gateway.go` 的 codex 归一化失败分支）：

```go
func newRouteNotFoundError() *gatewayError {
	return &gatewayError{
		status:  http.StatusNotFound,
		message: "route not found",
		code:    errorx.RouteNotFound.Error(),
	}
}
```

`looksLikeBrowserNav` 的文档注释补一句：它只在请求未通过 API key 鉴权时才被咨询。

## 2. 流程消费预鉴权结果（`pkg/server/gateway_flow.go`）

- `gatewayRouteKind` 新增取值：

```go
const (
	gatewayRoutePath gatewayRouteKind = iota
	gatewayRouteUnified
	// gatewayRouteNotFound records a request that matched no endpoint. It only
	// exists for a client that authenticated successfully; the flow stops right
	// after the meta row is written and never resolves a model or a candidate.
	gatewayRouteNotFound
)
```

- `gatewayFlow` 新增字段 `preAuth clientAuth`；`newGatewayFlow` 增加一个 `auth clientAuth` 形参（放在 `startedAt` 与 `cfg` 之间）并写入该字段。
- `authenticateAndBackfill` 首行由 `f.h.authenticateClient(f.ctxs.Request, f.r)` 改为读取 `f.preAuth`：

```go
apiKey, user, err := f.preAuth.APIKey, f.preAuth.User, f.preAuth.Err
```

其余逻辑（失败时 `failMeta` + `failGatewayError`、成功时组装 `gatewayAuthState`、OTR、project、artifact、trace）完全不变。函数注释说明 key 已由 HTTP 入口解析。

- `run()` 在 `authenticateAndBackfill` 之后、注册 `requestFinished` defer 之前插入终止分支：

```go
if f.config.Kind == gatewayRouteNotFound {
	// No endpoint matched: the row exists purely so an authenticated client's
	// misrouted call is visible in the dashboard. No JS session is created, so
	// no hook — requestFinished included — runs.
	f.failGatewayErrorWithFallback(newRouteNotFoundError(), http.StatusNotFound, "route not found")
	return
}
```

（`failGatewayErrorWithFallback` 已能从 `*gatewayError` 取出 404 与消息，写入 `status_code`、`error_message`、`time_spent_ms`、`finish_reason = FinishReasonInternal`，并上传响应 artifact。）

## 3. catch-all 网关入口重排（`pkg/server/handle_gateway.go`）

`ServeHTTP` 改为：

```go
startedAt := time.Now()
endpoint, pathVars, suffix, err := h.resolveEndpoint(r.Context(), r.URL.Path)
if err != nil {
	if isRouteNotFound(err) {
		h.serveRouteNotFound(w, r, startedAt)
		return
	}
	handleGatewayErr(w, err)
	return
}
// suffix 重切分支不变（其中的 gatewayError 字面量换成 newRouteNotFoundError()）
// CORS + OPTIONS 短路不变
auth := h.authenticateGatewayClient(r.Context(), r)
if endpoint.EndpointType == contract.EndpointType_ModelList {
	h.handleModelList(w, r, endpoint, auth)
	return
}
newGatewayFlow(h, w, r, startedAt, auth, h.newPathGatewayFlowConfig(endpoint, pathVars, suffix)).run()
```

鉴权放在 OPTIONS 短路之后：预检不带凭据，不该为它查库。

新增两个函数：

```go
// serveRouteNotFound answers a request that matched no configured endpoint.
// A client holding a valid API key is an API client by definition: it gets the
// structured JSON 404 and a recorded meta row, never dashboard HTML, however
// browser-ish its headers look. Without one there is no user to attribute a row
// to, so the old split stands — a safe navigation falls through to the SPA,
// anything else gets the JSON 404 unrecorded.
func (h *gatewayHandler) serveRouteNotFound(w http.ResponseWriter, r *http.Request, startedAt time.Time) {
	auth := h.authenticateGatewayClient(r.Context(), r)
	if routeNotFoundFallsBackToSPA(r, auth.ok()) {
		h.staticHandler.ServeHTTP(w, r)
		return
	}
	if !auth.ok() {
		handleGatewayErr(w, newRouteNotFoundError())
		return
	}
	writeCORSHeaders(w, r)
	newGatewayFlow(h, w, r, startedAt, auth, newNotFoundGatewayFlowConfig(r)).run()
}

func routeNotFoundFallsBackToSPA(r *http.Request, authenticated bool) bool {
	return !authenticated && looksLikeBrowserNav(r)
}
```

以及未命中流程的配置——除 `Kind` 与 `RecordedEndpointPath` 外全部留零值，`run()` 在用到它们之前就已返回：

```go
// newNotFoundGatewayFlowConfig configures the record-only flow for an
// authenticated request to an unmatched path. Endpoint stays the zero value and
// the callbacks stay nil: run() terminates right after the meta row is written,
// before anything reads them. endpoint_path records the decoded path the router
// failed to match.
func newNotFoundGatewayFlowConfig(r *http.Request) gatewayFlowConfig {
	return gatewayFlowConfig{
		Kind:                 gatewayRouteNotFound,
		RecordedEndpointPath: r.URL.Path,
	}
}
```

## 4. 模型列表端点消费预鉴权结果（`pkg/server/handle_model_list.go`）

`handleModelList` 增加 `auth clientAuth` 形参，删除内部的 `h.authenticateClient` 调用，第 3 步改为：

```go
if !auth.ok() {
	handleGatewayErr(w, auth.Err)
	return
}
```

步骤注释相应更新为 "Reject an unauthenticated client."。

## 5. 统一路由与 codex 挂载（`pkg/server/handle_unified_gateway.go`）

- `handleUnifiedGenerate` 与 `handleUnifiedCodex` 的 `newGatewayFlow` 调用各增加一个实参：`h.authenticateGatewayClient(r.Context(), r)`。
- `handleUnifiedCodex` 的 `normalizeCodexSuffix` 失败分支由直接 `handleGatewayErr` 改为 `h.serveRouteNotFound(w, r, started); return`。该挂载只注册 POST/OPTIONS 且 OPTIONS 由 `corsMiddleware` 提前应答，因此 `routeNotFoundFallsBackToSPA` 在这里恒为 false——带有效 key 时记录 404，否则维持原来的 JSON 404。`writeCORSHeaders` 是幂等的 `Set`，与 `corsMiddleware` 已写的头重叠无副作用。分支注释说明：记录用的 `endpoint_path` 是客户端实际请求的路径，因此 `/backend-api` 别名会出现在这类 404 行上。

## 6. 单元测试（`pkg/server/handle_gateway_test.go`，新建）

仓库没有 postgres 测试夹具，因此只测纯判定：

- `TestRouteNotFoundFallsBackToSPA`：表驱动覆盖
  - GET + `Accept: text/html` + 未认证 → true；
  - GET + `Accept: text/html` + 已认证 → false；
  - GET + `Accept: */*` + 已认证 → false；
  - GET + `Accept: application/json` + 未认证 → false；
  - POST + 未认证 → false。
- `TestNewNotFoundGatewayFlowConfig`：`Kind == gatewayRouteNotFound`，`RecordedEndpointPath` 等于 `r.URL.Path`（含一个带 query 与百分号编码的用例，确认记录的是解码后的 path 且不含 query）。

`pkg/server/gateway_helpers_test.go` 若有构造 `gatewayError` 断言路由未命中的用例，同步改用 `newRouteNotFoundError()`。

## 7. 验证

```bash
go build ./... && go test ./pkg/server/... ./pkg/llmbridge/...
```

手工验证（`docker compose up -d` + `mise run server`）：

1. `curl -i -H 'Authorization: Bearer <有效key>' http://localhost:9898/nope` → 404 JSON，dashboard 请求列表出现一条 `endpoint_path = /nope`、状态码 404 的记录。
2. 同上但 `Accept: text/html` → 仍是 JSON。
3. 不带 key 浏览器访问 `http://localhost:9898/nope` → dashboard SPA。
4. 不带 key `curl -X POST http://localhost:9898/nope` → JSON 404，无记录。
5. `curl -i -X POST -H 'Authorization: Bearer <有效key>' http://localhost:9898/api/unified/codex/` → 404 JSON 且被记录。
6. 正常网关请求与 `/v1/models` 类端点行为不变。

## 8. 文档

`CLAUDE.md` 的 **Key Patterns → Endpoint matching** 段落追加一句：路由未命中时先解析 API key，鉴权成功的请求一律得到结构化 JSON 404 并写入一条 meta 行（`endpoint_path` 为客户端请求的解码路径，无 model/provider，`finish_reason = 1`），SPA 回落只留给未鉴权的安全导航；同时说明 API key 现在由 HTTP 入口解析一次并传入 `gatewayFlow`。**The Codex mount** 段落中"别名从不出现在请求行里"的表述补上例外：子路径归一化失败产生的 404 记录行按客户端实际路径记录。

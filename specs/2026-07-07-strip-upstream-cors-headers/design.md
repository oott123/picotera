# 设计

## 根因

网关面向浏览器的路由（catch-all 网关 + `/api/unified/*`）在匹配到端点后由 `writeCORSHeaders`（`pkg/server/cors.go`）用 `Header.Set` 写入 CORS 头，其中 `Access-Control-Allow-Origin` 与 `Access-Control-Expose-Headers` 均为 `*`（单值，`["*"]`）。

随后在把上游响应头复制给下游 writer 时，两处复制点都用 `Header.Add` 逐值追加：

- `copyPathSuccessHeaders`（`pkg/server/gateway_flow_success.go:134`，catch-all 网关成功路径）。
- `unifiedStreamSuccess` 内的上游头复制循环（`pkg/server/gateway_unified_helpers.go:387`，统一路由成功路径）。

当上游本身回带了 `Access-Control-Allow-Origin: *`（或 `Access-Control-Expose-Headers: …`）时，`Add` 会把上游值追加到网关已 `Set` 的 `*` 之后，得到 `["*", "*"]`，HTTP 头序列化即 `*, *`。这就是用户观察到的现象。

model-list 端点（`handleModelList`）自行生成 JSON 响应、不复制上游头，不受影响。错误路径用 `writeGatewayError` 只 `Set` `Content-Type`，亦不受影响。

## 决策

网关是客户端侧的权威：浏览器/客户端只与网关通信，上游面向自身基础设施、自身 CORS 策略或自身缓存语义的头对客户端无意义。`writeCORSHeaders` 已确立固定且无凭据的宽松 CORS 策略（`Access-Control-Allow-Origin: *`、`Access-Control-Expose-Headers: *` 等）。因此以下上游头都不应透传到下游：

1. **`Access-Control-*` 家族**（前缀匹配，大小写不敏感）：`Allow-Methods`、`Allow-Headers`、`Max-Age`、`Allow-Credentials` 同样由网关策略决定，透传上游值要么重复、要么与无凭据策略冲突（如上游 `Allow-Credentials: true`）。
2. **`Alt-Svc`**：指向上游的替代端点（如 `h2="api.upstream.com:443"`），客户端无法直接访问上游、也不持有上游凭据，透传会误导客户端绕过网关直连上游。网关自身不声明 `Alt-Svc`，直接丢弃上游值即可。
3. **`Nel` 与 `Report-To`**：上游配置的错误上报/报告收集端点，客户端无法也不应向其上报，丢弃即可。
4. **`Vary`**：上游对缓存语义的描述，但网关会重写响应体与响应头（格式桥接、web-search 改写、写入自有 CORS 头、条件性剥离 `Content-Encoding` 等），上游的 `Vary` 对下游不可靠。网关自身不声明 `Vary`（非缓存代理），直接丢弃上游值。

在两处上游头复制点统一跳过这些头。这样：

- 上游回带 `Access-Control-Allow-Origin: *` / `Access-Control-Expose-Headers: …` 时不再被 `Add`，网关自己的 `*` 保持单值。
- 上游回带 `Alt-Svc: …` / `Nel: …` / `Report-To: …` 时被丢弃，不误导客户端。
- 上游回带 `Vary: …` 时被丢弃，避免下游依据不可靠的缓存语义。
- 上游若未回带这些头，行为不变（无值可跳过）。
- 跳过动作只作用于下游 client writer（`w.Header()`），不影响上游 artifact（`resp.Header.Clone()`）与 meta artifact（跳过后的 `w.Header().Clone()` 反而正确反映客户端实际收到的头）。

## 落点

在新建 `pkg/server/header_strip.go` 中放置判定函数，集中定义"哪些上游头不应到达客户端"。该关注点（上游响应头剥离策略）与 `cors.go`（网关出站 CORS 策略）不同，故独立成文件：

```go
// shouldStripUpstreamHeader reports whether lower (the lowercased header
// name) is an upstream response header that must never be forwarded to the
// client. The gateway is the client-facing authority, so headers that serve
// the upstream's own infrastructure or policy are dropped:
//   - Access-Control-*: the gateway owns the downstream CORS policy
//     (writeCORSHeaders); an upstream "Access-Control-Allow-Origin: *" would
//     be appended to the gateway's own "*" and serialize as "*, *" (and
//     similarly for Access-Control-Expose-Headers).
//   - Alt-Svc: points at the upstream's own alternative endpoints, which the
//     client cannot reach (no upstream credentials) and must not bypass the
//     gateway to reach.
//   - Nel / Report-To: the upstream's error/reporting collection endpoints,
//     which the client cannot and should not report to.
//   - Vary: the upstream's caching hint, which is unreliable after the
//     gateway rewrites the body/headers (bridging, web-search emulation,
//     CORS injection, conditional Content-Encoding stripping). The gateway
//     is not a caching proxy and does not emit its own Vary.
func shouldStripUpstreamHeader(lower string) bool {
	switch lower {
	case "alt-svc", "nel", "report-to", "vary":
		return true
	}
	return strings.HasPrefix(lower, "access-control-")
}
```

两处复制点在已有的 `content-length` 跳过旁，增加 `shouldStripUpstreamHeader(lower)` 跳过：

- `copyPathSuccessHeaders`：引入 `lower := strings.ToLower(key)`，跳过 `content-length` 或 `shouldStripUpstreamHeader(lower)`。
- `unifiedStreamSuccess` 复制循环：已有 `lower`，将首个 `if lower == "content-length"` 扩展为 `lower == "content-length" || shouldStripUpstreamHeader(lower)`。

无新 API、无环境变量、无配置项、无兼容层。

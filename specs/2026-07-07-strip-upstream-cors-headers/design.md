# 设计

## 根因

网关面向浏览器的路由（catch-all 网关 + `/api/unified/*`）在匹配到端点后由 `writeCORSHeaders`（`pkg/server/cors.go`）用 `Header.Set` 写入 CORS 头，其中 `Access-Control-Allow-Origin` 与 `Access-Control-Expose-Headers` 均为 `*`（单值，`["*"]`）。

随后在把上游响应头复制给下游 writer 时，两处复制点都用 `Header.Add` 逐值追加：

- `copyPathSuccessHeaders`（`pkg/server/gateway_flow_success.go:134`，catch-all 网关成功路径）。
- `unifiedStreamSuccess` 内的上游头复制循环（`pkg/server/gateway_unified_helpers.go:387`，统一路由成功路径）。

当上游本身回带了 `Access-Control-Allow-Origin: *`（或 `Access-Control-Expose-Headers: …`）时，`Add` 会把上游值追加到网关已 `Set` 的 `*` 之后，得到 `["*", "*"]`，HTTP 头序列化即 `*, *`。这就是用户观察到的现象。

model-list 端点（`handleModelList`）自行生成 JSON 响应、不复制上游头，不受影响。错误路径用 `writeGatewayError` 只 `Set` `Content-Type`，亦不受影响。

## 决策

网关是浏览器侧的 CORS 权威：浏览器只与网关通信，上游的 CORS 头对客户端无意义。`writeCORSHeaders` 已确立固定且无凭据的宽松策略（`Access-Control-Allow-Origin: *`、`Access-Control-Expose-Headers: *` 等），上游的任何 `Access-Control-*` 头都不应透传到下游。

因此在两处上游头复制点统一跳过所有 `Access-Control-*` 头（前缀匹配，大小写不敏感）。这样：

- 上游回带 `Access-Control-Allow-Origin: *` / `Access-Control-Expose-Headers: …` 时不再被 `Add`，网关自己的 `*` 保持单值。
- 上游若未回带 CORS 头，行为不变（无值可跳过）。
- 跳过动作只作用于下游 client writer（`w.Header()`），不影响上游 artifact（`resp.Header.Clone()`）与 meta artifact（跳过后的 `w.Header().Clone()` 反而正确反映客户端实际收到的头）。

跳过整个 `Access-Control-*` 家族（而非仅用户提到的两个头）是正确的：`Allow-Methods`、`Allow-Headers`、`Max-Age`、`Allow-Credentials` 同样由网关策略决定，透传上游值要么重复、要么与无凭据策略冲突（如上游 `Allow-Credentials: true`）。

## 落点

在 `pkg/server/cors.go` 新增一个聚焦的判定函数：

```go
// isUpstreamCORSHeader reports whether lower (the lowercased header name)
// is an Access-Control-* response header. The gateway owns the downstream
// CORS policy (writeCORSHeaders), so upstream CORS headers must never be
// forwarded — otherwise an upstream "Access-Control-Allow-Origin: *" is
// appended to the gateway's own "*" and serializes as "*, *" (and similarly
// for Access-Control-Expose-Headers).
func isUpstreamCORSHeader(lower string) bool {
	return strings.HasPrefix(lower, "access-control-")
}
```

两处复制点在已有的 `content-length` 跳过旁，增加 `isUpstreamCORSHeader(lower)` 跳过：

- `copyPathSuccessHeaders`：引入 `lower := strings.ToLower(key)`，跳过 `content-length` 或 `isUpstreamCORSHeader(lower)`。
- `unifiedStreamSuccess` 复制循环：已有 `lower`，将首个 `if lower == "content-length"` 扩展为 `lower == "content-length" || isUpstreamCORSHeader(lower)`。

无新 API、无环境变量、无配置项、无兼容层。

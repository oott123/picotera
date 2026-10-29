# 执行计划

## 1. 新建 `pkg/server/cors.go`

- `writeCORSHeaders(w http.ResponseWriter, r *http.Request)`：写入
  `Access-Control-Allow-Origin: *`、`Access-Control-Allow-Methods: GET, POST, OPTIONS`、
  `Access-Control-Expose-Headers: *`、`Access-Control-Max-Age: 86400`；
  `Access-Control-Allow-Headers` 取 `Access-Control-Request-Headers` 回显，缺省 `*`。
- `corsMiddleware(next http.Handler) http.Handler`：写头；`OPTIONS` → `204` 短路；否则 `next`。

## 2. catch-all 网关 `pkg/server/handle_gateway.go`

在 `gatewayHandler.ServeHTTP` 中，`resolveEndpoint` 成功（含 model-list 判断之前）后：
- 调用 `writeCORSHeaders(w, r)`；
- 若 `r.Method == http.MethodOptions` 写 `http.StatusNoContent` 并 `return`。

## 3. 统一路由 `pkg/server/server.go`

将 `registerEndpoints` 中五条 `s.router.Post("/api/unified/...", ...)` 改为放入
`s.router.Group(func(r chi.Router) { r.Use(corsMiddleware); ... })`，每条同时注册
`r.Post(pattern, h)` 与 `r.Options(pattern, h)`（`h` 为 `s.handleUnifiedGenerate(format)`）。

## 4. 测试页 `dashboard/src/api/client.ts`

`postGatewayTest` 的 `fetch(...)` 选项增加 `credentials: 'omit'`。

## 5. 验证

- `go build -o /tmp/picotera ./cmd/picotera` 通过。
- `pnpm --dir dashboard type-check` 通过。

无需改动 `openapi.yaml`（未新增/修改 Huma 操作）。

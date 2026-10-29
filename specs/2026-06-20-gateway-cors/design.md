# 设计

## CORS 策略

网关面向浏览器内的 LLM 客户端，采用宽松且无凭据的 CORS 策略：

- `Access-Control-Allow-Origin: *`（不回显 Origin，不发 `Access-Control-Allow-Credentials`，与“浏览器不携带 cookies”一致）。
- `Access-Control-Allow-Methods: GET, POST, OPTIONS`（网关端点为 POST，model-list 为 GET）。
- `Access-Control-Allow-Headers`：若预检请求带 `Access-Control-Request-Headers` 则原样回显（这样才能放行 `Authorization` 等规范不允许被 `*` 覆盖的请求头），否则为 `*`。
- `Access-Control-Expose-Headers: *`。
- `Access-Control-Max-Age: 86400`。

无环境变量配置项——策略固定为允许所有来源。

## 落点

新建 `pkg/server/cors.go`，提供两件东西：

- `writeCORSHeaders(w, r)`：按上述策略写入响应头。
- `corsMiddleware(next)`：chi 中间件，先写 CORS 头；遇到 `OPTIONS` 直接返回 `204` 应答预检，否则进入 `next`。

### catch-all 网关

在 `gatewayHandler.ServeHTTP` 中，**仅在成功匹配到 `endpoint` 之后**调用 `writeCORSHeaders`，随后若方法为 `OPTIONS` 则返回 `204`。这样静态 SPA 兜底分支不会带上 CORS 头，未匹配路径的预检按既有逻辑得到 404。头在 `flow.run()` / `handleModelList` 写响应体之前设置，流式响应也能带上。

### 统一路由

在 `registerEndpoints` 中把五条 `/api/unified/*` 路由收进一个 `s.router.Group`，组内 `r.Use(corsMiddleware)`，每条路由同时注册 `POST` 与 `OPTIONS`（指向同一 handler）。`OPTIONS` 由中间件短路应答，`POST` 经中间件带上 CORS 头后进入原 handler。

## 测试页去 cookie

`dashboard/src/api/client.ts` 的 `postGatewayTest` 的 `fetch` 增加 `credentials: 'omit'`。`postTestDirect` 不变。

# 执行计划

## 1. `pkg/server/cors.go` — 新增判定函数

新增 `isUpstreamCORSHeader(lower string) bool`：`strings.HasPrefix(lower, "access-control-")`。带文档注释说明“网关拥有下游 CORS 策略，上游 `Access-Control-*` 头不透传，否则与网关 `*` 拼成 `*, *`”。`cors.go` 需新增 `import "strings"`。

## 2. `pkg/server/gateway_flow_success.go` — `copyPathSuccessHeaders`

将循环改为：

```go
func copyPathSuccessHeaders(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		lower := strings.ToLower(key)
		if lower == "content-length" || isUpstreamCORSHeader(lower) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}
```

确认该文件已 import `"strings"`（原有 `strings.ToLower` 调用已存在，无需新增 import）。

## 3. `pkg/server/gateway_unified_helpers.go` — `unifiedStreamSuccess` 复制循环

将首个跳过条件由 `if lower == "content-length"` 改为 `if lower == "content-length" || isUpstreamCORSHeader(lower)`，其余 `transforming`/`bridging` 条件不变。

## 4. 测试 `pkg/server/gateway_flow_success_test.go`（或同包新测试）

- `isUpstreamCORSHeader` 表驱动用例：`access-control-allow-origin`、`Access-Control-Expose-Headers`、`ACCESS-CONTROL-MAX-AGE`、`access-control-allow-credentials` → true；`content-type`、`x-request-id`、`authorization` → false；`access-control`（边界，缺尾部 `-`）→ false（`HasPrefix("access-control-", …)` 要求尾部连字符，故不匹配）。
- `copyPathSuccessHeaders` 回归用例：构造一个 `*http.Response`，其 Header 含 `Access-Control-Allow-Origin: *`、`Access-Control-Expose-Headers: X-Foo`、`X-Trace: abc`、`Content-Length: 123`；先用 `writeCORSHeaders` 向 `httptest.ResponseRecorder` 写头（模拟网关前置写头），再调用 `copyPathSuccessHeaders`；断言：
  - `Access-Control-Allow-Origin` == `*`（单值，非 `*, *`）。
  - `Access-Control-Expose-Headers` == `*`（单值，非 `*, X-Foo`）。
  - `X-Trace` == `abc`（普通头仍透传）。
  - `Content-Length` 未被写入（既有行为不变）。

统一路由的复制循环与路径路由共用同一 `isUpstreamCORSHeader` 判定，策略由上述表驱动用例覆盖；其嵌入在 `unifiedStreamSuccess` 大函数内，不单独建集成测试。

## 5. 验证

- `go build -o /tmp/picotera ./cmd/picotera` 通过。
- `go test ./pkg/server/` 通过（含新增用例）。

无需改动 `openapi.yaml`（未新增/修改 Huma 操作）。

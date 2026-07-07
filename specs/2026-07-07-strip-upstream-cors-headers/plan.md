# 执行计划

## 1. 新建 `pkg/server/header_strip.go` — 判定函数

判定函数 `shouldStripUpstreamHeader(lower string) bool` 已不属于 CORS 关注点，从 `cors.go` 移入新文件 `pkg/server/header_strip.go`（与 `cors.go` 的出站 CORS 策略分属不同关注点）。函数体：前缀族 `Access-Control-*` 用 `strings.HasPrefix(lower, "access-control-")` 匹配，四个精确名用 `switch lower { case "alt-svc", "nel", "report-to", "vary": return true }` 命中。文档注释列出每类头不透传的缘由：`Access-Control-*` 由网关策略决定（上游与网关 `*` 拼成 `*, *` 或与无凭据策略冲突）；`Alt-Svc` 指向上游替代端点，客户端无凭据直连、不得绕过网关；`Nel`/`Report-To` 指向上游报告收集端点，客户端不应上报；`Vary` 是上游缓存语义，网关重写响应体/头后对下游不可靠，且网关非缓存代理、不声明自有 `Vary`。新文件需 `import "strings"`；`cors.go` 若随之不再使用 `strings` 则移除该 import。

## 2. `pkg/server/gateway_flow_success.go` — `copyPathSuccessHeaders`

将跳过条件由 `lower == "content-length" || isUpstreamCORSHeader(lower)` 改为 `lower == "content-length" || shouldStripUpstreamHeader(lower)`：

```go
func copyPathSuccessHeaders(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		lower := strings.ToLower(key)
		if lower == "content-length" || shouldStripUpstreamHeader(lower) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}
```

## 3. `pkg/server/gateway_unified_helpers.go` — `unifiedStreamSuccess` 复制循环

将首个跳过条件由 `lower == "content-length" || isUpstreamCORSHeader(lower)` 改为 `lower == "content-length" || shouldStripUpstreamHeader(lower)`，其余 `transforming`/`bridging` 条件不变。

## 4. 测试 `pkg/server/header_strip_test.go`

将 `shouldStripUpstreamHeader` 与 `lowerHeader` 及 `TestCopyPathSuccessHeaders_StripsUpstreamHeaders` 迁入新建的 `pkg/server/header_strip_test.go`（与被测函数同文件、同关注点），`cors_test.go` 仅留 CORS 相关测试。

- `shouldStripUpstreamHeader` 表驱动用例（`TestShouldStripUpstreamHeader`）：`access-control-allow-origin`、`Access-Control-Expose-Headers`、`ACCESS-CONTROL-MAX-AGE`、`access-control-allow-credentials`、`Alt-Svc`、`ALT-SVC`、`Nel`、`NEL`、`Report-To`、`REPORT-TO`、`Vary`、`VARY` → true；`content-type`、`x-request-id`、`authorization`、`alt-svc-x`（前缀不匹配，非 `alt-svc`）、`vary-x`（非 `vary`）→ false；`access-control`（边界，缺尾部 `-`）→ false。
- `copyPathSuccessHeaders` 回归用例（`TestCopyPathSuccessHeaders_StripsUpstreamHeaders`）：上游 Header 含 `Alt-Svc`、`Nel`、`Report-To`、`Vary`；断言这四个头均不出现在下游；`Access-Control-Allow-Origin`/`Access-Control-Expose-Headers` 仍单值 `*`；`X-Trace` 仍透传；`Content-Length` 仍跳过。

统一路由的复制循环与路径路由共用同一 `shouldStripUpstreamHeader` 判定，策略由上述表驱动用例覆盖；其嵌入在 `unifiedStreamSuccess` 大函数内，不单独建集成测试。

## 5. 验证

- `go build -o /tmp/picotera ./cmd/picotera` 通过。
- `go test ./pkg/server/` 通过（含新增用例）。

无需改动 `openapi.yaml`（未新增/修改 Huma 操作）。

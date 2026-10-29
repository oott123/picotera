# 执行计划：响应头 Set-Cookie 脱敏

## 1. 新增脱敏函数

- [ ] 在 `pkg/server/gateway_helpers.go` 的 `redactUpstreamCredentials` 之后新增 `redactResponseHeaders(header http.Header) http.Header` 与 `redactSetCookieValue(v string) string`，复用现有 `redactedPlaceholder` 常量。

## 2. 接入 response artifact 上传

- [ ] 在 `pkg/server/gateway_flow_success.go` 的 4 个 helper 中，`Enabled()` 早返之后、`artifacts.BuildResponse*` 之前，插入 `header = redactResponseHeaders(header)`：
  - `uploadResponseArtifact`（`BuildResponse` 之前）
  - `uploadResponseArtifactWithAggregation`（`BuildResponseWithAggregated` 之前）
  - `uploadMetaResponseArtifact`（`BuildResponseWithLogs` 之前）
  - `uploadMetaResponseArtifactWithAggregation`（`BuildResponseWithLogsAndAggregated` 之前）

## 3. 测试

- [ ] 在 `pkg/server/gateway_helpers_test.go` 新增 `TestRedactResponseHeaders`，覆盖：
  - 单 cookie value 脱敏、属性保留（`session=abc123; Path=/; HttpOnly` → `session=[REDACTED]; Path=/; HttpOnly`）
  - 多 Set-Cookie 头各自独立脱敏
  - 无属性 cookie（`token=xyz` → `token=[REDACTED]`）
  - 空 value（`session=; Path=/` → `session=[REDACTED]; Path=/`）
  - value 含 `=`（base64，`s=a=b=c; Path=/` → `s=[REDACTED]; Path=/`）
  - 引号 value 含 `;`（`foo="a;b"; Path=/` → `foo=[REDACTED]; Path=/`）
  - 无 `=` 畸形头（`justflags` → `[REDACTED]`）
  - 无 Set-Cookie 时头不变
  - 非 cookie 头不受影响

## 4. 验证

- [ ] `go test ./pkg/server/` 通过（含新测试）。
- [ ] `go build ./...` 通过。

# 执行计划

## 1. 重命名脱敏函数

`pkg/server/gateway_helpers.go`

- 将 `redactUpstreamCredentials` 重命名为 `redactRequestCredentials`。
- 更新其文档注释：说明它用于对请求 artifact（meta 与 upstream 均适用）的凭证脱敏，不再限定 “upstream”。

## 2. 更新调用点

- `pkg/server/gateway_flow_attempts.go:167`：`redactUpstreamCredentials(...)` → `redactRequestCredentials(...)`（逻辑不变）。
- `pkg/server/gateway_flow.go:324`：meta 请求上传点改为先脱敏再上传：

  ```go
  redactedHeader, redactedURL := redactRequestCredentials(f.meta.RequestHeader.Clone(), f.meta.RequestURL)
  f.h.uploadRequestArtifact(pctx, f.meta.ID, f.meta.CreatedAt, f.meta.RequestMethod, redactedURL, redactedHeader, f.artifactBody(f.body))
  ```

## 3. 更新测试

`pkg/server/gateway_helpers_test.go`

- 将测试中对 `redactUpstreamCredentials` 的调用改为 `redactRequestCredentials`。
- 现有用例（Authorization scheme 保留 / 整体替换、X-Api-Key、X-Goog-Api-Key、`?key=`、Cf-Access-*）即覆盖该函数，改名后继续生效，无需新增函数级用例。

## 4. 更新文档

`CLAUDE.md`「User isolation & authorization」下的「Upstream credential hygiene」段落：

- 将 `redactUpstreamCredentials` 改为 `redactRequestCredentials`。
- 修正 “so meta artifacts are untouched” 的表述：meta 与 upstream 请求 artifact 均对凭证头 / `?key=` 脱敏；仅响应侧 cookie 脱敏走 `redactResponseHeaders`。

## 5. 验证

- `go build ./...` 编译通过。
- `go test ./pkg/server/ -run 'Redact|Credential'` 通过。
- 全量 `go test ./pkg/server/` 通过。
- 手动确认：dashboard 请求详情中 meta 请求头的 `Authorization` / `X-Api-Key` 等显示为 `[REDACTED]`（无 DB/artifact 测试 harness，此项为人工验证）。

# 执行计划

## 改动 1：上游请求移除本地 auth header

1. `pkg/server/gateway_helpers.go` — 修改 `buildUpstreamRequest` 签名，新增末位参数 `authHeaderName string`。在 header 复制循环中，把 `authHeaderName` 小写化（非空时）加入跳过判断：
   ```go
   if lower == "authorization" || lower == "x-api-key" || lower == "x-goog-api-key" ||
       lower == "host" || lower == "content-length" ||
       (authHeaderName != "" && lower == authHeaderName) {
       continue
   }
   ```
   在循环前计算一次 `authHeaderName = strings.ToLower(authHeaderName)`。

2. `pkg/server/gateway_flow_attempts.go:259` — 调用 `buildUpstreamRequest` 处补传 auth header 名：
   ```go
   authHeaderName := ""
   if f.h.config.Auth.HeaderEnabled {
       authHeaderName = f.h.config.Auth.HeaderName
   }
   ```
   作为新参数传入。

## 改动 2：上游 artifact 凭证脱敏

3. `pkg/server/gateway_helpers.go` — 新增 `redactedPlaceholder` 常量与 `redactUpstreamCredentials(header http.Header, rawURL string) (http.Header, string)` 函数（含 `Authorization` 保留方案前缀、`X-Api-Key`/`X-Goog-Api-Key` 整体替换、URL `key` 查询参数替换的逻辑，见 design.md）。

4. `pkg/server/gateway_flow_attempts.go:150` — 在调用 `uploadRequestArtifact` 前，先用 `redactUpstreamCredentials` 处理 `prepared.Request.Header.Clone()` 与 `prepared.Request.URL.String()`，将脱敏后的 header 与 URL 传入。

## 验证

5. `go build ./...` 确认编译通过。
6. `go test ./pkg/server/...` 确认现有测试不回归（`gateway_flow_attempts_test.go` 覆盖 `buildRewrittenUpstreamRequest`，注意其对 `buildUpstreamRequest` 新签名的影响——若测试直接调用需同步更新）。
7. 为两处行为补单元测试：
   - `buildUpstreamRequest` 在传入 auth header 名时跳过该 header，传空串时不跳过。
   - `redactUpstreamCredentials`：`Authorization: Bearer xxx` → `Bearer [REDACTED]`；无空格的 Authorization → `[REDACTED]`；`X-Api-Key`/`X-Goog-Api-Key` → `[REDACTED]`；URL `?key=secret` → `?key=[REDACTED]`；无凭证时原样返回；确认未改动传入对象之外的实际请求。

## 不涉及

- 无数据库 / migration / sqlc 改动。
- 无 API contract 改动，无需重新生成 `openapi.yaml` 或 dashboard 类型。
- meta artifact、`handle_provider_endpoint.go` 的 fetch-models 请求（不保存 artifact）均不改动。

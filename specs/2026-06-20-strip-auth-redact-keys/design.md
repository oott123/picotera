# 设计

两处独立改动，均在 `pkg/server`，不涉及数据库、API contract、dashboard，无新增依赖。

## 1. 上游请求移除本地 auth header

上游 HTTP 请求由 `buildUpstreamRequest`（`pkg/server/gateway_helpers.go:538`）统一构建——路径网关与 unified 网关都经此函数。其 header 复制循环已通过小写黑名单跳过 `authorization` / `x-api-key` / `x-goog-api-key` / `host` / `content-length`。

本地鉴权 header 名由 `config.Auth.HeaderName` 配置（仅当 `config.Auth.HeaderEnabled` 时生效）。它只用于 `/api/picotera` 管理 API，但客户端或反向代理可能在打到网关的请求上携带它，从而被原样转发到上游。

**方案**：给 `buildUpstreamRequest` 新增参数 `authHeaderName string`，在 header 复制循环中按小写比较额外跳过该 header。唯一调用方 `buildRewrittenUpstreamRequest`（`gateway_flow_attempts.go:259`）通过 `f.h.config.Auth` 取值：`HeaderEnabled` 为真时传 `HeaderName`，否则传空串（空串不跳过任何额外 header）。

匹配按小写全等。`http.Header` 的 canonical 化与此处显式小写比较一致，不受大小写影响。

## 2. 上游 artifact 凭证脱敏

带上游凭证的请求 artifact 仅在一处保存：`gateway_flow_attempts.go:150` 调用 `uploadRequestArtifact`，传入 `prepared.Request.Header.Clone()` 与 `prepared.Request.URL.String()`。凭证由 `applyCredentials`（`gateway_helpers.go:275`）注入，落点固定为 `Authorization` / `X-Api-Key` / `X-Goog-Api-Key` 三个 header，或 `SearchKey` resolver 的 `?key=` 查询参数。

meta artifact 走同一个 `uploadRequestArtifact`，但脱敏只针对上游 artifact，因此**不在** `uploadRequestArtifact` 或 `artifacts` 包内做脱敏（那会同时命中 meta），而是在上游调用点先脱敏再传入。

**方案**：在 `gateway_helpers.go` 新增 server 层辅助函数

```go
const redactedPlaceholder = "[REDACTED]"

// redactUpstreamCredentials 就地脱敏已克隆的 header 中的凭证，并返回脱敏后的 URL。
func redactUpstreamCredentials(header http.Header, rawURL string) (http.Header, string)
```

行为：
- `Authorization`：若值含空格，保留首段方案前缀 → `<scheme> [REDACTED]`；否则整体 → `[REDACTED]`。
- `X-Api-Key`、`X-Goog-Api-Key`：存在则整体 → `[REDACTED]`。
- URL：解析后若含 `key` 查询参数，将其值置为 `[REDACTED]` 再重新编码；无该参数则原样返回。

仅对实际存在的 header / 参数脱敏。传入的 header 已是 clone，函数就地修改安全。

在 `gateway_flow_attempts.go:150` 调用点先经此函数处理，再传给 `uploadRequestArtifact`：

```go
redactedHeader, redactedURL := redactUpstreamCredentials(prepared.Request.Header.Clone(), prepared.Request.URL.String())
f.h.uploadRequestArtifact(reqArtifactCtx, input.UpstreamID, input.UpstreamCreatedAt,
    prepared.Request.Method, redactedURL, redactedHeader, prepared.RequestBody)
```

脱敏仅作用于 artifact 副本，不影响实际发往上游的 `prepared.Request`。

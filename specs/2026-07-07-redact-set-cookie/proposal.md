# 响应头 Set-Cookie 脱敏

## 需求

在对响应头脱敏时（已有逻辑），增加对 `Set-Cookie` 的脱敏：把所有的 cookie value 都改成 `[REDACTED]`。

## 说明

- 仅替换 cookie 的 **value**，保留 cookie 名和所有属性（`Path`、`Domain`、`Expires`、`Max-Age`、`HttpOnly`、`Secure`、`SameSite` 等）。
  - 例：`session=abc123; Path=/; HttpOnly` → `session=[REDACTED]; Path=/; HttpOnly`
- 与现有 `Authorization` 保留 scheme 前缀（`Bearer [REDACTED]`）的脱敏粒度一致。
- 仅对**存档副本（artifact）**脱敏，不影响实际下发给客户端的响应头（与现有 `redactUpstreamCredentials` 仅对 artifact 副本脱敏、实际上游请求保留真实凭据的做法一致）。

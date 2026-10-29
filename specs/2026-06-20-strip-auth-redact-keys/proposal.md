# 移除上游 auth header 与脱敏 artifact 凭证

## 需求

1. **向上游代理请求时，移除配置的 auth header。** 该请求头（`PICOTERA_AUTH_HEADER_NAME`）仅用于本地管理 API 鉴权，不应转发给上游 provider。

2. **保存请求 artifacts 时，脱敏发送的 api key（用 `[REDACTED]` 替代）。**

## 确认细节

- 脱敏范围：**仅上游 artifact**（picotera → provider，含 provider 凭证）。meta artifact（客户端 → picotera）保持客户端发来的 api key 明文可见，不改动。
- 脱敏形式：**保留方案前缀**。`Authorization: Bearer <secret>` → `Authorization: Bearer [REDACTED]`；无方案前缀的凭证头（`X-Api-Key`、`X-Goog-Api-Key`）及 URL 的 `?key=` 参数整体替换为 `[REDACTED]`。

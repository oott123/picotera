# API 变更

只在 provider 契约上新增一个字段，无新增操作、无路径变化。

## 字段

`insecureTls: boolean`（Go：`InsecureTls bool \`json:"insecureTls"\``，无 `omitempty`）

- `true`：该渠道的所有出站 HTTPS 请求（网关转发、拉取模型列表、直连测试）跳过服务端证书校验。
- `false`（默认）：正常校验。

## 涉及的类型（`pkg/contract/provider.go`）

| 类型 | 变更 |
| --- | --- |
| `ProviderView` | 新增 `insecureTls` |
| `CreateProviderRequest.Body` | 新增 `insecureTls` |
| `UpsertProviderRequest.Body` | 新增 `insecureTls` |

对应影响的接口（均为 admin 组）：

- `GET /api/picotera/providers`
- `GET /api/picotera/providers/{id}`
- `POST /api/picotera/providers`
- `PUT /api/picotera/providers`（upsert）
- `PUT /api/picotera/providers/{id}/models`（响应体是 `ProviderView`）

`GET /api/picotera/labels/providers` 只投影 id/name，不含该字段。

## 语义

- 请求体省略 `insecureTls` 等价于 `false`：upsert 是整体覆盖语义（与 `disabled`、`supportsNativeWebSearch` 一致），不做部分更新。
- 无额外校验：布尔字段，非布尔值由 Huma 的 JSON 解码直接拒绝。
- 该字段随渠道立即生效，无需重启；`GatewayEphemeralTransport` 默认开启时每次尝试新建传输，关闭时缓存按 `(proxy, insecureTls, streaming)` 分桶，改动后旧连接不会被复用于新策略。

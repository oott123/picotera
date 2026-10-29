# API 契约：扮演 Header

本特性不新增任何 REST 操作，仅为既有 `/api/picotera/*` 管理接口引入一个请求 Header。

## 请求 Header

| 名称 | 取值 | 说明 |
| --- | --- | --- |
| `X-PicoTera-Impersonation-User-Id` | 目标用户的数字 ID（十进制字符串） | 可选。仅对 `/api/picotera/*` 管理接口生效；网关与 `/api/unified` 不读取。 |

## 生效条件与处理

| 情形 | 结果 |
| --- | --- |
| Header 缺失 / 空字符串 | 按真实身份处理（不扮演） |
| 真实用户非管理员且 Header 非空 | `403 {"message":"forbidden"}` |
| Header 非数字 | `400 {"message":"invalid impersonation user id"}` |
| 目标用户不存在 | `404 {"message":"impersonation target not found"}` |
| 真实用户为管理员且目标存在（含已禁用） | 本次请求识别为目标用户，下游所有按 `user_id` 隔离的查询、`requireAdmin` 闸门、`GET /api/picotera/me` 均以目标用户为准 |

真实身份始终由既有鉴权来源（single-user-mode / http-header）可信解析；扮演 Header 仅是附加提示，绝不改变“真实用户是否为管理员”的判定。

## 受影响的端点

- 所有 `/api/picotera/*` 管理接口（经 `auth.Middleware`）。
- **不受影响**：网关 catch-all、`POST /api/unified/*`、静态资源——它们不经过 `auth.Middleware`，按 API key 鉴权。
- 前端“测试”请求（`POST /api/picotera/test/direct`、网关测试）使用原生 `fetch`，不携带此 Header。

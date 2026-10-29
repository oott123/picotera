# API：OIDC 认证

三条认证路由是裸 chi 路由（同 `POST /api/picotera/test/direct`），不是 Huma operation，因此不出现在 `openapi.yaml` 中。它们注册在 `s.router` 上而非 `s.mgmtRouter`，不经过 `auth.Middleware`；仅在 `auth.oidc.enabled` 为真时注册，其余模式下这些路径落到网关兜底。

## `GET /api/picotera/auth/login`

发起授权码流程。

**Query**

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `redirect_to` | 否 | 登录完成后跳转的站内路径。必须以单个 `/` 开头，不得以 `//` 或 `/\` 开头，不得含 CR/LF。缺省 `/`。 |

**响应**

- `302` → authorization endpoint，`Set-Cookie: __Host-Http-Picotera-Auth=<加密 state>`。
  查询参数：`response_type=code`、`client_id`、`redirect_uri=<base_url>/api/picotera/auth/callback`、`scope`、`state`，PKCE 启用时附 `code_challenge` + `code_challenge_method=S256`。
- `400` `{"message":"invalid redirect_to"}` — `redirect_to` 未通过校验。
- `502` `{"message":"oidc discovery failed"}` — 需要自动发现但发现文档拉取或校验失败。

## `GET /api/picotera/auth/callback`

IdP 回调。

**Query**：`code`、`state`；IdP 报错时为 `error` + 可选 `error_description`。

**响应**

- `302` → state 中记录的 `redirect_to`，`Set-Cookie: __Host-Http-Picotera-Auth=<加密 session>`。
- `400` HTML 错误页（附“重新登录”链接）：state cookie 缺失 / 解密失败 / 类型不是 state / `state` 不匹配 / 缺少 `code`。
- `401` HTML 错误页：身份未知且 `auth.auto_create_user` 未开启，或用户已被禁用。
- `502` HTML 错误页：token endpoint 或 userinfo endpoint 返回非 2xx、响应不可解析、`sub` 缺失或为空。

IdP 回传 `error` 时，错误页展示 `error` 与 `error_description`，HTTP 状态 `400`。

## `POST /api/picotera/auth/logout`

删除 `user_session` 行并清除会话 cookie。无请求体。

**响应**：`204`，`Set-Cookie: __Host-Http-Picotera-Auth=; Max-Age=0; Path=/; HttpOnly; Secure; SameSite=Lax`。cookie 缺失或已失效时同样返回 `204`。

不终止 IdP 侧的 SSO 会话。

## 管理 API 的 401 变化

oidc 模式下 `auth.Middleware` 返回的 401 额外携带响应头：

```
X-PicoTera-Login-Url: /api/picotera/auth/login
```

body 保持 `{"message":"unauthorized"}` 不变。其余模式不带该头。

## `GET /api/picotera/config` 变更

`ConfigView` 新增必填字段：

```jsonc
{
  "title": "PicoTera",
  "authMode": "oidc"   // "single-user-mode" | "http-header" | "oidc"
}
```

这是唯一影响 `openapi.yaml` 的改动，需要重跑 `mise run openapi` 与 `pnpm --dir dashboard generate-openapi`。

## 会话 cookie

| 属性 | 值 |
| --- | --- |
| 名称 | `__Host-Http-Picotera-Auth` |
| Path | `/` |
| Domain | 不设置（`__Host-` 前缀要求） |
| Secure | 始终 |
| HttpOnly | 始终 |
| SameSite | `Lax` |
| Max-Age | state 阶段 600 秒；session 阶段为 `auth.oidc.session_ttl` |

值由 `gorilla/securecookie` 编码：`base64(name|timestamp|base64(AES-CTR 密文)|HMAC-SHA256)`。明文是 gob 编码的 `map[any]any`，取下列两种形态之一：

```jsonc
// 登录中途的 state
{"k": "a", "state": "<base64url state>", "verifier": "<pkce code verifier>", "redirect": "/requests"}
// 已登录会话
{"k": "s", "sid": "<user_session.id>"}
```

cookie 的过期由 securecookie 依据自身的 `timestamp` 与 codec 的 `MaxAge` 判定，不信任浏览器回传的 `Max-Age`。会话本身的过期以数据库 `user_session.expires_at` 为准；每个已认证的管理 API 响应都会重新下发同值的 cookie，使两侧过期时刻对齐。

## 表 `user_session`

```sql
CREATE TABLE user_session (
  id         TEXT PRIMARY KEY,
  user_id    BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX user_session_user_id_idx ON user_session (user_id);
```

`id` 为 32 字节 `crypto/rand` 的 base64url 编码。与 `user_identity` 一致不加外键。行的产生只有登录一处；删除有三处：登出、登录时清理该用户的过期行、删除用户时清理其全部行。

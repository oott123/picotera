# 设计：OIDC 认证模式

## 目标与边界

在 `pkg/auth` 现有的 `single-user-mode` / `http-header` 之外新增第三个身份提供方 `oidc`，走标准的 OAuth 2.0 authorization code 流程，会话记在数据库里、cookie 只携带 session id。

**只影响管理面。** 网关数据面（catch-all 与 `/api/unified`）继续只认 API key，永远不读会话 cookie。本方案对网关的唯一改动是把 PicoTera 自己的 cookie 从上游请求里摘掉、并在 artifact 里脱敏。

**范围补充**：需求未提到登出。一个没有终止方式的会话是不完整的，因此本方案包含一个最小登出端点（清除本地 cookie，不触碰 IdP 会话，见下）。

## 认证模式互斥

`configx.Parse` 统计 `auth.single_user_mode` / `auth.header_enabled` / `auth.oidc.enabled` 中为真的个数：

- 0 个 → 沿用现有报错 `no auth provider enabled`。
- 大于 1 个 → 新报错 `exactly one auth provider must be enabled`，启动失败。

`Resolver.Resolve` 相应变为按模式单分支派发，而不是回退链。这替换掉现有的优先级链语义，CLAUDE.md 中描述优先级的段落一并改写。

## 配置

新增顶层 `base_url` 与 `auth.oidc` 子结构（viper 自动展开为 `PICOTERA_` 前缀环境变量）：

| 环境变量 | 说明 |
| --- | --- |
| `PICOTERA_BASE_URL` | PicoTera 对外 base url。oidc 模式下必填，其余模式忽略。 |
| `PICOTERA_AUTH_OIDC_ENABLED` | 启用 oidc 模式。 |
| `PICOTERA_AUTH_OIDC_ISSUER` | IdP issuer。用于自动发现。 |
| `PICOTERA_AUTH_OIDC_CLIENT_ID` | 必填。 |
| `PICOTERA_AUTH_OIDC_CLIENT_SECRET` | 必填。 |
| `PICOTERA_AUTH_OIDC_SESSION_SECRET` | 必填。会话 cookie 加密密钥的原始素材。 |
| `PICOTERA_AUTH_OIDC_SESSION_TTL` | 会话有效期，默认 `12h`。 |
| `PICOTERA_AUTH_OIDC_SCOPES` | 默认 `openid profile email`。 |
| `PICOTERA_AUTH_OIDC_AUTH_ENDPOINT` | 覆盖自动发现的 authorization endpoint。 |
| `PICOTERA_AUTH_OIDC_TOKEN_ENDPOINT` | 覆盖自动发现的 token endpoint。 |
| `PICOTERA_AUTH_OIDC_USERINFO_ENDPOINT` | 覆盖自动发现的 userinfo endpoint。 |
| `PICOTERA_AUTH_OIDC_PKCE` | `*bool`。覆盖自动发现推断出的 PKCE 开关。 |
| `PICOTERA_AUTH_OIDC_CLIENT_AUTH_METHOD` | `client_secret_basic`（默认）或 `client_secret_post`。 |

用户自动创建复用现有的 `PICOTERA_AUTH_AUTO_CREATE_USER`，新建用户非管理员，与 `http-header` 模式一致。

### 启动期校验（全部 fail fast）

oidc 启用时：

- `client_id` / `client_secret` / `session_secret` 非空。
- `session_ttl > 0`。
- `client_auth_method` 属于枚举二选一。
- `base_url` 能解析为绝对 URL，host 非空，path 为空或 `/`，无 query 与 fragment。归一化为不带尾斜杠的形式；scheme 不校验。
- `issuer`：当 auth / token / userinfo 三个 endpoint **未全部**配置时必填（即需要自动发现时）。必填时须是绝对 URL、无 query / fragment、**不带尾斜杠**（发现文档 URL 由拼接得到，带尾斜杠直接报错而不是静默裁剪）。三个 endpoint 全部配置时 issuer 不参与任何流程，此时留空是合法的。
- `pkce`：三个 endpoint 全部配置（不做自动发现）且 `pkce` 未显式设置时报错——无从推断，必须显式声明。

## 协议层：`coreos/go-oidc` + `x/oauth2`

OIDC 元数据、发现与 userinfo 走 `github.com/coreos/go-oidc/v3/oidc`；授权码流程走 `golang.org/x/oauth2`（当前已是间接依赖，本次提升为直接依赖）。

**Provider 构造分两条路**：

- 三个 endpoint 未全部覆盖 → `oidc.NewProvider(ctx, issuer)`。发现文档的 `issuer` 字段与配置值是否严格相等由库校验，不必自己比对。随后用 `provider.Claims(&doc)` 读原始文档，取 `code_challenge_methods_supported` 推断 PKCE，并用配置中显式给出的 endpoint 逐项覆盖库返回的值。
- 三个 endpoint 全部覆盖 → `oidc.ProviderConfig{IssuerURL, AuthURL, TokenURL, UserInfoURL, JWKSURL}.NewProvider(ctx)`，完全不访问网络。此时无从推断 PKCE，配置层已要求必须显式声明。

构造惰性执行（首个需要它的请求触发），成功后常驻内存缓存，失败不缓存、下次重试。启动阶段不访问 IdP，IdP 不可用不影响进程启动。HTTP 客户端通过 `oidc.ClientContext` 注入，统一 10s 超时。

`provider.Endpoint()` 喂给 `oauth2.Config`；`client_auth_method` 映射为 `oauth2.Endpoint.AuthStyle`（`client_secret_basic` → `AuthStyleInHeader`，`client_secret_post` → `AuthStyleInParams`），不使用库的自动探测，避免一次多余的失败往返。

PKCE 用 `oauth2.GenerateVerifier()` / `oauth2.S256ChallengeOption` / `oauth2.VerifierOption`，不自行生成 challenge。

身份来自 `provider.UserInfo(ctx, oauth2.StaticTokenSource(token))`，取 `UserInfo.Subject`。userinfo 返回 `application/jwt` 时 go-oidc 需要 JWKS：走自动发现时 JWKS URL 由发现文档提供，三个 endpoint 全覆盖时 `JWKSURL` 为空，此种响应会得到明确错误——该模式下 userinfo 必须返回 JSON。

## 会话：数据库表 + 只装 session id 的 cookie

会话是**有状态**的。cookie 里只有一个 session id，真正的会话记录在数据库里，服务端是唯一权威。

### 表 `user_session`（migration 051）

| 列 | 类型 | 说明 |
| --- | --- | --- |
| `id` | `TEXT PRIMARY KEY` | session id，32 字节 `crypto/rand` 的 base64url 编码。 |
| `user_id` | `BIGINT NOT NULL` | 与 `user_identity` 一致，不加外键。 |
| `created_at` | `TIMESTAMPTZ NOT NULL DEFAULT now()` | 登录时间。 |
| `expires_at` | `TIMESTAMPTZ NOT NULL` | 最后一次请求时间 + `session_ttl`。 |

索引 `user_session_user_id_idx ON (user_id)`，服务于按用户的过期清理与删除用户时的清理。

**读与滑动续期是同一条语句**，避免多一次往返、也避免读到刚过期的行：

```sql
WITH touched AS (
  UPDATE user_session SET expires_at = $2
  WHERE id = $1 AND expires_at > $3
  RETURNING user_id
)
SELECT app_user.* FROM app_user JOIN touched ON app_user.id = touched.user_id;
```

`$2` = `now + session_ttl`、`$3` = `now`，都由 Go 侧计算（与 `request` 等表由 Go 供给时间戳的既有做法一致）。零行即“无会话”，返回 401。JOIN `app_user` 使会话读取与用户读取合成一次查询，用户被删除时会话自然失效。

**过期清理**：每次登录成功后执行 `DELETE FROM user_session WHERE user_id = $1 AND expires_at <= $2`，只清当前用户的过期行。不设后台定时任务——不再登录的用户留下的过期行是惰性垃圾，不影响正确性。删除用户时在既有的删除事务里一并清掉该用户的全部会话。

**登出**按 id 删除对应行，会话立即失效，不依赖 cookie 过期。

### cookie

名称固定 `__Host-Http-Picotera-Auth`，`Options` 固定 `Path=/; HttpOnly; Secure; SameSite=Lax`，不设 `Domain`（`__Host-` 前缀的要求）。不按环境分支降级。

编码、加密与浏览器侧过期交给 `github.com/gorilla/sessions` 的 `CookieStore`（底层 `gorilla/securecookie`：HMAC-SHA256 认证 + AES 加密 + 时间戳校验），不手写密码学原语。session id 本身是不可猜的随机串，cookie 的加密是叠加的一层，不是安全性的唯一来源。

**密钥**：`securecookie` 需要认证密钥与加密密钥两把。由 `HKDF-SHA256(session_secret, info=...)` 用两个不同的 info 串各派生 32 字节，得到 hash key 与 AES-256 的 block key。运维仍只配一个 secret。

**同一个 cookie 名承载两种状态**，`k` 字段区分——登录中途本就没有会话，两者不会共存：

```
state   {k:"a", state:…, verifier:…, redirect:…}
session {k:"s", sid:…}
```

**两个 store，两个有效期**。`securecookie` 的过期窗口是 codec 级别的，因此建两个共享同一对密钥、`MaxAge` 不同的 `CookieStore`：state store 为 10 分钟，session store 为 `session_ttl`。

一律用 `store.New(r, name)` 而非 `store.Get(r, name)`：后者按 cookie 名缓存在 per-request registry 里，两个 store 用同一个名字会互相串台；`New` 每次从请求现解，正是这里需要的语义。解码失败（密钥轮换、篡改、超期）时 `New` 返回错误且 `session.IsNew` 为真，一律等价于“没有会话”，返回 401 / 触发登录，不产生 500。

`SameSite=Lax` 同时满足两件事：IdP 回调是顶层 GET 导航，Lax 允许携带 state cookie；而跨站发起的管理 API 请求（非导航）不会带上会话 cookie。

**浏览器侧与数据库侧的过期保持同步**：认证成功后中间件重新 `Save` 一次 cookie（值不变，仍是同一个 session id），`securecookie` 的时间戳与 `Max-Age` 随之刷新，与数据库里刚被推到 `now + ttl` 的 `expires_at` 对齐。代价是每个已认证的管理 API 响应都带一个 `Set-Cookie`（约 150 字节）；换来的是两侧过期时刻严格一致，不需要额外记录签发时间，也不需要“过半才续”的启发式。

**用户状态实时生效**：每次请求都会回查 `app_user`，因此 `disabled` / `is_admin` 的变更立即生效，无需等待会话过期。

## 登录流程

1. **发起** `GET /api/picotera/auth/login?redirect_to=<path>`：生成 32 字节随机 state 与（启用 PKCE 时）`oauth2.GenerateVerifier()`，写入 state cookie，`oauth2.Config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))` 后 302，`redirect_uri = base_url + "/api/picotera/auth/callback"`。
   `redirect_to` 严格校验：必须以单个 `/` 开头（拒绝 `//` 与 `/\`）、不含 CR/LF，否则 400；缺省为 `/`。
2. **回调** `GET /api/picotera/auth/callback`：IdP 返回 `error` 参数时直接渲染错误页。解出 state cookie，`k` 必须是 `a`，`state` 与 query 中的值做 `subtle.ConstantTimeCompare`，不一致或 cookie 缺失 → 400 错误页，附“重新登录”链接。
3. **换取令牌**：`oauth2.Config.Exchange(ctx, code, oauth2.VerifierOption(verifier))`。客户端认证方式由 `Endpoint.AuthStyle` 决定。
4. **取身份**：`provider.UserInfo(ctx, oauth2.StaticTokenSource(token))`，要求 `Subject` 非空，否则 502 错误页。
5. **落库**：以 `(provider="oidc", identity=sub)` 走既有的 `resolveOrCreate`，`auto_create_user` 决定未知身份是创建还是 401。显示名从 `UserInfo.Claims` 取 `name` → `preferred_username` → `email` → `sub` 的首个非空值，**仅在创建时使用**，后续登录不覆盖。
6. **建会话**：清理该用户的过期 `user_session` 行，插入新行（随机 id、`expires_at = now + session_ttl`），把 id 写入 session cookie，302 到 state 中的 `redirect`。

**不验签 ID token，也不使用 nonce。** 身份来自 userinfo 的直连 TLS 后信道响应，授权码注入由 state（绑定浏览器 cookie）与 PKCE 拦截，ID token 在整个流程中不被消费——`provider.Verifier` 不会被调用。

**多标签页**：后发起的登录会覆盖 state cookie，先发起的那个标签页回调时 state 校验失败，得到带“重新登录”链接的 400 错误页，不自动重试（避免重定向循环）。

## 未登录时的重定向

浏览器顶层导航走的是网关 catch-all 的 SPA 兜底分支（`serveRouteNotFound`），不经过 `auth.Middleware`。在 oidc 模式下，兜底分支先判断：请求是 GET/HEAD 且 `Accept` 含 `text/html`（顶层导航的可靠信号，脚本 / 样式 / fetch 子资源不含该值），且没有有效会话 cookie → 302 到 `/api/picotera/auth/login?redirect_to=<当前路径>`。其余兜底请求（静态资源）行为不变。

管理 API 的 401 由 `auth.Middleware` 返回，oidc 模式下额外带响应头 `X-PicoTera-Login-Url: /api/picotera/auth/login`。前端在 `openapi-fetch` 的 `onResponse` 中统一拦截：见到该头就做一次顶层跳转（拼上 `redirect_to`）。放在响应头而不是 body，middleware 无需消费 / 克隆响应体；非 oidc 模式不带该头，因此不会出现重定向循环。

## 登出

`POST /api/picotera/auth/logout` 删除 `user_session` 行、清除 cookie，返回 204。会话立即失效——即使 cookie 被留存也无法复用。前端随后跳转 `/`，未登录判定生效并重新走登录流程。

这只终止 PicoTera 的会话，不终止 IdP 会话：IdP 侧仍有 SSO 会话时会静默重新登录。不实现 RP-initiated logout（`end_session_endpoint`），避免额外的配置面。

## 网关 cookie 处理

- `buildUpstreamRequest`：复制请求头时对 `Cookie` 特殊处理——按 `;` 拆分，丢弃名字等于 `__Host-Http-Picotera-Auth` 的那一对，其余原样保留并重新拼接；结果为空则整个头不下发。精确名字匹配，不做前缀猜测。
- `redactRequestCredentials`：对 artifact 副本中的 `Cookie` 头，把同名 cookie 的**值**替换为 `[REDACTED]`，保留名字与其它 cookie——与既有 `redactSetCookieValue` 的处理风格一致。两个调用点（meta 请求与上游请求 artifact）自动覆盖。

meta artifact 记录的是客户端 → PicoTera 的请求，cookie 正是在这里出现；上游 artifact 里 cookie 已被前一步摘除。

## 前端

- `ConfigView` 新增 `authMode` 字段（`single-user-mode` / `http-header` / `oidc`），侧边栏据此决定是否渲染登出按钮。
- `api/plugin.ts` 增加 `onResponse` 中间件处理 `X-PicoTera-Login-Url`。
- 侧边栏底部用户区新增一个 `IconButton`（登出），仅 `authMode === 'oidc'` 时显示。

## 文件布局

| 文件 | 内容 |
| --- | --- |
| `db/migrations/051_user_session.sql` | `user_session` 表与索引。 |
| `db/queries/user_session.sql` | 五条 sqlc 查询（touch / insert / delete / 清过期 / 按用户清）。 |
| `pkg/auth/session.go` | 两个 `CookieStore` 的构造与密钥派生、两种 payload 的读写辅助、session id 生成。 |
| `pkg/auth/oidc.go` | `OIDC` 类型：provider 与 `oauth2.Config` 的惰性构造与缓存、userinfo 取身份。 |
| `pkg/auth/handlers.go` | `Login` / `Callback` / `Logout` 三个 `http.HandlerFunc`，以及供网关调用的 `RedirectUnauthenticatedNav`。 |
| `pkg/auth/auth.go` | 新增 `ProviderOIDC` 常量、`Resolve` 的 oidc 分支（读会话 + 滑动续期）、`RefreshSessionCookie`。 |
| `pkg/auth/middleware.go` | cookie 重签调用、401 的 `X-PicoTera-Login-Url` 响应头。 |
| `pkg/server/handle_user_admin.go` | 删除用户事务中一并清理其会话。 |
| `pkg/configx/config.go` | `BaseURL`、`OIDCConfig`、互斥与 fail-fast 校验。 |
| `pkg/server/server.go` | 构造 `*auth.OIDC`，oidc 启用时在 catch-all 之前注册三条裸路由。 |
| `pkg/server/handle_gateway.go` | SPA 兜底前的未登录重定向。 |
| `pkg/server/gateway_helpers.go` | cookie 剥离与脱敏。 |

## 依赖

新增三个直接依赖：

| 模块 | 许可证 | 用途 |
| --- | --- | --- |
| `github.com/coreos/go-oidc/v3` | Apache-2.0 | 发现文档拉取与校验、provider 元数据、userinfo。 |
| `github.com/gorilla/sessions` | BSD-3-Clause | cookie 会话存储（传递依赖 `gorilla/securecookie`，同许可证）。 |
| `golang.org/x/oauth2` | BSD-3-Clause | 授权码流程与 PKCE。当前已在 `go.sum` 中作为间接依赖（v0.36.0），本次提升为直接依赖。 |

go-oidc 会引入 `github.com/go-jose/go-jose/v4`（Apache-2.0）。三者均为宽松许可证，`THIRD_PARTY_NOTICES.md` 只收录要求署名的许可证（当前仅 LGPL 的 axonhub），无需新增条目。

标准库侧只用到 `crypto/hkdf`（Go 1.24+）、`crypto/rand`、`crypto/subtle`。HTTP 客户端为标准 `http.Client`（10s 超时，默认 transport 已走 `ProxyFromEnvironment`），经 `oidc.ClientContext` 与 `context.WithValue(ctx, oauth2.HTTPClient, …)` 注入两个库。

# 执行计划：OIDC 认证模式

## 1. 配置层 `pkg/configx/config.go`

1. `Config` 新增 `BaseURL`，mapstructure 标签 `base_url`。
2. `AuthConfig` 新增 `OIDC OIDCConfig`，mapstructure 标签 `oidc`；定义 `OIDCConfig`：`Enabled bool`、`Issuer`、`ClientID`、`ClientSecret`、`SessionSecret`、`Scopes`、`AuthEndpoint`、`TokenEndpoint`、`UserinfoEndpoint`、`ClientAuthMethod` 均为 `string`，`SessionTTL time.Duration`，`PKCE *bool`。
3. 默认值：`auth.oidc.session_ttl = 12h`、`auth.oidc.scopes = "openid profile email"`、`auth.oidc.client_auth_method = "client_secret_basic"`。
4. 校验：
   - 把现有的 `!SingleUserMode && !HeaderEnabled` 判断替换为三选一计数——0 个报 `no auth provider enabled`，多于 1 个报 `exactly one auth provider must be enabled`。
   - oidc 启用时逐项校验 `client_id` / `client_secret` / `session_secret` 非空、`session_ttl > 0`、`client_auth_method` 属于枚举。
   - `base_url` 非空且 `url.Parse` 成功、scheme 与 host 非空、path 为空或 `/`、无 query / fragment；把归一化（去尾斜杠）后的值写回 `config.BaseURL`。
   - 当三个 endpoint 未全部配置时：`issuer` 必填、绝对 URL、无 query / fragment、无尾斜杠。
   - 当三个 endpoint 全部配置且 `PKCE == nil` 时报错。
5. `bindEnvs` 已递归处理嵌套结构体，`*bool` 落在 default 分支，无需改动。

## 2. 依赖

```bash
go get github.com/coreos/go-oidc/v3 github.com/gorilla/sessions golang.org/x/oauth2
go mod tidy
```

`golang.org/x/oauth2` 已在 `go.sum`（v0.36.0），只是从间接依赖提升为直接依赖。三者均为宽松许可证，`THIRD_PARTY_NOTICES.md` 不需要新增条目。

## 3. 会话表 `db/migrations/051_user_session.sql` + `db/queries/user_session.sql`

1. 迁移（Up 建表 + 索引，Down `DROP TABLE user_session`）：

```sql
CREATE TABLE user_session (
  id         TEXT PRIMARY KEY,
  user_id    BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX user_session_user_id_idx ON user_session (user_id);
```

2. 五条 sqlc 查询：
   - `TouchUserSession :one` —— CTE 版“读 + 滑动续期”，见 design.md；参数 `id`、`expires_at`（`now+ttl`）、`now`；返回 `app_user` 行。
   - `InsertUserSession :exec` —— `id`、`user_id`、`expires_at`。
   - `DeleteUserSession :exec` —— 按 `id`。
   - `DeleteExpiredUserSessions :exec` —— `WHERE user_id = $1 AND expires_at <= $2`。
   - `DeleteUserSessionsByUser :exec` —— `WHERE user_id = $1`。
3. `sqlc generate`。

## 4. 会话 cookie `pkg/auth/session.go`（新文件）

1. 常量 `SessionCookieName = "__Host-Http-Picotera-Auth"`、`stateTTL = 10 * time.Minute`；payload key 常量 `keyKind` / `keyState` / `keyVerifier` / `keyRedirect` / `keySessionID`；kind 值 `kindState = "a"` / `kindSession = "s"`。
2. `newSessionStores(secret string, sessionTTL time.Duration) (state, session *sessions.CookieStore, err error)`：HKDF-SHA256 以两个不同 info 串各派生 32 字节，得到 hash key 与 block key；两个 store 共用这对密钥，`MaxAge` 分别为 `stateTTL` 与 `sessionTTL`；`Options` 统一为 `Path: "/"`、`HttpOnly: true`、`Secure: true`、`SameSite: http.SameSiteLaxMode`、无 `Domain`。
3. `newSessionID() (string, error)`：32 字节 `crypto/rand` → `base64.RawURLEncoding`。
4. 读写辅助：
   - `readState(store, r) (state, verifier, redirect string, ok bool)`
   - `saveState(store, r, w, state, verifier, redirect string) error`
   - `readSessionID(store, r) (string, bool)`
   - `saveSessionID(store, r, w, sessionID string) error`
   - `clearSessionCookie(store, r, w) error`（`Options.MaxAge = -1` 后 `Save`）

   一律用 `store.New(r, SessionCookieName)` 取 session 对象，**不用** `store.Get`——后者的 per-request registry 按 cookie 名缓存，两个同名 store 会串台。解码失败、`k` 不匹配、字段类型不对一律返回 `ok=false`，不向上抛 error。
5. 单测 `pkg/auth/session_test.go`：state / session 往返；篡改单字节；换一把 secret 解不开；超过 `MaxAge` 后失效；state cookie 被 session store 读出时因 `k` 不符而拒绝；`clearSessionCookie` 产出 `Max-Age=0`；cookie 属性齐全（`Secure` / `HttpOnly` / `Path=/` / 无 `Domain`）。

## 5. OIDC 客户端 `pkg/auth/oidc.go`（新文件）

1. `OIDC` 结构：`cfg configx.OIDCConfig`、`baseURL string`、`autoCreate bool`、两个 store、`resolver *Resolver`、`client *http.Client`（10s 超时）、provider 缓存（`sync.Mutex` + `*providerBundle`，只缓存成功结果）。
2. `NewOIDC(cfg configx.OIDCConfig, baseURL string, autoCreate bool) (*OIDC, error)`：只做密钥派生与 store 构造，不访问网络。
3. `providerBundle`：`provider *oidc.Provider`、`oauth *oauth2.Config`、`pkce bool`。
4. `(o *OIDC) bundle(ctx) (*providerBundle, error)`：
   - `ctx = oidc.ClientContext(ctx, o.client)`。
   - 三个 endpoint 全部配置 → `(&oidc.ProviderConfig{IssuerURL: cfg.Issuer, AuthURL, TokenURL, UserInfoURL}).NewProvider(ctx)`，`pkce = *cfg.PKCE`（配置层已保证非 nil）。
   - 否则 → `oidc.NewProvider(ctx, cfg.Issuer)`（issuer 严格相等由库校验），再 `provider.Claims(&doc)` 取原始发现文档，`doc.CodeChallengeMethodsSupported` 含 `S256` 即推断 `pkce` 为真，`cfg.PKCE != nil` 时以配置为准。
     配置里有任一 endpoint 覆盖时需要重建 provider：`*oidc.Provider` 只导出 `Endpoint()`（auth + token），`userinfo_endpoint` / `jwks_uri` 是未导出字段，因此从 `doc` 里取这两个值，与配置覆盖逐项合并后填 `oidc.ProviderConfig` 重建。顺序不可颠倒——`ProviderConfig.NewProvider` 构造出的 provider 没有原始文档，`Claims` 会报错。
   - 组装 `oauth2.Config{ClientID, ClientSecret, RedirectURL: baseURL + "/api/picotera/auth/callback", Scopes: strings.Fields(cfg.Scopes), Endpoint: …}`，`Endpoint.AuthStyle` 按 `client_auth_method` 取 `AuthStyleInHeader` / `AuthStyleInParams`。
   - 成功后缓存，失败不缓存。
5. `(o *OIDC) identity(ctx, token *oauth2.Token, b *providerBundle) (sub, displayName string, err error)`：`b.provider.UserInfo(ctx, oauth2.StaticTokenSource(token))`；`Subject` 为空报错；`ui.Claims(&claims)` 后显示名取 `name` → `preferred_username` → `email` → `sub` 的首个非空值。
6. 单测 `pkg/auth/oidc_test.go`（`httptest.Server` 承载假 IdP）：发现文档 issuer 不匹配被拒；PKCE 的三种来源（发现推断、配置覆盖、全覆盖模式下取配置）；`AuthStyle` 映射；配置 endpoint 覆盖发现结果；userinfo 缺 `sub` 报错；显示名回退顺序。

## 6. 解析器与中间件 `pkg/auth/auth.go` / `middleware.go`

1. 新增 `ProviderOIDC = "oidc"`。
2. `Resolver` 增加 `oidc *OIDC` 字段；`NewResolver` 增加该参数。
3. `Resolve` 改为单分支派发：`SingleUserMode` / `HeaderEnabled` / `OIDC.Enabled` 三选一（互斥已由配置层保证），均未启用时返回 `ErrUnauthorized`。oidc 分支 `readSessionID` → `TouchUserSession(id, now+ttl, now)`；`pgx.ErrNoRows` → `ErrUnauthorized`；返回的 `app_user` 若 `disabled` 同样 `ErrUnauthorized`。
   这条分支不走 `resolveOrCreate`：用户创建只发生在回调流程里（那里才拿得到显示名），持有会话却查不到用户意味着用户已被删除，应当 401。
4. 新增 `(r *Resolver) RefreshSessionCookie(w http.ResponseWriter, req *http.Request)`：非 oidc 模式直接返回；否则以原 session id 重新 `saveSessionID`，刷新浏览器侧的 `Max-Age` 与 securecookie 时间戳，与刚被推到 `now+ttl` 的 `expires_at` 对齐。无数据库访问。
5. 新增 `(r *Resolver) LoginURL() string`：oidc 模式返回 `/api/picotera/auth/login`，否则空串。
6. `Middleware`：成功分支在 `next.ServeHTTP` 之前调用 `RefreshSessionCookie`；失败分支在 401 时，若 `LoginURL()` 非空则设置 `X-PicoTera-Login-Url` 响应头。

## 7. HTTP 处理器 `pkg/auth/handlers.go`（新文件）

1. `validateRedirectTo(raw string) (string, bool)`：空 → `/`；必须以 `/` 开头，不得以 `//` 或 `/\` 开头，不得含 `\r` / `\n`。
2. `(o *OIDC) Login(w, r)`：校验 `redirect_to` → `bundle(ctx)` → 生成 state（32 字节随机 base64url）与 `oauth2.GenerateVerifier()`（PKCE 时）→ `saveState` → `b.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))` → 302。
3. `(o *OIDC) Callback(w, r)`：按 api.md 的顺序处理 `error` 参数 → `readState` → `subtle.ConstantTimeCompare` 比对 state → `b.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))` → `identity(...)` → `o.resolver.resolveOrCreate(ProviderOIDC, sub, displayName, false, o.autoCreate)` → `DeleteExpiredUserSessions(userID, now)` → `newSessionID()` + `InsertUserSession(id, userID, now+ttl)` → `saveSessionID` → 302 到 `redirect`。
   清理与插入不放进同一个事务：清理失败只是留下垃圾行，不应阻断登录，记一条 warn 即可。
4. `(o *OIDC) Logout(w, r)`：`readSessionID` 成功则 `DeleteUserSession`，随后 `clearSessionCookie` + 204；cookie 缺失或解不开时同样 204。
5. `(o *OIDC) RedirectUnauthenticatedNav(w, r) bool`：GET/HEAD 且 `Accept` 含 `text/html` 且 cookie 里没有可解出的 session id → 302 到 `/api/picotera/auth/login?redirect_to=<r.URL.RequestURI()>` 并返回 `true`；否则 `false`。
   这里只看 cookie，不查库：导航路径上多一次数据库往返不值得，而持有已失效 session id 的浏览器会在随后的管理 API 401 上被 `X-PicoTera-Login-Url` 兜住。
6. 错误页：单个 `writeAuthError(w, status int, title, detail string)`，输出极简 HTML，含指向 `/api/picotera/auth/login` 的“重新登录”链接。
7. `OIDC` 持有 `*Resolver` 引用（同包内可直接调用未导出的 `resolveOrCreate`），在 `NewServer` 中双向装配。
8. 单测：`validateRedirectTo` 的通过与拒绝用例；`RedirectUnauthenticatedNav` 在有 / 无会话、`Accept` 含 / 不含 `text/html`、非 GET 时的行为；state 不匹配走 400。

## 8. 服务器装配 `pkg/server/server.go`

1. `NewServer` 中：oidc 启用时 `auth.NewOIDC(...)`，与 `auth.NewResolver` 互相装配；非 oidc 模式为 nil。
2. `Server` 新增 `oidc *auth.OIDC` 字段。
3. `registerEndpoints`：oidc 启用时，在 catch-all `Mount("/")` **之前**于 `s.router` 注册
   `GET /api/picotera/auth/login`、`GET /api/picotera/auth/callback`、`POST /api/picotera/auth/logout`。
4. `NewHuma()` 构造的裸 `Server` 没有 oidc，`register()` 不触碰这些路由，openapi 生成路径不受影响。

## 9. 网关 `pkg/server/handle_gateway.go`

`serveRouteNotFound` 的 SPA 兜底分支改为：

```go
if routeNotFoundFallsBackToSPA(r, auth.ok()) {
    if h.oidc != nil && h.oidc.RedirectUnauthenticatedNav(w, r) {
        return
    }
    h.staticHandler.ServeHTTP(w, r)
    return
}
```

其余分支不变。

## 10. 网关 cookie 处理 `pkg/server/gateway_helpers.go`

1. 新增 `stripPicoteraCookie(value string) string`：按 `;` 拆分、逐段 trim、丢弃名字等于 `auth.SessionCookieName` 的一对，其余按 `"; "` 重新拼接。
2. `buildUpstreamRequest` 的请求头复制循环：`lower == "cookie"` 时逐个值过一遍 `stripPicoteraCookie`，结果为空串则不添加该值。
3. 新增 `redactCookieValue(value string) string`：把同名 cookie 的值替换为 `[REDACTED]`，其余 cookie 原样保留。
4. `redactRequestCredentials` 中对 `Cookie` 的每个值调用它并写回。
5. 单测 `pkg/server/gateway_helpers_test.go`：只有 picotera cookie / 混合多个 cookie / 完全没有 picotera cookie / 名字是其它 cookie 前缀的近似名 / 值中含 `=` 的情形。

## 11. 删除用户时清理会话 `pkg/server/handle_user_admin.go`

`handleDeleteUser` 的事务中，在 `DeleteUserIdentitiesByUser` 旁加一条 `DeleteUserSessionsByUser`。会话本身在用户被删后已因 JOIN `app_user` 失效，这一步是为了不留孤儿行。

## 12. 契约与前端

1. `pkg/contract/config.go`：`ConfigView` 新增 `AuthMode string`，json 标签 `authMode`。
2. `pkg/server/handle_config.go`：按配置返回 `single-user-mode` / `http-header` / `oidc`。
3. `mise run openapi` → `pnpm --dir dashboard generate-openapi`。
4. `dashboard/src/api/plugin.ts`：`client.use` 增加 `onResponse`，`response.status === 401` 且有 `X-PicoTera-Login-Url` 头时，`window.location.assign(loginUrl + '?redirect_to=' + encodeURIComponent(location.pathname + location.search))`。
5. `dashboard/src/components/AppSidebar.vue`：引入 `useAppTitle` 已有的 config 查询（或扩展它暴露 `authMode`），`authMode === 'oidc'` 时在底部用户区渲染登出 `IconButton`；点击调用新增的 `logout()` fetcher（`client.ts`），成功后 `window.location.assign('/')`。
6. `dashboard/src/api/client.ts`：新增 `logout()`，直接 `fetch('/api/picotera/auth/logout', { method: 'POST' })`（该路由不在 openapi 契约中）。
7. `pnpm --dir dashboard type-check && pnpm --dir dashboard lint && pnpm --dir dashboard format`。

## 13. 文档

更新 `CLAUDE.md`：

- “User authentication (`pkg/auth/`)” 段落——三选一互斥（替换现有优先级链描述）、oidc 模式的配置与流程、`user_session` 表与滑动续期语义、cookie 只装 session id。
- “Request credential hygiene” 段落——补充网关的 cookie 剥离与脱敏。
- “Database Schema” 段落——把 `user_session` 加入核心表清单。

## 14. 验证

1. `go build ./... && go test ./...`。
2. `docker compose up -d`，配一个测试 IdP，跑通登录 → 落 cookie 与 `user_session` 行 → `/me` → 登出（行被删除、再次请求 401）。
3. 校验滑动续期：登录后隔一段时间再请求，确认 `expires_at` 被推进到“本次请求时间 + 12h”，且响应带 `Set-Cookie`。
4. 校验登录清理：制造一行该用户的过期会话，再登录一次，确认过期行被删除而其它用户的过期行不受影响。
5. 校验互斥：同时设 `single_user_mode` 与 `oidc.enabled` 时启动失败。
6. 校验网关：带 `Cookie: __Host-Http-Picotera-Auth=x; other=y` 打一次网关请求，确认上游只收到 `other=y`，meta artifact 中 picotera cookie 值为 `[REDACTED]` 且 `other` 保留。

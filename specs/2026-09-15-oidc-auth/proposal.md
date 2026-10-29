# OIDC 用户认证模式

## 原始需求

为用户认证新增“oidc”模式：运维可以配置 oidc issuer url 和 client id + secret, 并可选地配置自定义 token endpoint / userinfo endpoint / auth endpoint / pkce 以替代 oidc 自动发现。该模式下，当用户未登录时，页面将重定向到 auth endpoint，后端通过 cookie 管理会话，cookie 叫 `__Host-Http-Picotera-Auth` 吧。在这种模式下用户必须配置 picotera 的 base url，这将被当作 oauth callback 的 base url 使用。

会话是有状态的：cookie 里只保存 session id，数据库里建一张表存放 session id 与 user id 的对应关系和过期时间。每个 session 的有效期是**最后一次请求时间 + 12 小时**。每次成功登录时，自动清理当前用户的过期 session。session 仅在 oidc 模式下需要。

网关需要增加脱敏，避免将 picotera 的 cookie 发送到渠道，但需要保留其它 cookie。

## 澄清补充

规划期间与用户确认的细节：

1. **会话 cookie 加密密钥**：使用独立的必填环境变量（`PICOTERA_AUTH_OIDC_SESSION_SECRET`），oidc 模式下缺失即启动失败。不从 client secret 派生，两者生命周期解耦。
2. **认证模式互斥**：`single_user_mode` / `header_enabled` / `oidc_enabled` 三者最多只能启用一个，多于一个时启动即失败。这会改变现有行为（目前 `single_user_mode` + `header_enabled` 可同时配置，前者胜出）。
3. **会话续期**：IdP 认证只发生在首次登录，之后完全由 PicoTera 自己的 session 轮转。session 有效期可配置（默认 12 小时），有效期内的请求自动滑动续期，不保存 refresh token，也不再回访 IdP。
4. **HTTPS**：不校验 base url 的 scheme。cookie 固定使用 `__Host-` 前缀并始终携带 `Secure`，运维自行保证 secure context。
5. **实现选型**：cookie 的编码与加密用 `github.com/gorilla/sessions`，OIDC 协议用 `github.com/coreos/go-oidc`，不手写密码学原语与协议细节。

# 设计：多用户功能

## 概述

第一期引入「用户」与「用户身份」两张表、一个可插拔的身份解析层（第一期实现 `http-header` 与 `single-user-mode` 两种提供商）、一个作用于 `/api/picotera` 的鉴权中间件、一个 `set-admin` CLI 子命令，以及控制台左下角的用户名展示。不做权限控制（管理员标志仅落库，不参与任何鉴权判断），不做资源用户归属。

鉴权与网关数据面完全解耦：网关 catch-all 与迁移后的 `/api/unified` 用 API Key 鉴权，不经过用户中间件；只有 `/api/picotera` 前缀的内部管理 API 需要用户身份。

## 数据库

新增两张表（goose 迁移 `033_users.sql`）。`user` 是 Postgres 保留字，表名用 `app_user` 避免到处加引号，与现有单数命名约定（`provider`、`api_key`、`script`）一致。

```sql
CREATE TABLE app_user (
  id           BIGSERIAL PRIMARY KEY,
  display_name TEXT NOT NULL,
  is_admin     BOOLEAN NOT NULL DEFAULT false,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE user_identity (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT NOT NULL,
  provider   TEXT NOT NULL,
  identity   TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, identity)
);

CREATE INDEX user_identity_user_id_idx ON user_identity (user_id);
```

- `user_identity.user_id` 是对 `app_user.id` 的逻辑引用，**不加外键约束**——与代码库既有约定一致（如 `request.project_id` 同样无 FK）。
- `(provider, identity)` 唯一约束是身份解析的核心键，也是自动创建时的并发护栏（`ON CONFLICT DO NOTHING`）。
- 用户可绑定多条 `user_identity`（一对多）。

## 配置（环境变量）

在 `configx.Config` 新增 `Auth AuthConfig`：

```go
type AuthConfig struct {
    HeaderEnabled  bool   `mapstructure:"header_enabled"`
    HeaderName     string `mapstructure:"header_name"`
    AutoCreateUser bool   `mapstructure:"auto_create_user"`
    SingleUserMode bool   `mapstructure:"single_user_mode"`
}
```

对应环境变量：

| 环境变量 | 含义 | 默认 |
|---|---|---|
| `PICOTERA_AUTH_HEADER_ENABLED` | 开启 http-header 提供商 | false |
| `PICOTERA_AUTH_HEADER_NAME` | 读取身份的 header 名 | 空 |
| `PICOTERA_AUTH_AUTO_CREATE_USER` | 匹配不到用户时自动创建 | false |
| `PICOTERA_AUTH_SINGLE_USER_MODE` | 单用户模式 | false |

**启动期校验（fail fast）**：当 `HeaderEnabled=true` 且 `HeaderName` 为空时，启动直接报错退出，不做任何默认 header 名猜测。

## 身份解析与鉴权中间件

新增 `pkg/auth/` 包，封装身份解析逻辑与 context 存取，使其可被中间件与（未来的）handler 复用。

### 解析顺序（`auth.Resolver.Resolve(ctx, r)`）

1. **单用户模式优先**：`SingleUserMode=true` 时，忽略所有 header，固定 `provider="single-user-mode"`、`identity="root"`。查 `(single-user-mode, root)`；不存在则**无条件**创建用户（`display_name="root"`、`is_admin=true`）并写入身份，再返回该用户。不受 `AutoCreateUser` 影响。
2. **HTTP Header 提供商**：否则若 `HeaderEnabled=true`，读取 `HeaderName` 对应 header：
   - header 缺失或为空 → 未鉴权（401）。
   - 非空值作为 `identity`，查 `(http-header, value)`：命中则返回用户；未命中且 `AutoCreateUser=true` 则创建用户（`display_name=value`、`is_admin=false`）并写入身份；未命中且未开自动创建 → 401。
3. **都未配置** → 始终 401（不做隐式默认）。

自动创建走 `CreateUserWithIdentity` 事务：插入 `app_user` 取得 id，再 `INSERT INTO user_identity ... ON CONFLICT (provider, identity) DO NOTHING`；若冲突（并发已创建）则回滚并重新按身份查询，保证幂等。

### 中间件

`auth.Middleware(resolver)` 返回 chi 中间件，**不**在中间件内部匹配路径前缀，而是通过路由分组把它精确挂在 `/api/picotera` 这组路由上：

- `NewServer` 在 `router.Use(decompressRequest)` 之后，用 `mgmtRouter := router.With(auth.Middleware(resolver))` 派生一个携带该中间件的内联子路由（chi 的 `With` 与父路由共享路由树，只对在其上注册的路由套用中间件）。
- 所有 `/api/picotera` 路由都注册在 `mgmtRouter` 上：`humachi.New(mgmtRouter, ...)` 承载全部 Huma 管理操作（含 Huma 自带的 `/openapi.*`、`/docs`），`registerEndpoints` 里的 `test/direct` 也改挂 `mgmtRouter`。
- 网关 catch-all（`router.Mount("/", …)`）与 `/api/unified` 仍注册在裸 `router` 上，不经过该中间件，按 API Key 鉴权。
- 中间件自身逻辑只剩鉴权：解析成功把 `*db.AppUser` 写入 request context（`auth.WithUser`）放行；失败写 `401` JSON（`{"message":"unauthorized"}`）短路。

humachi 把 request context 透传给 Huma handler 的 `ctx`，因此 `me` handler 通过 `auth.UserFromContext(ctx)` 取当前用户，无需额外管线。

> 由于 Huma 自带的 `/openapi.*` 与 `/docs` 也随管理 API 注册在 `mgmtRouter` 上，它们同样需要用户鉴权（此前的前缀白名单写法会放行这些根路径）。控制台从仓库内置的 `openapi.yaml` 生成类型，不依赖运行时这些端点，故无影响。

> 中间件用闭包构造（`auth.Middleware(resolver)`），`resolver` 持有 `queries` 与 `AuthConfig`，在 `NewServer` 构建 router 阶段即可注册，不依赖尚未构建完成的 `*Server`。

## 路由迁移：unified → /api/unified

`registerEndpoints()` 中五条 unified 路由前缀从 `/api/picotera` 改为 `/api/unified`：

- `POST /api/unified/v1/messages`
- `POST /api/unified/v1/responses`
- `POST /api/unified/v1/chat/completions`
- `POST /api/unified/v1beta/models/{model}:generateContent`
- `POST /api/unified/v1beta/models/{model}:streamGenerateContent`

`test/direct` 仍为内部接口，保留在 `/api/picotera/test/direct`（需要鉴权）。CLAUDE.md 中描述这五条路由的段落同步更新。

> **破坏性变更**：unified 路由地址变化。按项目「不做兼容层」约定，不保留旧 `/api/picotera/v1/*` 路由；调用方需更新到 `/api/unified`。

## 自身信息 API 与控制台展示

- 后端新增 Huma operation `GET /api/picotera/me`，返回当前用户 `{id, displayName, isAdmin}`（见 `api.md`）。从 context 取用户，正常情况下中间件已保证存在；缺失则 500（不应发生）。
- 前端在 `src/api/client.ts` 增加 `fetchMe` fetcher、`queryKeys.me`，`AppSidebar.vue` 用 `useQuery` 读取并在左下角展示用户名。
- 底栏布局调整：用户名占左侧 `flex-1 truncate`，`PreferencesMenu` 与刷新按钮靠右。

## CLI：set-admin

`cmd/picotera/main.go` 新增 cobra 子命令：

```
picotera set-admin <user-id>
```

流程：`configx.Parse()` → `pgxpool.New` → `UpdateUserAdmin(id, true)`（`db/queries/user.sql`）→ 打印结果。用户不存在时返回非零退出码并报错。参数严格解析为整数，非法输入直接报错（fail fast）。

## 不在本期范围

- 任何权限控制：`is_admin` 仅落库，不参与鉴权判断；所有已鉴权用户对内部 API 拥有同等访问。
- 资源的用户归属（provider / key / request 等不记录创建者）。
- 用户管理界面、登录页、会话/登出。第一期假定身份由反向代理注入 header，或单用户模式直通。

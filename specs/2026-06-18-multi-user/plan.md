# 执行计划：多用户功能

## 1. 数据库迁移

- 新建 `db/migrations/033_users.sql`：建 `app_user`、`user_identity` 两表及索引（见 design.md）。Down 段 `DROP TABLE user_identity; DROP TABLE app_user;`。

## 2. sqlc 查询

- 新建 `db/queries/user.sql`：
  - `GetUserByID :one` — `SELECT * FROM app_user WHERE id = $1`。
  - `GetUserByIdentity :one` — `SELECT u.* FROM app_user u JOIN user_identity i ON i.user_id = u.id WHERE i.provider = $1 AND i.identity = $2`。
  - `InsertUser :one` — `INSERT INTO app_user (display_name, is_admin) VALUES ($1, $2) RETURNING *`。
  - `InsertUserIdentity :one` — `INSERT INTO user_identity (user_id, provider, identity) VALUES ($1,$2,$3) ON CONFLICT (provider, identity) DO NOTHING RETURNING *`。
  - `UpdateUserAdmin :one` — `UPDATE app_user SET is_admin = $2, updated_at = now() WHERE id = $1 RETURNING *`。
- 运行 `sqlc generate`，生成 `pkg/db` 中的 `AppUser`、`UserIdentity` 模型与 `Querier` 方法。

## 3. 配置

- `pkg/configx/configx.go`：`Config` 增加 `Auth AuthConfig`；定义 `AuthConfig` 四字段（见 design.md）。`bindEnvs` 已递归处理嵌套 struct，无需额外改动。
- 在 `Parse()` 末尾（unmarshal 后）做 fail-fast 校验：`HeaderEnabled && HeaderName == ""` → 返回 error。

## 4. auth 包

- 新建 `pkg/auth/auth.go`：
  - context key 与 `WithUser(ctx, *db.AppUser)` / `UserFromContext(ctx) *db.AppUser`。
  - `Resolver` 结构体，持有 `queries db.Querier` 与 `configx.AuthConfig`。
  - `Resolve(ctx, r) (*db.AppUser, error)`：实现 design.md 的三步解析顺序。
  - `resolveOrCreate(ctx, provider, identity, displayName string, admin, autoCreate bool)`：查 `GetUserByIdentity`；未命中按 `autoCreate` 决定是否走 `CreateUserWithIdentity`。
  - `CreateUserWithIdentity`：开事务（`s.db.Begin` 经由 `queries.WithTx`），`InsertUser` → `InsertUserIdentity`（ON CONFLICT DO NOTHING）。若 identity 插入返回 0 行（并发冲突），回滚事务并改用非事务 `GetUserByIdentity` 重查返回。
- 新建 `pkg/auth/middleware.go`：`Middleware(resolver *Resolver) func(http.Handler) http.Handler`。仅对 `/api/picotera` 前缀鉴权，成功 `WithUser` 放行，失败写 401 JSON。

> `Resolver` 需要事务能力，注入 `*pgxpool.Pool` 与 `*db.Queries`（`db.New(tx)` 创建事务版 queries）。

## 5. server 接线

- `pkg/server/server.go`：
  - 在 `router.Use(decompressRequest)` 之后追加 `router.Use(auth.Middleware(auth.NewResolver(conn, queries, config.Auth)))`。
  - `registerOperations()`：在 `mgmt` 组注册 `OperationGetMe`。
  - `registerEndpoints()`：五条 unified 路由前缀 `/api/picotera` → `/api/unified`。
- 新建 `pkg/server/handle_user.go`：`handleGetMe`（见 api.md）。
- `pkg/contract/user.go`：`MeView`、`ToMeView`、`GetMeResponse`、`OperationGetMe`。

## 6. CLI：set-admin

- `cmd/picotera/main.go`：新增 cobra 子命令 `set-admin <user-id>`。`Args: cobra.ExactArgs(1)`，`strconv.ParseInt` 严格解析（失败报错）。`configx.Parse` → `pgxpool.New` → `db.New(pool).UpdateUserAdmin(ctx, ...)`；`pgx.ErrNoRows` → 报「用户不存在」并 `os.Exit(1)`。成功打印用户 id + display_name。

## 7. 前端

- `dashboard/src/api/client.ts`：新增 `fetchMe()`，调用 `api.GET('/api/picotera/me')`，沿用 `ApiRequestError` 约定。
- `dashboard/src/api/queryKeys.ts`：新增 `me: ['me'] as const`。
- `dashboard/src/components/AppSidebar.vue`：
  - `useQuery({ queryKey: queryKeys.me, queryFn: fetchMe })` 取用户名。
  - 底栏改布局：左侧 `<span class="flex-1 truncate ...">{{ me?.displayName }}</span>`，右侧依次 `PreferencesMenu` 与刷新按钮（`gap-2`，按钮 `shrink-0`）。
  - 用户名加载中/失败时降级为占位（如空字符串），不阻塞底栏。

## 8. 重新生成与文档

- `mise run openapi` 重新生成 `openapi.yaml`（新增 `getMe`）。
- `pnpm --dir dashboard generate-openapi` 重新生成 TS 类型。
- 更新根 `CLAUDE.md`：unified 路由章节地址改为 `/api/unified`；补一段「身份鉴权」说明（中间件作用于 `/api/picotera`、两种提供商、四个环境变量、单用户模式、CLI）。

## 9. 验证

- `go build -o picotera ./cmd/picotera` 通过。
- 启动（单用户模式）：`PICOTERA_AUTH_SINGLE_USER_MODE=true` → 访问 `/api/picotera/me` 返回 root 用户且 `isAdmin=true`；首启自动建用户。
- 启动（header 模式）：`PICOTERA_AUTH_HEADER_ENABLED=true PICOTERA_AUTH_HEADER_NAME=X-Forwarded-User`，带/不带 header 分别验证 200 / 401；开 `AUTO_CREATE_USER` 验证自动建用户。
- 未配置任何 provider：`/api/picotera/*` 返回 401；`/api/unified/v1/messages` 与网关路由不受影响。
- `picotera set-admin <id>` 提权后 `me.isAdmin=true`。
- `pnpm --dir dashboard build` 通过；控制台左下角显示用户名、两个按钮靠右。

## 文件清单

**新增**：`db/migrations/033_users.sql`、`db/queries/user.sql`、`pkg/auth/auth.go`、`pkg/auth/middleware.go`、`pkg/contract/user.go`、`pkg/server/handle_user.go`。
**修改**：`pkg/configx/configx.go`、`pkg/server/server.go`、`cmd/picotera/main.go`、`dashboard/src/api/client.ts`、`dashboard/src/api/queryKeys.ts`、`dashboard/src/components/AppSidebar.vue`、`CLAUDE.md`、`openapi.yaml`、`dashboard/src/openapi-types.d.ts`、`pkg/db/*`（sqlc 生成）。

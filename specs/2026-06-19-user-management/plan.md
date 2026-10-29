# 执行计划

## 1. 数据库与查询

1. 新增 `db/migrations/034_user_disabled.sql`：`ALTER TABLE app_user ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT false;`（含 Down）。
2. 在 `db/queries/user.sql` 增加查询：
   - `ListUsers :many` — `SELECT * FROM app_user ORDER BY id`。
   - `UpdateUser :one` — 按 `id` 更新 `display_name`、`is_admin`、`disabled`，`updated_at = now()`，`RETURNING *`。
   - `DeleteUser :exec` — `DELETE FROM app_user WHERE id = $1`。
   - `DeleteUserIdentitiesByUser :exec` — `DELETE FROM user_identity WHERE user_id = $1`。
   - `ListUserIdentities :many` — 按 `user_id` 列出，`ORDER BY id`。
   - `GetUserIdentityByID :one` — `SELECT * FROM user_identity WHERE id = $1`。
   - `CreateUserIdentity :one` — 严格 INSERT（不带 `ON CONFLICT`），`RETURNING *`。
   - `UpdateUserIdentity :one` — 按 `id` 更新 `provider`、`identity`，`RETURNING *`。
   - `DeleteUserIdentity :exec` — `DELETE FROM user_identity WHERE id = $1`。
   - 保留既有 `InsertUserIdentity`（鉴权幂等路径专用）。
3. 运行 `sqlc generate` 重新生成 `pkg/db/`。

## 2. 鉴权解析器

4. 在 `pkg/auth/auth.go` 的 `resolveOrCreate` 命中已存在用户分支后增加：`if u.Disabled { return nil, ErrUnauthorized }`。

## 3. 后端契约与处理器

5. 扩展 `pkg/contract/user.go`：
   - `UserView` + `ToUserView(*db.AppUser)`；`UserIdentityView` + `ToUserIdentityView(*db.UserIdentity)`。
   - 请求/响应类型：`List/Get/Create/Update/DeleteUser*`，`List/Create/Update/DeleteUserIdentity*`。
   - 对应 `huma.Operation` 定义（OperationID、Method、Path、Summary、Tags `["User"]`）。
6. 新增 `pkg/server/handle_user_admin.go`（或扩展 `handle_user.go`）实现各处理器：
   - 用户 CRUD；`createUser` 校验 `displayName` 非空。
   - `deleteUser`：用 `s.pool.BeginTx` 开事务，`q := s.queries.WithTx(tx)`，先 `DeleteUserIdentitiesByUser` 再 `DeleteUser`，提交。
   - 身份处理器：`createUserIdentity` 先 `GetUserByID` 校验用户存在（404），唯一冲突映射 409（复用 `isUniqueViolation`）；`update/deleteUserIdentity` 先 `GetUserIdentityByID` 校验存在且 `UserID == userId`（404）。
7. 在 `pkg/server/server.go` 的 `registerOperations()` 注册全部新操作。

## 4. 重生成 OpenAPI 与前端类型

8. `mise run openapi` 重写 `openapi.yaml`。
9. `pnpm --dir dashboard generate-openapi` 重生成 `dashboard/src/openapi-types.d.ts`。

## 5. 控制台数据层

10. `dashboard/src/api/queryKeys.ts`：增加 `users` 键族（`all`、`detail(id)`、`identities(userId)`）。
11. `dashboard/src/api/client.ts`：增加 fetcher（`listUsers`、`createUser`、`updateUser`、`deleteUser`、`listUserIdentities`、`createUserIdentity`、`updateUserIdentity`、`deleteUserIdentity`）与 `invalidateUsers` / `invalidateUserIdentities`。

## 6. 控制台界面

12. `dashboard/src/ui/icons/paths.ts`：增加 `users` 图标（取自 `@tabler/icons-vue`）。
13. 新增 `dashboard/src/components/UserForm.vue`（SidePanel）：显示名、是否管理员、是否禁用；新建/编辑两态，仿 `ApiKeyForm.vue`。
14. 新增 `dashboard/src/components/UserIdentitiesPanel.vue`（SidePanel）：仿 `ProviderEndpointsPanel.vue`，列出身份并支持行内编辑（provider + identity）与删除，底部新增身份表单。
15. 新增 `dashboard/src/views/UsersView.vue`：仿 `ProvidersView.vue`，列表 + 启用/禁用、身份面板、编辑、删除动作。
16. 接入 chrome：
    - `dashboard/src/router/index.ts` 增加 `{ path: '/users', name: 'users', component: () => import('@/views/UsersView.vue') }`。
    - `dashboard/src/App.vue` 的 `pageMeta` 增加 `users` 的 `title` / `hint`。
    - `dashboard/src/components/AppSidebar.vue` 的 `nav` 增加 `{ name: 'users', label: '用户', icon: 'users' }`。

## 7. 验证

17. `go build ./...` 通过；`pnpm --dir dashboard type-check` 与 `pnpm --dir dashboard lint` 通过。
18. 手动验证：新建/编辑/删除用户；增删改身份；禁用某用户后以其身份请求 `/api/picotera/me` 返回 401，启用后恢复 200；删除用户后其 identity 一并消失。

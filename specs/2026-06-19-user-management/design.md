# 设计

在已有的 `app_user` / `user_identity` 两张表与 `pkg/auth` 鉴权解析器之上，补齐用户与身份的完整 CRUD（后端 Huma 操作 + 控制台界面），并为用户增加「禁用」标志位接入鉴权。

不引入任何第三方库；沿用现有 sqlc + Huma + chi + vue-query 模式。

## 数据库

新增迁移 `db/migrations/034_user_disabled.sql`：

```sql
-- +goose Up
ALTER TABLE app_user ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT false;
-- +goose Down
ALTER TABLE app_user DROP COLUMN disabled;
```

`user_identity` 表结构不变（已有 `UNIQUE (provider, identity)` 与 `user_identity_user_id_idx`）。

## 鉴权解析器（pkg/auth）

`Resolver.resolveOrCreate` 在命中已存在用户后增加禁用检查：若 `user.Disabled` 为真，返回 `ErrUnauthorized`（中间件写出 `401 {"message":"unauthorized"}`）。

- 该检查对所有身份提供商统一生效，包含单用户模式的 `(single-user-mode, root)`。单用户模式仍在缺失时无条件 bootstrap root 为 `is_admin=true`、`disabled=false`；一旦 root 被显式禁用，后续请求一律 401（按澄清，不做自我锁定保护）。
- 自动创建路径产出的新用户 `disabled=false`，不受影响。
- `is_admin` 仍不参与任何授权判断，仅作标记。

## 后端 CRUD（pkg/contract + pkg/server）

新增查询、契约类型与处理器：

- **用户**：列表、创建、更新（显示名 / 管理员 / 禁用）、删除（事务内级联删除其 identity）。
- **身份**：按用户列出、创建、更新（provider / identity 字符串）、删除；均按用户嵌套寻址，处理器校验目标 identity 归属该用户（fail-fast）。

身份的 `(provider, identity)` 唯一约束冲突映射为 `409 Conflict`（复用 `handle_api_key.go` 的 `isUniqueViolation`）。创建身份使用严格 INSERT（不带 `ON CONFLICT`），与鉴权自动创建路径所用的幂等 `InsertUserIdentity` 区分开——后者保持不变。

删除用户在单个事务内先 `DELETE FROM user_identity WHERE user_id=$1`，再删除 `app_user`，保证级联原子性（表间无 FK 约束）。

CLI（`set-admin`、`bind-identity`）无需改动。

## 控制台（dashboard）

参照渠道页（`ProvidersView` + `ProviderForm` + `ProviderEndpointsPanel`）的「主表单 + 关联子项侧栏面板」双表单结构：

- **UsersView.vue**：用户列表。列：ID、显示名、管理员标记、禁用标记。行内动作：启用/禁用、身份面板、编辑、删除（带 `useConfirm` 确认）。
- **UserForm.vue**（SidePanel）：新建/编辑用户，字段为显示名、是否管理员、是否禁用。
- **UserIdentitiesPanel.vue**（SidePanel，仿 `ProviderEndpointsPanel`）：列出该用户的身份条目，支持行内编辑（provider + identity）与删除；底部「新增身份」表单填 provider + identity。

数据层沿用 vue-query：在 `src/api/client.ts` 增加 fetcher 与 `invalidateUsers` / `invalidateUserIdentities`，在 `src/api/queryKeys.ts` 增加 `users` 键族（`all` / `detail(id)` / `identities(userId)`）。

接入 chrome：`src/router/index.ts` 增加 `/users` 路由；`src/App.vue` 的 `pageMeta` 增加 `users` 标题/副标题；`AppSidebar.vue` 的 `nav` 增加「用户」入口；`src/ui/icons/paths.ts` 增加 `users` 图标（取自 `@tabler/icons-vue`）。

契约改动后按既定流程重生成 `openapi.yaml` 与 `dashboard/src/openapi-types.d.ts`。

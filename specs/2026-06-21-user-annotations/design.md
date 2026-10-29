# 设计：用户级 Annotations

## 概述

为 `app_user` 增加 `annotations` 列，使其成为注解（annotation）合并链中的**最低优先级层**，并把用户对象暴露给 jsx 脚本上下文 `ctx.user`。

注解系统现状（见 `pkg/annotations/`、`pkg/server/annotations.go`）：

- `annotations.Decode([]byte) map[string]string` 解析 JSONB；`annotations.Merge(layers...)` 后者覆盖前者。
- 当前合并顺序：`model < provider < entry < apiKey`。
- 合并由 `candidateAnnotationsBuilder` 完成：请求级固定 `modelAnno` + `apiKeyAnno`，每个候选再叠加 `(provider, entry)`。
- 同样的链在 `gateway_flow.go` 三处用于回填 `ctx.annotations`（PatchContext）：`annotations.Merge(f.model.Annotations, f.auth.APIKeyAnno)`。

本设计在 entry 与 apiKey 之间插入 `userAnno`，新顺序：

```
model < provider < entry < user < apiKey
```

## 数据层

### 迁移 `db/migrations/039_user_annotations.sql`

```sql
-- +goose Up
ALTER TABLE app_user ADD COLUMN annotations JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE app_user DROP COLUMN annotations;
```

与 `provider` / `model` / `api_key` 的 annotations 列一致：`NOT NULL DEFAULT '{}'::jsonb`，对已有行（如 single-user-mode root）自动填空对象。

### sqlc 查询（`db/queries/user.sql`）

- `GetUserByID` / `GetUserByIdentity` / `ListUsers` 均为 `SELECT *`，重新生成后 `db.AppUser` 自动带上 `Annotations []byte`，无需改语句。
- `UpdateUser` 增加 `annotations` 赋值列。
- `InsertUser` 增加 `annotations` 写入（创建时即可设置）。
- 其余依赖默认值（`auth` 自动创建/bootstrap 路径不受影响）。

## 管理 API（admin 组）

`UserView` 增加 `Annotations map[string]string`，`UserMutateBody` 增加 `Annotations map[string]string`。`ToUserView` 解码 JSONB；`handleCreateUser` / `handleUpdateUser` 编码并写入。遵循仓库既有 `model` / `provider` 的转换写法。空/缺失输入解码为空 map（不为 nil）。

## 网关与 jsx 上下文

### 复用已查询的 user 行

`authenticateClient`（`gateway_helpers.go`）已为「禁用检查」执行 `GetUserByID`，但只回传 `*db.ApiKey`。改为同时回传该 `*db.AppUser`，避免二次查询。

### `gatewayAuthState` 新增字段

```go
UserJS   *jsx.UserSummary    // 暴露给 jsx
UserAnno map[string]string   // 注解最低优先层
```

在 `authenticateAndBackfill` 中由 user 行构建：`UserAnno` 解码 `user.Annotations`，`UserJS` 填 `id/name/annotations/isAdmin`。

### jsx 类型与上下文

- `pkg/jsx/types.go`：新增 `UserSummary{ ID int64; Name string; Annotations map[string]string; IsAdmin bool }`，`ContextPatch` 新增 `User *UserSummary`。
- `pkg/jsx/session.go` 的 `ctxInit`：`globalThis.ctx` 增加 `user: null`。
- `gateway_flow.go` 首次 `PatchContext` 时带上 `User: f.auth.UserJS`（与 `ApiKey` 同处设置；user 在请求生命周期内不变，只需设置一次）。

凭据信息一律不跨 JS 边界（与 `ApiKeySummary` 一致）。

### 合并链改造

- `candidateAnnotationsBuilder` 增加 `userAnno` 字段；`newCandidateAnnotationsBuilder` 增加 `userAnno` 参数；`merge` 改为 `annotations.Merge(b.modelAnno, provider, entryAnno, b.userAnno, b.apiKeyAnno)`。
- `buildPathCandidateSet` / `buildUnifiedCandidateSet` 增加 `userAnno` 参数并传入 builder；调用点（`handle_gateway.go`、`handle_unified_gateway.go` 的 `ResolveCandidates` 闭包）传 `auth.UserAnno`。
- `gateway_flow.go` 三处 `ctx.annotations` 合并改为 `annotations.Merge(f.model.Annotations, f.auth.UserAnno, f.auth.APIKeyAnno)`（这三处尚无 provider/entry 层，user 位于 model 之后、apiKey 之前）。

## 仪表盘

`UserForm.vue` 复用 `AnnotationsEditor.vue`（与 `ModelForm` / `ApiKeyForm` 一致），把 annotations 纳入创建/更新提交体。类型由 `generate-openapi` 自动产出。

## 不做的事

- 不提供任何兼容垫片或旧路径分支。
- 不放宽输入校验。
- 不修改 `/me`（`MeView`）。

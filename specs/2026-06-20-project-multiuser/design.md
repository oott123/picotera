# 设计

## 总览

照搬 `api_key` 的资源归属模式（migration 035 + `db/queries/api_key.sql` + `handle_api_key.go`）给 `project` 加 `user_id`：每个 query 带 `user_id` 过滤，每个 handler 通过 `requireUser(ctx)` 取当前用户并传入 `u.ID`。同时把设置体系拆分为：标题走环境变量、每用户偏好走新的 `user_setting` 表，移除 `global_setting`。

不引入任何第三方库，不保留兼容层。`global_setting` 整体删除而非保留空壳。

## 数据库

### 项目用户归属（migration 037）

```sql
ALTER TABLE project ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE project ALTER COLUMN user_id DROP DEFAULT;

-- name 由全局唯一改为用户内唯一
ALTER TABLE project DROP CONSTRAINT project_name_key;
ALTER TABLE project ADD CONSTRAINT project_user_id_name_key UNIQUE (user_id, name);

CREATE INDEX project_user_id_idx ON project (user_id);
```

`DEFAULT 1` 完成存量回填后立即 `DROP DEFAULT`，与 migration 035 一致——后续插入必须显式提供 `user_id`，符合"fail fast，不容忍缺省"。

### 每用户设置 + 移除全局设置（migration 038）

```sql
CREATE TABLE user_setting (
  user_id    BIGINT NOT NULL,
  key        TEXT NOT NULL,
  value      JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);

DROP TABLE global_setting;
```

`user_setting` 不迁移 `global_setting` 的旧数据：`app.title` 转为环境变量，`project.autoCreate` 默认关闭（未设置即视为 false），由各用户自行开启。

## 查询层（`db/queries/`）

### `project.sql`（全部 query 增加 `user_id` 作用域）

- `ListProjects(user_id)` — `WHERE user_id = $1 ORDER BY name ASC`
- `GetProject(id, user_id)` — `WHERE id = $1 AND user_id = $2`
- `GetProjectByName(user_id, name)` — `WHERE user_id = $1 AND name = $2`
- `InsertProject(user_id, name, paths)`、`InsertAutoCreatedProject(user_id, name, paths)` — 携带 `user_id`
- `UpdateProject(id, user_id, name, paths)` — `WHERE id = $1 AND user_id = $2`
- `DeleteProject(id, user_id)` — `WHERE id = $1 AND user_id = $2`
- `MatchProjectByPaths(user_id, candidate_paths)` — 增加 `AND p.user_id = @user_id`
- `MergeProjectUpdateTarget` / `MergeProjectReassignRequests` — target 与 source 均由 handler 先以 `GetProject(id, user_id)` 校验归属，merge query 维持现状（按 project_id 操作，已隐含用户内）

### `user_setting.sql`（新增，镜像原 `global_setting.sql`）

- `ListUserSettings(user_id) :many`
- `GetUserSetting(user_id, key) :one`
- `UpsertUserSetting(user_id, key, value) :one`
- `DeleteUserSetting(user_id, key) :execrows`

删除 `db/queries/global_setting.sql`。

### `request.sql`

`UpdateRequestOnHeader` 增加 `project_id` 字段写入（与现有 `user_id` 一同设置）：

```sql
UPDATE request
SET provider_id = $2, model = $3, upstream_model = $4, endpoint_path = $5, api_key_id = $6, status = $7,
    user_id = sqlc.narg('user_id')::bigint,
    project_id = sqlc.narg('project_id')::int
WHERE id = $1 AND created_at = sqlc.arg('created_at')::timestamp;
```

## 网关项目识别时序

`pkg/server/gateway_flow.go`：

- `insertMetaRequest()`：移除 `extractProjectID` 调用，meta 行以 `project_id = NULL` 插入。
- `authenticateAndBackfill()`：解析出 `apiKey.UserID` 后调用 `extractProjectID(ctx, body, userID)`，将结果赋给 `f.meta.ProjectID`；`UpdateRequestOnHeader` 同时写入 `user_id` 与 `project_id`；异步 `upsertProjectSeen` 移至此处。
- attempt 行（`gateway_flow_attempts.go:241`）继续读取 `f.meta.ProjectID`，因其在 `runAttempts`（认证之后）执行，取值正确。

`pkg/server/gateway_helpers.go`：`extractProjectID(ctx, body []byte, userID int64) pgtype.Int4`。

`pkg/server/project_extractor.go`：

- `Extract(ctx, body, userID int64) (int32, bool, error)` — `MatchProjectByPaths` 传入 `userID`。
- 自动创建开关由全局 `project.autoCreate` 改为读取该用户的 `user_setting`（`GetUserSetting(userID, "project.autoCreate")`，缺失视为 false）。
- `InsertAutoCreatedProject` 携带 `userID`；名称冲突重试遵循新的 `(user_id, name)` 唯一约束。

unified 网关（`handle_unified_gateway.go`）复用同一 `gatewayFlow.run()`，自动覆盖。

## 配置与标题

`pkg/configx/configx.go`：`Config` 增加 `AppTitle string \`mapstructure:"app_title"\``，`viper.SetDefault("app_title", "PicoTera")`。环境变量 `PICOTERA_APP_TITLE` 由现有 `bindEnvs()` 自动绑定。

新增 `GET /api/picotera/config`（mgmt 组，所有认证用户）返回 `{ "title": <AppTitle> }`，handler 直接读 `s.config.AppTitle`。

## 权限调整

- 项目 CRUD（List/Get/Upsert/Delete/Merge）operation 从 `admin` 组移至 `mgmt` 组，全部经 `requireUser` 作用域到当前用户。
- `ListProjectLabels`（已在 mgmt 组）改为用户作用域查询。
- 每用户设置 CRUD 与 config 端点注册在 `mgmt` 组。

## 前端

- 重新生成 `openapi.yaml` 与 `openapi-types.d.ts`。
- `api/client.ts`：删除 global settings 相关函数，新增 user-setting CRUD 与 `getConfig()`。
- `useAppTitle.ts`：改为读取 `GET /config` 的 `title`。
- `SettingsView.vue`：移除标题编辑（标题现由环境变量决定，不在页面可改）；"允许自动创建项目"开关改用 per-user user-setting API（key `project.autoCreate`）。
- 项目管理页面从管理员区移至普通用户可访问区（路由与侧边栏导航的 admin 门控调整）。

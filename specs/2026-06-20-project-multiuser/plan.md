# 执行计划

## 1. 数据库迁移

1. `db/migrations/037_project_user_ownership.sql`：
   - `ALTER TABLE project ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1;` 然后 `ALTER COLUMN user_id DROP DEFAULT;`
   - `ALTER TABLE project DROP CONSTRAINT project_name_key;`
   - `ALTER TABLE project ADD CONSTRAINT project_user_id_name_key UNIQUE (user_id, name);`
   - `CREATE INDEX project_user_id_idx ON project (user_id);`
2. `db/migrations/038_user_setting.sql`：
   - 建 `user_setting (user_id, key, value, updated_at, PRIMARY KEY(user_id, key))`。
   - `DROP TABLE global_setting;`

## 2. 查询层

3. 改 `db/queries/project.sql`：为 `ListProjects`、`GetProject`、`GetProjectByName`、`InsertProject`、`InsertAutoCreatedProject`、`UpdateProject`、`DeleteProject`、`MatchProjectByPaths` 增加 `user_id`（见 design.md）。
4. 新建 `db/queries/user_setting.sql`：`ListUserSettings`、`GetUserSetting`、`UpsertUserSetting`、`DeleteUserSetting`。
5. 删除 `db/queries/global_setting.sql`。
6. 改 `db/queries/request.sql` 的 `UpdateRequestOnHeader`：增加 `project_id` 写入（narg）。
7. 运行 `sqlc generate`，刷新 `pkg/db/`。

## 3. 后端 contract

8. 改 `pkg/contract/project.go`：operation 定义不变（路由/类型不动），仅供 server 改注册到 mgmt 组。
9. 新建 `pkg/contract/user_setting.go`：`UserSettingView`、Upsert 请求体、四个 operation（List/Get/Upsert/Delete，路径 `/settings`）。
10. 新建 `pkg/contract/config.go`：`ConfigView{ Title string }`、`OperationGetConfig`（GET `/config`）。
11. 删除 `pkg/contract/global_setting.go`。

## 4. 后端 handler 与配置

12. 改 `pkg/configx/configx.go`：`Config` 加 `AppTitle string \`mapstructure:"app_title"\``，`viper.SetDefault("app_title", "PicoTera")`。
13. 改 `pkg/server/handle_project.go`：每个 handler 调 `requireUser(ctx)`，将 `u.ID` 传入对应 query；merge handler 用 `GetProject(id, u.ID)` 校验 source/target 归属。
14. 新建 `pkg/server/handle_user_setting.go`：镜像原 `handle_global_setting.go`，每个 handler 经 `requireUser` 注入 `user_id`；校验 key/value 非空。
15. 新建 `pkg/server/handle_config.go`：返回 `s.config.AppTitle`。
16. 删除 `pkg/server/handle_global_setting.go`。
17. 改 `pkg/server/server.go` `register()`：项目五个 operation 从 `admin` 移到 `mgmt`；注册 user-setting 四个 operation 与 `getConfig` 到 `mgmt`；移除四个 global-setting 注册。

## 5. 网关项目识别时序

18. 改 `pkg/server/project_extractor.go`：
    - `Extract(ctx, body, userID int64)`，`MatchProjectByPaths` 传 `userID`。
    - 自动创建开关改读 `GetUserSetting(userID, "project.autoCreate")`（缺失/解析失败视为 false）；移除常量 `autoCreateSettingKey` 对 global_setting 的依赖。
    - `InsertAutoCreatedProject` 携带 `userID`。
19. 改 `pkg/server/gateway_helpers.go`：`extractProjectID(ctx, body []byte, userID int64) pgtype.Int4`。
20. 改 `pkg/server/gateway_flow.go`：
    - `insertMetaRequest()` 移除 `extractProjectID`，meta 行 `ProjectID` 插入为 `{Valid:false}`。
    - `authenticateAndBackfill()` 在拿到 `f.auth.UserID` 后调用 `extractProjectID(..., apiKey.UserID)`，赋给 `f.meta.ProjectID`；`UpdateRequestOnHeader` 增传 `project_id`；异步 `upsertProjectSeen` 搬到此处（仅当 `ProjectID.Valid`）。

## 6. OpenAPI 与前端

21. `mise run openapi` 重生成 `openapi.yaml`。
22. `pnpm --dir dashboard generate-openapi` 重生成 TS 类型。
23. 改 `dashboard/src/api/client.ts`：删 global-settings 函数；加 user-setting CRUD 与 `getConfig()`；更新 `queryKeys.ts`。
24. 改 `dashboard/src/composables/useAppTitle.ts`：改为读取 `getConfig()` 的 `title`（默认 `PicoTera`），仍同步 `document.title`。
25. 改 `dashboard/src/views/SettingsView.vue`：移除标题编辑控件；"允许自动创建项目"开关改用 per-user user-setting API（key `project.autoCreate`）。
26. 调整项目管理页面的访问门控：将项目管理路由/侧边栏导航从管理员专属区移至普通用户可见区（定位 `2026-06-20-admin-user-function-split` 引入的 admin 门控并放开 projects）。

## 7. 验证

27. `go build ./...` 通过。
28. `pnpm --dir dashboard build`、`pnpm --dir dashboard lint`、`type-check` 通过。
29. 全文检索确认 `global_setting` / `app.title` global-setting 路径无残留引用。
30. 手测：两个用户各自只能看到/管理自己的项目；网关请求只匹配本用户项目；各自的 auto-create 开关独立生效；标题随 `PICOTERA_APP_TITLE` 显示。

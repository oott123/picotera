# 项目多用户功能

将"项目(project)"从全局共享资源改造为按用户归属的资源，并把设置体系也用户化。

## 原始需求

1. 为项目增加用户属性。迁移时，原有项目全部归入 `user_id = 1` 的用户。
2. 将项目管理从管理员功能调整为普通用户功能，允许每个用户自由管理自己的项目。
3. 在对请求识别项目时，只查找发起请求的用户名下的项目。
4. 将设置也添加用户属性，使得每个用户可以设置自己的"允许自动创建项目(auto-create project)"。
5. 原有的"应用标题(app title)"设置改为通过环境变量进行配置。

## 澄清与补充决定

- **每用户设置的存储**：新建 `user_setting` 键值表（按 `user_id` 作用域，结构镜像原 `global_setting`）。`global_setting` 表/queries/contract/handler 及前端 client 全部移除——其原有的两项配置（`app.title`、`project.autoCreate`）分别迁往环境变量与 `user_setting`，迁完后无任何消费方。
- **应用标题下发方式**：新增环境变量 `PICOTERA_APP_TITLE`（默认 `PicoTera`）。dashboard 通过一个**需认证**的 `GET /api/picotera/config` 端点读取标题，不新增公开未认证路由。
- **项目识别时序调整**：网关流程中项目识别目前发生在认证之前（meta 行插入时无 `user_id`）。改造后项目识别移至认证之后执行（此时已从 API key 解析出 `user_id`），`project_id` 改在 `UpdateRequestOnHeader` 阶段连同 `user_id` 一并写入 meta 行；attempt 行继续从 `f.meta.ProjectID` 取值，无需额外改动。
- **项目名唯一性**：由全局唯一 `(name)` 改为用户内唯一 `(user_id, name)`。

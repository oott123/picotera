# API 设计

所有路由位于 `/api/picotera` 前缀，注册在 `mgmt` 组（需认证，所有用户均可访问），按当前用户作用域。

## 项目管理（权限由 admin 下放至 mgmt）

操作签名与请求/响应体不变，仅 handler 内部增加按当前用户的归属过滤；越权访问他人项目返回 404。

| Operation | Method | Path | 说明 |
|---|---|---|---|
| `listProjects` | GET | `/projects` | 列出当前用户的项目 |
| `getProject` | GET | `/projects/{id}` | 获取当前用户的某项目（非本人项目 → 404） |
| `upsertProject` | PUT | `/projects` | 创建/更新当前用户的项目，名称在用户内唯一（冲突 → 409） |
| `deleteProject` | POST | `/projects/delete` | 删除当前用户的项目 |
| `mergeProject` | POST | `/projects/merge` | 合并当前用户名下两个项目（source、target 均须属当前用户） |
| `listProjectLabels` | GET | （现有路径） | 当前用户项目的 `{id,name}` 列表，供 overview 过滤 |

`ProjectView` 字段保持不变（不对外暴露 `userId`）。

## 每用户设置 user-setting（替代 global-setting）

镜像原 global-setting 接口，作用域为当前用户。

| Operation | Method | Path | 说明 |
|---|---|---|---|
| `listUserSettings` | GET | `/settings` | 列出当前用户的所有设置 |
| `getUserSetting` | GET | `/settings/{key}` | 读取某项设置（不存在 → 404） |
| `upsertUserSetting` | PUT | `/settings` | 写入设置，body `{ key, value }`（`value` 为任意 JSON） |
| `deleteUserSetting` | DELETE | `/settings/{key}` | 删除设置（不存在 → 404） |

已知 key：`project.autoCreate`（JSON 布尔，控制本用户的项目自动创建）。

## 应用配置 config（新增）

| Operation | Method | Path | 说明 |
|---|---|---|---|
| `getConfig` | GET | `/config` | 返回运行期应用配置，需认证 |

响应体：

```json
{ "title": "PicoTera" }
```

`title` 来源于环境变量 `PICOTERA_APP_TITLE`（默认 `PicoTera`）。

## 移除

`listGlobalSettings` / `getGlobalSetting` / `upsertGlobalSetting` / `deleteGlobalSetting` 四个 operation 及其 contract、handler、路由注册全部删除。

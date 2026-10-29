# 用户级 Annotations

## 原始需求

给用户（user）也加上 annotation。给 jsx 的 Context 也加上 `user`，并且能看到 `user.id`、`user.name`、`user.annotations`、`user.isAdmin` 之类的属性。在合并 annotation 时，也合并 user 的 annotations，优先级比 apiKey 低、但比 entry 高。

## 澄清与补充

- **合并优先级**：user annotations 位于 entry 与 apiKey 之间。最终合并顺序为 `model < provider < entry(provider model) < user < apiKey`（后者覆盖前者）。
- **jsx `ctx.user` 字段**：暴露 `id`、`name`（取自 `display_name`）、`annotations`、`isAdmin`。不暴露凭据类信息。
- **管理 API**：`app_user` 新增 `annotations` JSONB 列，并通过用户管理（admin）接口的 `UserView` / `UserMutateBody` 读写。仪表盘的用户表单复用现有 `AnnotationsEditor.vue`。
- 范围不包含 `/me` 接口的 annotations 暴露。

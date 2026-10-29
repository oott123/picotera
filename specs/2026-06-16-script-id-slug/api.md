# API 变更

## ScriptMutateBody

新增 `id` 字段（创建与编辑共用）：

```jsonc
{
  "id": "reverse-providers", // 可选；创建时留空则服务端生成 xid。编辑时为目标 ID，必填且合法
  "name": "Reverse providers",
  "source": "...",
  "enabled": true
}
```

## POST /scripts （createScript）

- `body.id` 为空：服务端生成随机 xid。
- `body.id` 非空：必须匹配 `^[a-zA-Z0-9_-]+$`（长度 1–64），否则 `400`。
- `body.id` 与已有脚本冲突：`409`。
- 成功返回 `ScriptView`（含最终 `id`）。

## PUT /scripts/{id} （updateScript）

- path 的 `{id}` 为待修改脚本的旧 ID。
- `body.id` 为目标 ID（可与旧 ID 相同），必填且合法，否则 `400`。
- 旧 ID 不存在：`404`。
- 目标 ID 已被其他脚本占用：`409`。
- 成功返回更新后的 `ScriptView`（`id` 为新值）。

## 错误码

| 场景 | 状态码 |
| --- | --- |
| ID 格式非法 / 编辑时 ID 为空 | 400 |
| 脚本源码语法错误 | 400 |
| 编辑时旧 ID 不存在 | 404 |
| ID 冲突（创建或改 ID 撞已有脚本） | 409 |

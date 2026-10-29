# API 设计

## 管理 API（admin 组，前缀 `/api/picotera`）

无新增 operation，仅在既有用户 CRUD 的 view / body 上增加字段。

### `UserView`（响应）

```jsonc
{
  "id": 1,
  "displayName": "root",
  "isAdmin": true,
  "disabled": false,
  "annotations": { "team": "infra" },   // 新增
  "createdAt": "2026-06-21T00:00:00Z",
  "updatedAt": "2026-06-21T00:00:00Z"
}
```

涉及：`GET /users`、`GET /users/{id}`、`POST /users`、`PUT /users/{id}` 的响应体。

### `UserMutateBody`（请求体，`POST /users` 与 `PUT /users/{id}`）

```jsonc
{
  "displayName": "alice",
  "isAdmin": false,
  "disabled": false,
  "annotations": { "team": "infra" }     // 新增；缺失视为 {}
}
```

`displayName` 仍为必填且不做宽松归一化。`annotations` 值为字符串映射；非字符串值按既有 `annotations.Decode` 规则强制转换为字符串。

## jsx 脚本上下文

`globalThis.ctx.user`（请求生命周期内只读、不变）：

```jsonc
ctx.user = {
  "id": 1,
  "name": "root",          // 取自 app_user.display_name
  "annotations": { "team": "infra" },
  "isAdmin": true
}
```

`ctx.annotations` 的合并优先级（后者覆盖前者）：

```
model < provider < entry(provider model) < user < apiKey
```

`ctx.user` 在所有 hook（`sortProviders` / `rewriteModel` / `beforeRequest` / `rewriteRequest` / `rewriteProviderModels` / `afterUpstreamError`）中可见。

# API 设计

所有操作位于 `/api/picotera` 前缀下，经既有用户鉴权中间件保护。

## 视图类型

```
UserView {
  id: int64
  displayName: string
  isAdmin: bool
  disabled: bool
  createdAt: string   // RFC3339
  updatedAt: string   // RFC3339
}

UserIdentityView {
  id: int64
  userId: int64
  provider: string
  identity: string
  createdAt: string   // RFC3339
}
```

## 用户

| 操作 | 方法 | 路径 | 请求体 | 响应 |
|------|------|------|--------|------|
| listUsers | GET | `/users` | — | `UserView[]` |
| getUser | GET | `/users/{id}` | — | `UserView` |
| createUser | POST | `/users` | `{ displayName, isAdmin?, disabled? }` | `UserView` |
| updateUser | PUT | `/users/{id}` | `{ displayName, isAdmin, disabled }` | `UserView` |
| deleteUser | POST | `/users/delete` | `{ id }` | 204 |

- `createUser`：`displayName` 必填（空字符串拒绝，返回 400）。`isAdmin` / `disabled` 缺省 false。
- `deleteUser`：在事务内级联删除该用户的全部 `user_identity` 后删除用户；用户不存在直接返回成功（幂等删除，与既有删除处理器一致）。

## 身份（按用户嵌套）

| 操作 | 方法 | 路径 | 请求体 | 响应 |
|------|------|------|--------|------|
| listUserIdentities | GET | `/users/{userId}/identities` | — | `UserIdentityView[]` |
| createUserIdentity | POST | `/users/{userId}/identities` | `{ provider, identity }` | `UserIdentityView` |
| updateUserIdentity | PUT | `/users/{userId}/identities/{id}` | `{ provider, identity }` | `UserIdentityView` |
| deleteUserIdentity | POST | `/users/{userId}/identities/delete` | `{ id }` | 204 |

- `createUserIdentity`：`provider`、`identity` 均必填且非空，否则 400。先校验 `userId` 用户存在（不存在 404）。`(provider, identity)` 已存在时返回 409。
- `updateUserIdentity` / `deleteUserIdentity`：先校验路径中的 `id` 对应身份存在且 `user_id == userId`（不满足返回 404）。`updateUserIdentity` 改写后若与其他记录的 `(provider, identity)` 冲突返回 409。

## 错误约定

- 400：必填字段为空。
- 404：目标用户 / 身份不存在，或身份不归属指定用户。
- 409：`(provider, identity)` 唯一约束冲突。
- 500：其余数据库错误。

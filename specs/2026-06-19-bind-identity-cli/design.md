# 设计

## 概述

在 `cmd/picotera/main.go` 中新增一个顶层 cobra 子命令 `bind-identity`，复用现有 `set-admin` 的形态：解析配置、连接 `pgxpool`、通过 `db.Queries` 操作数据库、用 `log.Fatalf` / `os.Exit(1)` 处理失败。命令直接写 `user_identity` 表，把 `(provider, identity)` 映射到指定 `user-id`，复用 `pkg/auth` 已有的身份模型（`app_user` + `user_identity`，唯一约束 `(provider, identity)`）。

不引入新的 HTTP API、不改动 `pkg/auth` 解析逻辑、不引入第三方库。

## CLI 接口

```
picotera bind-identity <provider> <identity> <user-id> [--force]
```

- 参数顺序：`provider`（身份提供商名，如 `http-header`、`single-user-mode`）、`identity`（该提供商下的唯一识别字段）、`user-id`（自增数字用户 ID）。
- `cobra.ExactArgs(3)` 限定三个位置参数；`--force`（`-f`）为布尔标志。
- 输入严格校验：`user-id` 必须能解析为 `int64`，否则报错退出；`provider`、`identity` 原样使用，不做 trim/大小写折叠等宽松处理。

## 执行流程

1. 解析 `user-id` 为 `int64`，失败则 `log.Fatalf`。
2. `configx.Parse()` + `pgxpool.New` 建连，`defer pool.Close()`。
3. `GetUserByID(user-id)`：`pgx.ErrNoRows` → 打印 "user N not found" 并 `os.Exit(1)`；其他错误 `log.Fatalf`。
4. `GetUserIdentity(provider, identity)` 查询当前绑定：
   - **存在且 `user_id == 目标`**：幂等成功，打印已绑定并退出 0。
   - **存在且 `user_id != 目标`**：
     - 无 `--force` → 报错（提示当前所属 user id）并 `os.Exit(1)`；
     - 有 `--force` → `UpdateUserIdentityUser(provider, identity, user-id)` 改绑，打印 "rebound"。
   - **`pgx.ErrNoRows`**：`InsertUserIdentity(user-id, provider, identity)` 新建绑定，打印 "bound"。
   - **其他错误**：`log.Fatalf`。

## 数据层

`db/queries/user.sql` 新增两条 sqlc 查询，随后 `sqlc generate` 重新生成 `pkg/db/`：

- `GetUserIdentity :one` — 按 `(provider, identity)` 读取当前映射，用于判断是否冲突及当前所属用户。
- `UpdateUserIdentityUser :one` — `UPDATE user_identity SET user_id = $3 WHERE provider = $1 AND identity = $2 RETURNING *`，用于 `--force` 改绑。

新建绑定复用已有的 `InsertUserIdentity`（其 `ON CONFLICT DO NOTHING` 在本流程里不会触发——插入前已确认无冲突行）。

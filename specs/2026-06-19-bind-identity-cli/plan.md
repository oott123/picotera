# 执行计划

## 1. 新增 sqlc 查询

编辑 `db/queries/user.sql`，追加：

```sql
-- name: GetUserIdentity :one
SELECT * FROM user_identity WHERE provider = $1 AND identity = $2 LIMIT 1;

-- name: UpdateUserIdentityUser :one
UPDATE user_identity SET user_id = $3 WHERE provider = $1 AND identity = $2 RETURNING *;
```

运行 `sqlc generate`，确认 `pkg/db/` 中生成 `GetUserIdentity`、`UpdateUserIdentityUser` 及对应的 `*Params` 类型，并出现在 `pkg/db/querier.go` 的 `Querier` 接口里。

## 2. 新增 `bind-identity` 子命令

在 `cmd/picotera/main.go` 中，紧接 `set-admin` 之后注册新子命令：

- `Use: "bind-identity <provider> <identity> <user-id>"`，`Short` 描述。
- `Args: cobra.ExactArgs(3)`。
- 注册布尔标志 `--force` / `-f`，默认 `false`。
- `Run`：
  1. `strconv.ParseInt(args[2], 10, 64)` 解析 user-id，失败 `log.Fatalf`。
  2. `configx.Parse()`；`pgxpool.New(ctx, config.DatabaseURL)`，`defer pool.Close()`；`q := db.New(pool)`。
  3. `q.GetUserByID(ctx, id)`：`errors.Is(err, pgx.ErrNoRows)` → 打印 "user %d not found" 并 `os.Exit(1)`；其他错误 `log.Fatalf`。
  4. `q.GetUserIdentity(ctx, {Provider: args[0], Identity: args[1]})`：
     - `err == nil`：
       - `existing.UserID == id` → 打印已绑定，返回。
       - `existing.UserID != id` 且未 `--force` → 打印 "(provider, identity) already bound to user %d; pass --force to rebind" 并 `os.Exit(1)`。
       - `existing.UserID != id` 且 `--force` → `q.UpdateUserIdentityUser(ctx, {Provider, Identity, UserID: id})`，打印 rebound（从旧 user 到新 user）。
     - `errors.Is(err, pgx.ErrNoRows)` → `q.InsertUserIdentity(ctx, {UserID: id, Provider, Identity})`，打印 bound。
     - 其他错误 → `log.Fatalf`。

读取 `--force` 的方式：在 `Run` 闭包外用 `cmd.Flags().GetBool("force")`，或将命令对象提出为变量后 `cmd.Flags().BoolP(...)` 再在 `Run` 内 `cmd.Flags().GetBool`。

## 3. 构建校验

`go build ./cmd/picotera` 通过；手动验证（可选，需本地数据库）：

- `picotera bind-identity http-header alice 1` → 新建绑定。
- 重复执行同一条 → 幂等成功。
- `picotera bind-identity http-header alice 2`（无 force）→ 报错退出。
- `picotera bind-identity http-header alice 2 --force` → 改绑成功。
- 对不存在的 user-id → 报错退出。

## 涉及文件

- `db/queries/user.sql` — 新增两条查询。
- `pkg/db/*`（`models`/`queries`/`querier`）— 由 `sqlc generate` 重新生成，不手改。
- `cmd/picotera/main.go` — 新增 `bind-identity` 子命令。

# 执行计划

设计见 `design.md`，接口见 `api.md`。

## 1. 数据库

1. 新建 `db/migrations/054_kv_entry.sql`（内容见 `api.md`）。
2. 新建 `db/queries/kv.sql`（内容见 `api.md`），执行 `sqlc generate`。

## 2. `pkg/kv`

1. `store.go`：新增 `ErrInvalidArgument`。
2. 新建 `glob.go`：实现 `GlobToRegexp`，规则见 `design.md`「Glob → 正则」。
3. 新建 `glob_test.go`：表驱动测试，覆盖 `*`、`?`、`\c` 转义、标点字面量（`.`、`+`、`(`、`|`、`$`、`{`）、非 ASCII 字面量、`[abc]`、`[^a]`、`[a-z]`、类首 / 类尾的 `-`、类内 `\]`，以及四种错误情况。每个用例都用 `regexp.MustCompile` 编译输出，并断言若干 key 的匹配结果。
4. 新建 `database.go`：`Queries`、`DatabaseStore`、`NewDatabaseStore`，包括 TTL 计算、`ScanEntries` 的分页逻辑、每分钟一次的清理 goroutine（日志用 `logx`）。
5. `kv.go`：新增 `WithDatabase`；`New` 增加 `database` 分支，没有传 `WithDatabase` 时返回错误；更新 `New` 的注释。`memory.go` 不改动。

## 3. 装配与调用方

1. `pkg/configx/configx.go`：`kv.driver` 默认值改为 `database`。
2. `pkg/server/server.go`：`kv.New` 增加 `kv.WithDatabase(queries)`；`Server.Close` 在 `s.db.Close()` 之前调用 `s.kvStore.Close()`，错误并入 `errs`。
3. `pkg/server/handle_kv.go`：按 `design.md`「管理 API 的错误映射」处理 `kv.ErrInvalidArgument`，`ErrKeyNotFound` 改用 `errors.Is`。
4. `pkg/jsx/helpers.go`：`err == kv.ErrKeyNotFound` 改为 `errors.Is`。
5. `go build ./... && go test ./pkg/kv/... ./pkg/jsx/... ./pkg/server/...`。

## 4. 对真实数据库验证

仓库没有 postgres 测试框架，这一步手动执行（`docker compose up -d` 后）：

1. `PICOTERA_KV_DRIVER` 不设置，启动 `mise run server`，确认 migration 054 已执行、服务正常启动。
2. 用管理 API 验证：
   - `PUT /kv/a.b` 不带 TTL，再 `GET` 得到 `ttl = -1`；
   - `PUT /kv/c` 带 `ttlSeconds: 2`，`GET` 得到 `ttl` 为 1 或 2，3 秒后 `GET` 返回 404；
   - 写入约 250 个 key，`GET /kv?pattern=*` 逐页翻完，确认每页 100 条、无重复、最后一页 `hasMore = false`；
   - `pattern` 分别为 `a.*`（只匹配 `a.b`，不匹配 `axb`）、`[a-c]*`、`[^a]*`、`a\.b` 时结果正确；`pattern` 为 `[abc`、`[]`、`[z-a]`、`a\` 时返回 400；
   - `PUT /kv/d` 带 `ttlSeconds: 0`，`GET` 得到 `ttl = -1`。
3. 写一个调用 `picotera.kv.setex` / `get` / `ttl` / `del` 的脚本，通过网关发一个请求，确认脚本行为与之前一致；`setex(k, 0, v)` 写入后 `ttl(k)` 为 `-1`。
4. 设置 `PICOTERA_KV_DRIVER=memory` 启动，确认服务正常启动、KV 的读写走内存（`kv_entry` 表中没有新行）。设置 `PICOTERA_KV_DRIVER=foo` 启动，确认启动失败并报 `kv: unknown driver "foo"`。
5. 等待一分钟以上，确认已过期的行被清理任务从 `kv_entry` 删除。

## 5. 文档

按 `design.md`「文档」更新根目录 `CLAUDE.md` 和 `docs/scripting.md`。

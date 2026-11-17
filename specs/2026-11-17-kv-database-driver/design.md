# 设计：KV 数据库驱动

## 总体

`pkg/kv` 新增 `database` 驱动（`DatabaseStore`），与现有的 `memory`、`redis` 并列。它把条目存在 PostgreSQL 的 `kv_entry` 表里，经 sqlc 生成的查询访问，实现现有的 `kv.Store` 接口。`Store` 接口、`memory` 和 `redis` 两个驱动、脚本 SDK（`picotera.kv.*`）和管理 API（`/api/picotera/kv`）的形状都不变。

- `PICOTERA_KV_DRIVER` 默认值从 `memory` 改为 `database`，合法取值为 `database`、`memory`、`redis`。
- 单元测试继续使用 `kv.NewMemoryStore()`。

## 存储模型

表 `kv_entry`（migration 054）：

| 列 | 类型 | 说明 |
|---|---|---|
| `id` | `BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY UNIQUE` | 只用作分页游标 |
| `key` | `TEXT PRIMARY KEY` | 键 |
| `value` | `TEXT NOT NULL` | 值，原样存储（脚本侧已是 JSON 字符串） |
| `expires_at` | `TIMESTAMPTZ` | `NULL` 表示永不过期 |

另建部分索引 `kv_entry_expires_at_idx ON kv_entry (expires_at) WHERE expires_at IS NOT NULL`，供清理任务使用。

KV 是管理员级的全局资源（`/kv` 在 admin 组，脚本也不区分用户），表上不加 `user_id`。

## 时间来源

与本 schema 的约定一致，所有时间戳都由 Go 传入（`time.Now()`），SQL 中不使用 `now()`。多实例部署时各实例的时钟偏差会直接体现为 TTL 偏差，这一点不做补偿。

## 各操作的语义

`now` 均为调用时的 `time.Now()`。"存活"指 `expires_at IS NULL OR expires_at > now`。

- **Get**：查存活行；没有则返回 `ErrKeyNotFound`。过期但尚未清理的行视为不存在，读路径不删除它。
- **Set**：upsert，`expires_at = NULL`（与 Redis `SET` 一样清掉原有 TTL）。
- **SetEx**：`ttl <= 0` 时与 Set 相同（`expires_at = NULL`，永不过期），与 `memory` 驱动一致；否则 upsert，`expires_at = now + ttl`。
- **TTL**：查存活行。不存在返回 `-2`，`expires_at` 为 `NULL` 返回 `-1`，否则返回 `ceil((expires_at - now) 秒)`。
- **Del**：按 key 删除；key 不存在不报错。
- **ScanEntries**：见下文「Scan」。
- **Close**：停止清理 goroutine 并等待它退出。不关闭连接池，连接池归 `Server` 所有。

upsert 命中已有行时 `id` 保持不变，即使该行已过期。

### 非法文本

PostgreSQL 的 `TEXT` 不能存 NUL 字节，也不能存非法 UTF-8。`DatabaseStore` 不预先检查 key 和 value，原样交给数据库；数据库报错时作为普通错误返回（管理 API 为 500，脚本侧抛出 `Error`）。

## Scan

`ScanEntries(ctx, pattern, cursor, count)`：

`ErrInvalidArgument` 是 `pkg/kv` 新增的哨兵错误，具体错误用 `fmt.Errorf("...: %w", ErrInvalidArgument)` 包装。下列校验只属于 `database` 驱动，`memory` 和 `redis` 驱动的行为不变。

1. `count <= 0` 或 `count == math.MaxInt64`（`count + 1` 会溢出）→ `ErrInvalidArgument`。`cursor > math.MaxInt64` → `ErrInvalidArgument`。
2. 用 `kv.GlobToRegexp(pattern)` 把 Redis 风格的 glob 转为 PostgreSQL 正则；转换失败 → `ErrInvalidArgument`。
3. 查询 `id > cursor`、`key ~ regexp`、存活的行，`ORDER BY id LIMIT count + 1`。
4. 取到 `count + 1` 行时，只返回前 `count` 行，`NextCursor` 为第 `count` 行的 `id`；否则返回全部行，`NextCursor = 0`。

TTL 字段的计算方式与 `TTL` 方法相同。

游标是 `id`，所以扫描期间新插入的 key 会出现在后面的页里；已有 key 被覆盖时 `id` 不变，不会重复出现。这与 Redis `SCAN` 的保证同级。

### Glob → 正则

`GlobToRegexp` 是 `pkg/kv` 中的纯函数，实现 Redis `KEYS` / `SCAN` 的 glob 语法，输出一个 `^...$` 锚定的正则字符串，同时兼容 PostgreSQL ARE 和 Go RE2（单元测试用 RE2 编译输出来验证）。`memory` 驱动继续使用 `filepath.Match`，不改用这个函数。

| glob | 输出 |
|---|---|
| `*` | `.*` |
| `?` | `.` |
| `\c` | 字面量 `c` |
| `[...]` | 字符类，见下 |
| 其他字符 | 字面量 |

字面量输出规则：ASCII 非字母数字字符输出为 `\c`，其余字符（ASCII 字母数字、非 ASCII 字符）原样输出。这条规则在方括号内外都适用：ARE 和 RE2 都把"反斜杠 + 标点"解释为该标点本身，而反斜杠加字母数字会变成转义序列，所以字母数字不能加反斜杠。

字符类：

- `[` 之后紧跟的 `^` 表示取反，输出 `[^`。
- 类内的成员是字面量字符（可用 `\c` 转义，包括 `\]`）或区间 `a-b`。区间两端都是字面量字符，且 `a <= b`（按 rune 比较），否则报错。类首或类尾的 `-` 是字面量。
- 遇到未转义的 `]` 时字符类结束。

以下情况返回错误（调用方映射为 `ErrInvalidArgument`）：

- pattern 以单个 `\` 结尾；
- `[` 没有对应的 `]`；
- 字符类为空（`[]`、`[^]`）；
- 区间反向（如 `[z-a]`）。

与 Redis 的差异：`?` 和字符类按字符匹配，Redis 按字节匹配。对上述非法 pattern，Redis 会尽量宽松地匹配，这里直接拒绝。

## 过期清理

`NewDatabaseStore` 启动一个 goroutine，每分钟执行一次 `DELETE FROM kv_entry WHERE expires_at <= now`，每次执行设 30 秒超时，失败时记一条 warn 日志后等下一轮。多实例部署时每个实例各自清理，删除语句是幂等的。

读路径已经按 `expires_at` 过滤，清理任务只负责回收空间，不影响正确性。

## 装配

- `kv.New(driver, opts...)` 新增 `kv.WithDatabase(q kv.Queries)` 选项。`kv.Queries` 是 `pkg/kv` 中定义的窄接口，只包含 `kv_entry` 的五个 sqlc 方法，`*db.Queries` 天然满足它。`driver == "database"` 但没有传 `WithDatabase` 时，`New` 返回错误。
- `server.go` 调用 `kv.New(config.KV.Driver, kv.WithRedisURL(...), kv.WithDatabase(queries))`。
- `Server.Close` 目前没有关闭 kvStore。改为在关闭连接池之前调用 `kvStore.Close()`，确保清理 goroutine 先于连接池退出。

## 管理 API 的错误映射

`handleListKvEntries` 在 `ScanEntries` 返回的错误满足 `errors.Is(err, kv.ErrInvalidArgument)` 时返回 400，消息为该错误的文本（目前只有 `database` 驱动会返回这种错误）；其他错误维持现状。`kv.ErrKeyNotFound` 的判断改用 `errors.Is`。

脚本侧不需要改动：host 函数把 store 返回的错误原样交给 SDK，SDK 抛出 `Error`。

## 文档

- 根目录 `CLAUDE.md`：`pkg/kv/` 一条改为 database（PostgreSQL `kv_entry` 表，默认）、memory、Redis（KeyDB）三种驱动。"Database Schema" 一节的核心表列表加上 `kv_entry`。
- `docs/scripting.md` 的 `picotera.kv` 一节补一句：数据默认存在数据库里，可通过 `PICOTERA_KV_DRIVER=redis` 改用 Redis，或用 `PICOTERA_KV_DRIVER=memory` 存在进程内存中（重启即丢失，多实例之间不共享）。
- `README.md` 不改动。

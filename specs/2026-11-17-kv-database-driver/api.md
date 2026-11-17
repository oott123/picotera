# 接口：KV 数据库驱动

## 配置

| 环境变量 | 默认值 | 取值 |
|---|---|---|
| `PICOTERA_KV_DRIVER` | `database` | `database`、`memory` 或 `redis`，其他取值启动失败（`kv: unknown driver "<x>"`） |
| `PICOTERA_KV_REDIS_URL` | `localhost:6379` | 不变，仅 `redis` 驱动使用 |

## Migration `db/migrations/054_kv_entry.sql`

```sql
-- +goose Up
CREATE TABLE kv_entry (
  id         BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY UNIQUE,
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  expires_at TIMESTAMPTZ
);

CREATE INDEX kv_entry_expires_at_idx ON kv_entry (expires_at) WHERE expires_at IS NOT NULL;

-- +goose Down
DROP TABLE kv_entry;
```

## 查询 `db/queries/kv.sql`

```sql
-- name: GetKvEntry :one
SELECT value, expires_at FROM kv_entry
WHERE key = sqlc.arg(key) AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- name: UpsertKvEntry :exec
INSERT INTO kv_entry (key, value, expires_at) VALUES ($1, $2, $3)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at;

-- name: DeleteKvEntry :exec
DELETE FROM kv_entry WHERE key = $1;

-- name: ScanKvEntries :many
SELECT id, key, value, expires_at FROM kv_entry
WHERE id > sqlc.arg(cursor)
  AND key ~ sqlc.arg(pattern)::text
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now))
ORDER BY id
LIMIT sqlc.arg(row_limit)::bigint;

-- name: DeleteExpiredKvEntries :exec
DELETE FROM kv_entry WHERE expires_at <= $1;
```

每条查询上方按 `user_session.sql` 的风格写注释，说明时间由 Go 传入、`ScanKvEntries` 的 `row_limit` 由调用方传 `count + 1`。

## Go 接口 `pkg/kv`

```go
// store.go
var ErrInvalidArgument = errors.New("kv: invalid argument")

// kv.go
func WithDatabase(q Queries) Option
func New(driver string, opts ...Option) (Store, error) // driver: "database" | "memory" | "redis"

// database.go
type Queries interface {
	GetKvEntry(ctx context.Context, arg db.GetKvEntryParams) (db.GetKvEntryRow, error)
	UpsertKvEntry(ctx context.Context, arg db.UpsertKvEntryParams) error
	DeleteKvEntry(ctx context.Context, key string) error
	ScanKvEntries(ctx context.Context, arg db.ScanKvEntriesParams) ([]db.KvEntry, error)
	DeleteExpiredKvEntries(ctx context.Context, expiresAt pgtype.Timestamptz) error
}

type DatabaseStore struct { /* queries, cancel, done */ }

func NewDatabaseStore(q Queries) *DatabaseStore // 启动清理 goroutine

// glob.go
func GlobToRegexp(pattern string) (string, error)
```

上面的 `db.*Params` / `db.*Row` 名称以 `sqlc generate` 的实际输出为准。

## HTTP

`/api/picotera/kv` 的路径、请求体、响应体不变。`database` 驱动下新增的错误响应：

| 操作 | 条件 | 响应 |
|---|---|---|
| `GET /kv` | `pattern` 不是合法 glob；`cursor > 9223372036854775807` | 400 |

`openapi.yaml` 不需要重新生成。

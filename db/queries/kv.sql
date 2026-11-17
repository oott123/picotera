-- GetKvEntry returns a live entry only: a row past its expires_at reads as
-- missing even before the sweeper removes it. now is supplied by Go like every
-- other timestamp in this schema.
-- name: GetKvEntry :one
SELECT value, expires_at FROM kv_entry
WHERE key = sqlc.arg(key) AND (expires_at IS NULL OR expires_at > sqlc.arg(now));

-- UpsertKvEntry keeps the row's id when it overwrites an existing key (expired
-- or not), so a scan cursor never sees the same key twice. A NULL expires_at
-- clears any previous TTL, like Redis SET.
-- name: UpsertKvEntry :exec
INSERT INTO kv_entry (key, value, expires_at) VALUES ($1, $2, $3)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, expires_at = EXCLUDED.expires_at;

-- name: DeleteKvEntry :exec
DELETE FROM kv_entry WHERE key = $1;

-- ScanKvEntries pages live entries by id. pattern is a PostgreSQL regular
-- expression built by kv.GlobToRegexp; row_limit is count + 1 so the caller can
-- tell whether another page follows. now is supplied by Go.
-- name: ScanKvEntries :many
SELECT id, key, value, expires_at FROM kv_entry
WHERE id > sqlc.arg(cursor)
  AND key ~ sqlc.arg(pattern)::text
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now))
ORDER BY id
LIMIT sqlc.arg(row_limit)::bigint;

-- DeleteExpiredKvEntries is the periodic sweeper's statement. Reads already
-- filter on expires_at, so it only reclaims space. The cutoff is supplied by Go.
-- name: DeleteExpiredKvEntries :exec
DELETE FROM kv_entry WHERE expires_at <= $1;

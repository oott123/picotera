-- name: ListApiKeys :many
SELECT * FROM api_key ORDER BY created_at DESC, id DESC;

-- name: GetApiKey :one
SELECT * FROM api_key WHERE id = $1 LIMIT 1;

-- name: GetApiKeyByKey :one
SELECT * FROM api_key WHERE key = $1 LIMIT 1;

-- name: InsertApiKey :one
INSERT INTO api_key (name, key, disabled, annotations)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateApiKey :one
UPDATE api_key
SET name = $2, key = $3, disabled = $4, annotations = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteApiKey :exec
DELETE FROM api_key WHERE id = $1;

-- name: ListApiKeysByAccount :many
SELECT * FROM api_key WHERE account_id = $1 ORDER BY created_at DESC, id DESC;

-- name: GetApiKeyOwnedBy :one
SELECT * FROM api_key WHERE id = $1 AND account_id = $2 LIMIT 1;

-- name: ListScripts :many
SELECT * FROM script ORDER BY created_at DESC, id DESC;

-- name: ListEnabledScripts :many
SELECT * FROM script WHERE enabled = TRUE ORDER BY id ASC;

-- name: ListEnabledScriptFrontends :many
SELECT id, name, frontend FROM script
WHERE enabled = TRUE AND frontend <> ''
ORDER BY id ASC;

-- name: GetScript :one
SELECT * FROM script WHERE id = $1 LIMIT 1;

-- name: InsertScript :one
INSERT INTO script (id, name, source, frontend, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateScript :one
UPDATE script
SET id = $2, name = $3, source = $4, frontend = $5, enabled = $6, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteScript :exec
DELETE FROM script WHERE id = $1;

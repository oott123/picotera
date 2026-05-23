-- name: ListProjectsByAccount :many
SELECT * FROM project WHERE account_id = $1 ORDER BY name ASC;

-- name: GetProjectForAccount :one
SELECT * FROM project WHERE id = $1 AND account_id = $2 LIMIT 1;

-- name: GetProjectByAccountAndName :one
SELECT * FROM project WHERE account_id = $1 AND name = $2 LIMIT 1;

-- name: InsertProject :one
INSERT INTO project (account_id, name, paths) VALUES ($1, $2, $3) RETURNING *;

-- name: InsertProjectIfNotExists :one
-- Used by the gateway auto-create path. ON CONFLICT DO NOTHING means a
-- concurrent insert by the same (account_id, name) leaves the prior row
-- in place and RETURNING is empty; callers must follow up with
-- GetProjectByAccountAndName to fetch the existing row.
INSERT INTO project (account_id, name, paths)
VALUES ($1, $2, $3)
ON CONFLICT (account_id, name) DO NOTHING
RETURNING *;

-- name: UpdateProject :one
UPDATE project SET name = $2, paths = $3, updated_at = now()
WHERE id = $1 AND account_id = $4
RETURNING *;

-- name: DeleteProject :execrows
DELETE FROM project WHERE id = $1 AND account_id = $2;

-- name: ListProjectPaths :many
SELECT id AS project_id, account_id, jsonb_array_elements_text(paths) AS path
FROM project
WHERE jsonb_array_length(paths) > 0;

-- name: UpsertProjectSeen :exec
UPDATE project
SET first_seen_at = LEAST(COALESCE(first_seen_at, sqlc.arg('seen_at')::timestamp), sqlc.arg('seen_at')::timestamp),
    last_seen_at  = GREATEST(COALESCE(last_seen_at,  sqlc.arg('seen_at')::timestamp), sqlc.arg('seen_at')::timestamp),
    updated_at    = now()
WHERE id = $1;

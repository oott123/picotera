-- name: ListProjects :many
SELECT * FROM project WHERE user_id = $1 ORDER BY name ASC;

-- name: GetProject :one
SELECT * FROM project WHERE id = $1 AND user_id = $2 LIMIT 1;

-- name: GetProjectByName :one
SELECT * FROM project WHERE user_id = $1 AND name = $2 LIMIT 1;

-- name: InsertProject :one
INSERT INTO project (name, paths, user_id) VALUES ($1, $2, $3) RETURNING *;

-- name: InsertAutoCreatedProject :one
INSERT INTO project (name, paths, auto_created, user_id) VALUES ($1, $2, true, $3) RETURNING *;

-- name: UpdateProject :one
UPDATE project SET name = $3, paths = $4, updated_at = now() WHERE id = $1 AND user_id = $2 RETURNING *;

-- name: DeleteProject :exec
DELETE FROM project WHERE id = $1 AND user_id = $2;

-- name: MatchProjectByPaths :one
SELECT p.id
FROM project AS p
CROSS JOIN LATERAL jsonb_array_elements_text(p.paths) AS path
WHERE p.user_id = @user_id
  AND path = ANY(@candidate_paths::text[])
ORDER BY length(path) DESC, p.id ASC
LIMIT 1;

-- name: UpsertProjectSeen :exec
UPDATE project
SET first_seen_at = LEAST(COALESCE(first_seen_at, sqlc.arg('seen_at')::timestamp), sqlc.arg('seen_at')::timestamp),
    last_seen_at  = GREATEST(COALESCE(last_seen_at,  sqlc.arg('seen_at')::timestamp), sqlc.arg('seen_at')::timestamp),
    updated_at    = now()
WHERE id = $1;

-- name: MergeProjectUpdateTarget :one
UPDATE project AS p
SET paths = COALESCE((
  SELECT jsonb_agg(DISTINCT elem)
  FROM (
    SELECT jsonb_array_elements_text(p.paths) AS elem
    UNION
    SELECT jsonb_array_elements_text(src.paths) AS elem
    FROM project AS src WHERE src.id = @source_id
  ) all_paths
), p.paths),
    first_seen_at = LEAST(p.first_seen_at, (
      SELECT first_seen_at FROM project WHERE id = @source_id
    )),
    last_seen_at  = GREATEST(p.last_seen_at, (
      SELECT last_seen_at FROM project WHERE id = @source_id
    )),
    updated_at = now()
WHERE p.id = @target_id
RETURNING *;

-- name: MergeProjectReassignRequests :execrows
UPDATE request SET project_id = @target_id
WHERE project_id = @source_id;

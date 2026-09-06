-- name: GetEndpoints :many
SELECT * FROM endpoint;

-- name: GetFirstEndpointByType :one
SELECT * FROM endpoint WHERE endpoint_type = $1 LIMIT 1;

-- name: UpsertEndpoint :one
INSERT INTO endpoint (name, path, model_path, credentials_resolver, endpoint_type, prefix_match) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (path) DO UPDATE SET name = $1, model_path = $3, credentials_resolver = $4, endpoint_type = $5, prefix_match = $6 RETURNING *;

-- name: DeleteEndpoint :exec
DELETE FROM endpoint WHERE path = $1;

-- name: GetUserByID :one
SELECT * FROM app_user WHERE id = $1 LIMIT 1;

-- name: GetUserByIdentity :one
SELECT u.* FROM app_user u
JOIN user_identity i ON i.user_id = u.id
WHERE i.provider = $1 AND i.identity = $2
LIMIT 1;

-- name: InsertUser :one
INSERT INTO app_user (display_name, is_admin, annotations) VALUES ($1, $2, $3) RETURNING *;

-- name: InsertUserIdentity :one
INSERT INTO user_identity (user_id, provider, identity)
VALUES ($1, $2, $3)
ON CONFLICT (provider, identity) DO NOTHING
RETURNING *;

-- name: GetUserIdentity :one
SELECT * FROM user_identity WHERE provider = $1 AND identity = $2 LIMIT 1;

-- name: UpdateUserIdentityUser :one
UPDATE user_identity SET user_id = $3 WHERE provider = $1 AND identity = $2 RETURNING *;

-- name: UpdateUserAdmin :one
UPDATE app_user SET is_admin = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: ListUsers :many
SELECT * FROM app_user ORDER BY id;

-- name: UpdateUser :one
UPDATE app_user
SET display_name = $2, is_admin = $3, disabled = $4, annotations = $5, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM app_user WHERE id = $1;

-- name: DeleteUserIdentitiesByUser :exec
DELETE FROM user_identity WHERE user_id = $1;

-- name: ListUserIdentities :many
SELECT * FROM user_identity WHERE user_id = $1 ORDER BY id;

-- name: GetUserIdentityByID :one
SELECT * FROM user_identity WHERE id = $1 LIMIT 1;

-- name: CreateUserIdentity :one
INSERT INTO user_identity (user_id, provider, identity)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdateUserIdentity :one
UPDATE user_identity SET provider = $2, identity = $3 WHERE id = $1 RETURNING *;

-- name: DeleteUserIdentity :exec
DELETE FROM user_identity WHERE id = $1;

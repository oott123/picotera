-- name: ListUserSettings :many
SELECT * FROM user_setting WHERE user_id = $1 ORDER BY key;

-- name: GetUserSetting :one
SELECT * FROM user_setting WHERE user_id = $1 AND key = $2 LIMIT 1;

-- name: UpsertUserSetting :one
INSERT INTO user_setting (user_id, key, value, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (user_id, key) DO UPDATE SET value = $3, updated_at = now()
RETURNING *;

-- name: DeleteUserSetting :execrows
DELETE FROM user_setting WHERE user_id = $1 AND key = $2;

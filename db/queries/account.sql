-- name: GetAccountByID :one
SELECT * FROM account WHERE id = $1;

-- name: GetAccountByUsername :one
SELECT * FROM account WHERE username = $1;

-- name: GetAccountByWebauthnUserHandle :one
SELECT * FROM account WHERE webauthn_user_handle = $1;

-- name: InsertAccount :one
INSERT INTO account (
  username, display_name, webauthn_user_handle, role,
  can_view_own_usage, can_manage_own_api_keys, can_view_models, can_view_own_traces,
  disabled
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: HasAnyActiveAdmin :one
SELECT EXISTS(SELECT 1 FROM account WHERE role = 'admin' AND NOT disabled) AS has_admin;

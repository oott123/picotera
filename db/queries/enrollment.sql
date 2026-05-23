-- name: GetEnrollmentByToken :one
SELECT * FROM enrollment WHERE token = $1;

-- name: InsertEnrollment :one
INSERT INTO enrollment (
  token, intent, target_account_id, expires_at,
  template_role,
  template_can_view_own_usage, template_can_manage_own_api_keys,
  template_can_view_models, template_can_view_own_traces,
  template_username, template_display_name
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: ConsumeEnrollment :one
-- Atomic single-use consume. Returns the row only if it was unconsumed.
-- Callers detect the "already consumed" branch via pgx.ErrNoRows.
UPDATE enrollment
SET consumed_at = now()
WHERE token = $1 AND consumed_at IS NULL
RETURNING *;

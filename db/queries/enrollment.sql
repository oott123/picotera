-- name: GetEnrollmentByToken :one
SELECT * FROM enrollment WHERE token = $1;

-- name: InsertEnrollment :one
INSERT INTO enrollment (token, intent, target_account_id, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: MarkEnrollmentConsumed :exec
UPDATE enrollment SET consumed_at = now() WHERE token = $1;

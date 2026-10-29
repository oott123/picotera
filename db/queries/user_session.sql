-- TouchUserSession reads a session and slides its expiry in one statement: the
-- UPDATE is the read, so there is no window in which a just-expired row could be
-- observed as live, and the JOIN folds the user lookup into the same round trip
-- (a deleted user's session therefore returns no rows and reads as "no session").
-- new_expires_at is now + session_ttl; both timestamps are supplied by Go like
-- every other timestamp in this schema.
-- name: TouchUserSession :one
WITH touched AS (
  UPDATE user_session SET expires_at = sqlc.arg(new_expires_at)
  WHERE user_session.id = sqlc.arg(id) AND user_session.expires_at > sqlc.arg(now)
  RETURNING user_id
)
SELECT app_user.* FROM app_user JOIN touched ON app_user.id = touched.user_id;

-- name: InsertUserSession :exec
INSERT INTO user_session (id, user_id, expires_at) VALUES ($1, $2, $3);

-- name: DeleteUserSession :exec
DELETE FROM user_session WHERE id = $1;

-- DeleteExpiredUserSessions runs on every successful login and only touches the
-- logging-in user's rows. Sessions of users who never come back are lazy garbage;
-- they cost nothing but disk, so there is no background sweeper.
-- name: DeleteExpiredUserSessions :exec
DELETE FROM user_session WHERE user_id = $1 AND expires_at <= $2;

-- name: DeleteUserSessionsByUser :exec
DELETE FROM user_session WHERE user_id = $1;

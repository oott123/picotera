-- +goose Up
-- Backfill existing projects to user 1 (single-user-mode root on fresh deploys),
-- then drop the default so future inserts must supply user_id explicitly.
ALTER TABLE project ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE project ALTER COLUMN user_id DROP DEFAULT;

-- name is now unique per user instead of globally.
ALTER TABLE project DROP CONSTRAINT project_name_key;
ALTER TABLE project ADD CONSTRAINT project_user_id_name_key UNIQUE (user_id, name);

CREATE INDEX project_user_id_idx ON project (user_id);

-- +goose Down
DROP INDEX IF EXISTS project_user_id_idx;
ALTER TABLE project DROP CONSTRAINT project_user_id_name_key;
ALTER TABLE project ADD CONSTRAINT project_name_key UNIQUE (name);
ALTER TABLE project DROP COLUMN user_id;

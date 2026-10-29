-- +goose Up
-- Backfill existing rows to user 1 (single-user-mode root on fresh deploys),
-- then drop the default so future inserts must supply user_id explicitly.
ALTER TABLE api_key ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE api_key ALTER COLUMN user_id DROP DEFAULT;

-- request.user_id stays nullable: the meta row is inserted before auth (user
-- unknown) and backfilled by UpdateRequestOnHeader once the API key resolves.
ALTER TABLE request ADD COLUMN user_id BIGINT DEFAULT 1;
ALTER TABLE request ALTER COLUMN user_id DROP DEFAULT;

-- traces are only created after auth (user known), so user_id is NOT NULL.
ALTER TABLE traces ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1;
ALTER TABLE traces ALTER COLUMN user_id DROP DEFAULT;

-- A parent_span_id is client-controlled, so two users can collide on it. Scope
-- trace identity by (parent_span_id, user_id) so each user gets an independent
-- trace row. Existing rows are all user 1, so the composite key has no conflict.
ALTER TABLE traces DROP CONSTRAINT traces_parent_span_id_key;
ALTER TABLE traces ADD CONSTRAINT traces_parent_span_id_user_id_key UNIQUE (parent_span_id, user_id);

CREATE INDEX request_user_id_idx ON request (user_id, created_at DESC, id DESC);
CREATE INDEX api_key_user_id_idx ON api_key (user_id);
CREATE INDEX traces_user_id_idx ON traces (user_id, last_request_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS traces_user_id_idx;
DROP INDEX IF EXISTS api_key_user_id_idx;
DROP INDEX IF EXISTS request_user_id_idx;

ALTER TABLE traces DROP CONSTRAINT traces_parent_span_id_user_id_key;
ALTER TABLE traces ADD CONSTRAINT traces_parent_span_id_key UNIQUE (parent_span_id);

ALTER TABLE traces DROP COLUMN user_id;
ALTER TABLE request DROP COLUMN user_id;
ALTER TABLE api_key DROP COLUMN user_id;

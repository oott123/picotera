-- +goose Up
ALTER TABLE app_user ADD COLUMN annotations JSONB NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE app_user DROP COLUMN annotations;

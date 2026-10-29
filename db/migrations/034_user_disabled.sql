-- +goose Up
ALTER TABLE app_user ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE app_user DROP COLUMN disabled;

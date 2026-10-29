-- +goose Up
ALTER TABLE request DROP COLUMN status;

-- +goose Down
ALTER TABLE request ADD COLUMN status INTEGER NOT NULL DEFAULT 0;

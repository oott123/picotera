-- +goose Up
ALTER TABLE script ADD COLUMN frontend TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE script DROP COLUMN frontend;

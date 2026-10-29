-- +goose Up
ALTER TABLE request ADD COLUMN inferred_provider TEXT;
ALTER TABLE request ADD COLUMN inferred_model TEXT;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS inferred_model;
ALTER TABLE request DROP COLUMN IF EXISTS inferred_provider;

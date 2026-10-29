-- +goose Up
ALTER TABLE request ADD COLUMN inferred_model_source SMALLINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS inferred_model_source;

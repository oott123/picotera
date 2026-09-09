-- +goose Up
ALTER TABLE request ADD COLUMN usage_raw JSONB;
ALTER TABLE request ADD COLUMN tool_usage_raw JSONB;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS tool_usage_raw;
ALTER TABLE request DROP COLUMN IF EXISTS usage_raw;

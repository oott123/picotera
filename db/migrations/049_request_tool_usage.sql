-- +goose Up
ALTER TABLE request ADD COLUMN tool_usage JSONB;
ALTER TABLE request ADD COLUMN tool_cost NUMERIC(20, 6);
ALTER TABLE request ADD COLUMN tool_cost_currency TEXT;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS tool_cost_currency;
ALTER TABLE request DROP COLUMN IF EXISTS tool_cost;
ALTER TABLE request DROP COLUMN IF EXISTS tool_usage;

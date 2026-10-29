-- +goose Up
ALTER TABLE request ADD COLUMN external_request_id TEXT;
ALTER TABLE request ADD COLUMN external_response_id TEXT;

-- +goose Down
ALTER TABLE request DROP COLUMN IF EXISTS external_response_id;
ALTER TABLE request DROP COLUMN IF EXISTS external_request_id;

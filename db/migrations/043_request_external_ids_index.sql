-- +goose Up
CREATE INDEX request_external_request_id_idx
  ON request (external_request_id)
  WHERE external_request_id IS NOT NULL AND external_request_id <> '';
CREATE INDEX request_external_response_id_idx
  ON request (external_response_id)
  WHERE external_response_id IS NOT NULL AND external_response_id <> '';

-- +goose Down
DROP INDEX IF EXISTS request_external_response_id_idx;
DROP INDEX IF EXISTS request_external_request_id_idx;

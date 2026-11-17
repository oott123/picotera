-- +goose Up
CREATE TABLE kv_entry (
  id         BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY UNIQUE,
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  expires_at TIMESTAMPTZ
);

CREATE INDEX kv_entry_expires_at_idx ON kv_entry (expires_at) WHERE expires_at IS NOT NULL;

-- +goose Down
DROP TABLE kv_entry;

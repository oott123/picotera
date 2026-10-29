-- +goose Up
ALTER TABLE provider ADD COLUMN insecure_tls BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE provider DROP COLUMN insecure_tls;

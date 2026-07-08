-- +goose Up
ALTER TABLE request ADD COLUMN annotations JSONB;
CREATE INDEX request_annotations_idx ON request USING GIN (annotations jsonb_path_ops)
  WHERE annotations IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS request_annotations_idx;
ALTER TABLE request DROP COLUMN IF EXISTS annotations;

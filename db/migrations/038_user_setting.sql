-- +goose Up
CREATE TABLE user_setting (
  user_id    BIGINT NOT NULL,
  key        TEXT NOT NULL,
  value      JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, key)
);

DROP TABLE global_setting;

-- +goose Down
CREATE TABLE global_setting (
  key TEXT PRIMARY KEY,
  value JSONB NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DROP TABLE user_setting;

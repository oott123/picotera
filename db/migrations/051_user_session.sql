-- +goose Up
CREATE TABLE user_session (
  id         TEXT PRIMARY KEY,
  user_id    BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX user_session_user_id_idx ON user_session (user_id);

-- +goose Down
DROP TABLE user_session;

-- +goose Up
CREATE TABLE app_user (
  id           BIGSERIAL PRIMARY KEY,
  display_name TEXT NOT NULL,
  is_admin     BOOLEAN NOT NULL DEFAULT false,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE user_identity (
  id         BIGSERIAL PRIMARY KEY,
  user_id    BIGINT NOT NULL,
  provider   TEXT NOT NULL,
  identity   TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, identity)
);

CREATE INDEX user_identity_user_id_idx ON user_identity (user_id);

-- +goose Down
DROP TABLE user_identity;
DROP TABLE app_user;

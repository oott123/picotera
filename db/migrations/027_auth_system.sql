-- +goose Up

CREATE TABLE account (
  id                       SERIAL PRIMARY KEY,
  username                 TEXT NOT NULL UNIQUE,
  display_name             TEXT NOT NULL,
  webauthn_user_handle     BYTEA NOT NULL UNIQUE,
  role                     TEXT NOT NULL CHECK (role IN ('admin','user')),
  can_view_own_usage       BOOLEAN NOT NULL DEFAULT FALSE,
  can_manage_own_api_keys  BOOLEAN NOT NULL DEFAULT FALSE,
  can_view_models          BOOLEAN NOT NULL DEFAULT FALSE,
  can_view_own_traces      BOOLEAN NOT NULL DEFAULT FALSE,
  disabled                 BOOLEAN NOT NULL DEFAULT FALSE,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE webauthn_credential (
  id                SERIAL PRIMARY KEY,
  account_id        INTEGER NOT NULL REFERENCES account(id) ON DELETE CASCADE,
  credential_id     BYTEA   NOT NULL UNIQUE,
  public_key        BYTEA   NOT NULL,
  sign_count        BIGINT  NOT NULL,
  transports        TEXT[]  NOT NULL DEFAULT '{}',
  aaguid            BYTEA,
  attestation_type  TEXT NOT NULL DEFAULT '',
  backup_eligible   BOOLEAN NOT NULL,
  backup_state      BOOLEAN NOT NULL,
  nickname          TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_used_at      TIMESTAMPTZ
);
CREATE INDEX webauthn_credential_account_idx ON webauthn_credential (account_id);

CREATE TABLE enrollment (
  token             TEXT PRIMARY KEY,
  intent            TEXT NOT NULL CHECK (intent IN ('bootstrap','invite','reset')),
  target_account_id INTEGER REFERENCES account(id) ON DELETE CASCADE,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at        TIMESTAMPTZ NOT NULL,
  consumed_at       TIMESTAMPTZ,
  CHECK (
    (intent = 'bootstrap' AND target_account_id IS NULL)
    OR (intent IN ('invite','reset') AND target_account_id IS NOT NULL)
  )
);

ALTER TABLE api_key ADD COLUMN account_id INTEGER REFERENCES account(id) ON DELETE SET NULL;
CREATE INDEX api_key_account_idx ON api_key (account_id);

-- +goose Down

DROP INDEX IF EXISTS api_key_account_idx;
ALTER TABLE api_key DROP COLUMN account_id;
DROP TABLE IF EXISTS enrollment;
DROP INDEX IF EXISTS webauthn_credential_account_idx;
DROP TABLE IF EXISTS webauthn_credential;
DROP TABLE IF EXISTS account;

-- +goose Up

-- 1. api_key.account_id: move from ON DELETE SET NULL to ON DELETE CASCADE,
--    enforce NOT NULL, and add per-user UNIQUE(name).
--
-- Pre-030, api_key.account_id could be NULL in two cases: keys created before
-- migration 027, and keys whose owner account was deleted (the old SET NULL
-- semantics). H1 makes the gateway reject NULL account_id, so the latter case
-- is already broken at runtime. We backfill NULL rows to the oldest active
-- admin so they remain usable, then flip the constraint to CASCADE: from now
-- on, deleting a user revokes their api_keys atomically.

-- +goose StatementBegin
DO $$
DECLARE
  orphans INTEGER;
  admin_id INTEGER;
BEGIN
  SELECT COUNT(*) INTO orphans FROM api_key WHERE account_id IS NULL;
  IF orphans > 0 THEN
    SELECT id INTO admin_id FROM account
      WHERE role = 'admin' AND NOT disabled
      ORDER BY id ASC LIMIT 1;
    IF admin_id IS NULL THEN
      RAISE EXCEPTION 'migration 030: % orphan api_key rows but no active admin to backfill to. Run picotera enroll-admin first or DELETE FROM api_key WHERE account_id IS NULL before re-running.', orphans;
    END IF;
    UPDATE api_key SET account_id = admin_id WHERE account_id IS NULL;
  END IF;
END$$;
-- +goose StatementEnd

ALTER TABLE api_key ALTER COLUMN account_id SET NOT NULL;

ALTER TABLE api_key DROP CONSTRAINT api_key_account_id_fkey;
ALTER TABLE api_key
  ADD CONSTRAINT api_key_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES account(id) ON DELETE CASCADE;

ALTER TABLE api_key ADD CONSTRAINT api_key_account_id_name_key UNIQUE (account_id, name);

-- 2. request.account_id: denormalize the owner so request rows survive
--    api_key/account deletion. No FK constraint — same pattern as
--    request.project_id (migration 020) on a hypertable: we want history to
--    persist independently of the parent row.

ALTER TABLE request ADD COLUMN account_id INTEGER;

UPDATE request r
   SET account_id = k.account_id
  FROM api_key k
 WHERE r.api_key_id = k.id AND r.account_id IS NULL;

CREATE INDEX request_account_id_idx ON request (account_id, created_at DESC);

-- 3. account.can_manage_own_projects: new permission flag. Defaults to FALSE
--    for normal users; admins are flipped to TRUE explicitly. The handle_account
--    admin-coercion code keeps admins all-true going forward.

ALTER TABLE account ADD COLUMN can_manage_own_projects BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE account SET can_manage_own_projects = TRUE WHERE role = 'admin';

-- +goose Down

ALTER TABLE account DROP COLUMN can_manage_own_projects;

DROP INDEX IF EXISTS request_account_id_idx;
ALTER TABLE request DROP COLUMN account_id;

ALTER TABLE api_key DROP CONSTRAINT api_key_account_id_name_key;

ALTER TABLE api_key DROP CONSTRAINT api_key_account_id_fkey;
ALTER TABLE api_key
  ADD CONSTRAINT api_key_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES account(id) ON DELETE SET NULL;

ALTER TABLE api_key ALTER COLUMN account_id DROP NOT NULL;

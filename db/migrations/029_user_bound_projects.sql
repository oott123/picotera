-- +goose Up

-- Projects are now per-user: each row is owned by exactly one account_id.
-- Two different users may have projects with the same name or paths without
-- collision; uniqueness is scoped (account_id, name).
ALTER TABLE project ADD COLUMN account_id INTEGER REFERENCES account(id) ON DELETE CASCADE;

-- Backfill existing rows to the oldest active admin. Pre-029 there is no
-- per-user notion, so historical rows are arbitrarily attributed to whoever
-- bootstrapped the instance. Documented as a one-time dev-data quirk: in
-- production deployments where pre-029 project rows exist, an admin should
-- review and reassign them via direct SQL after migration.
--
-- Guard: if pre-existing project rows exist but no active admin is available
-- to backfill to, abort with a clear message rather than silently setting
-- account_id = NULL and then crashing on the SET NOT NULL below.
-- +goose StatementBegin
DO $$
DECLARE
  pre_existing INTEGER;
  admin_id INTEGER;
BEGIN
  SELECT COUNT(*) INTO pre_existing FROM project WHERE account_id IS NULL;
  IF pre_existing > 0 THEN
    SELECT id INTO admin_id FROM account
      WHERE role = 'admin' AND NOT disabled
      ORDER BY id ASC LIMIT 1;
    IF admin_id IS NULL THEN
      RAISE EXCEPTION 'migration 029: % pre-existing project rows but no active admin to backfill to. Run picotera enroll-admin first or DELETE FROM project before re-running.', pre_existing;
    END IF;
    UPDATE project SET account_id = admin_id WHERE account_id IS NULL;
  END IF;
END$$;
-- +goose StatementEnd

ALTER TABLE project ALTER COLUMN account_id SET NOT NULL;

-- Replace global UNIQUE(name) with per-account uniqueness so a user's
-- namespace doesn't collide with another user's.
ALTER TABLE project DROP CONSTRAINT project_name_key;
ALTER TABLE project ADD CONSTRAINT project_account_id_name_key UNIQUE (account_id, name);

-- Index for per-account scans (the common case for ListProjectsByAccount
-- and ListProjectPaths). Without it, every list call would seq-scan the
-- whole table.
CREATE INDEX project_account_id_idx ON project (account_id);

-- +goose Down

-- Down restores the pre-029 shape. NOTE: this can fail if post-029 data
-- contains cross-account name duplicates — in that case the operator must
-- de-duplicate manually before downgrading. Acceptable for dev.
DROP INDEX IF EXISTS project_account_id_idx;
ALTER TABLE project DROP CONSTRAINT IF EXISTS project_account_id_name_key;
ALTER TABLE project ADD CONSTRAINT project_name_key UNIQUE (name);
ALTER TABLE project DROP COLUMN IF EXISTS account_id;

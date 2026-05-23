-- +goose Up

ALTER TABLE enrollment
  ADD COLUMN template_role                     TEXT,
  ADD COLUMN template_can_view_own_usage       BOOLEAN,
  ADD COLUMN template_can_manage_own_api_keys  BOOLEAN,
  ADD COLUMN template_can_view_models          BOOLEAN,
  ADD COLUMN template_can_view_own_traces      BOOLEAN,
  -- template_username and template_display_name are RESERVED but unused as of
  -- P5.03. The invite flow stopped pre-populating username/displayName
  -- suggestions; the invitee always picks their own credentials at consume
  -- time. Kept in schema to avoid the migration churn of dropping nullable
  -- columns that hold no data.
  ADD COLUMN template_username                 TEXT,
  ADD COLUMN template_display_name             TEXT;

-- Drop the anonymous CHECK that required invite to have a target. We can't
-- reference it by name (migration 027 declared it inline), so look it up via
-- pg_constraint by matching on its definition.
-- +goose StatementBegin
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c
    FROM pg_constraint
    WHERE conrelid = 'enrollment'::regclass
      AND contype = 'c'
      AND pg_get_constraintdef(oid) LIKE '%intent = ''bootstrap''%';
  IF c IS NOT NULL THEN
    EXECUTE 'ALTER TABLE enrollment DROP CONSTRAINT ' || quote_ident(c);
  END IF;
END$$;
-- +goose StatementEnd

-- New CHECK: only reset requires target. Invite no longer ties to target.
-- Bootstrap still requires target IS NULL.
ALTER TABLE enrollment ADD CONSTRAINT enrollment_intent_target_check CHECK (
  (intent = 'bootstrap' AND target_account_id IS NULL)
  OR (intent = 'invite')
  OR (intent = 'reset' AND target_account_id IS NOT NULL)
);

-- Templates only make sense on invite intent. Defensive constraint:
ALTER TABLE enrollment ADD CONSTRAINT enrollment_template_intent_check CHECK (
  intent = 'invite'
  OR (template_role IS NULL
      AND template_can_view_own_usage IS NULL
      AND template_can_manage_own_api_keys IS NULL
      AND template_can_view_models IS NULL
      AND template_can_view_own_traces IS NULL
      AND template_username IS NULL
      AND template_display_name IS NULL)
);

-- +goose Down

ALTER TABLE enrollment DROP CONSTRAINT enrollment_template_intent_check;
ALTER TABLE enrollment DROP CONSTRAINT enrollment_intent_target_check;
ALTER TABLE enrollment ADD CONSTRAINT enrollment_check CHECK (
  (intent = 'bootstrap' AND target_account_id IS NULL)
  OR (intent IN ('invite','reset') AND target_account_id IS NOT NULL)
);

ALTER TABLE enrollment
  DROP COLUMN template_display_name,
  DROP COLUMN template_username,
  DROP COLUMN template_can_view_own_traces,
  DROP COLUMN template_can_view_models,
  DROP COLUMN template_can_manage_own_api_keys,
  DROP COLUMN template_can_view_own_usage,
  DROP COLUMN template_role;

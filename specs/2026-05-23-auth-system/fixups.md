# Phase 4 — Auth System Fix-ups

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Address audit findings on the WebAuthn auth & account system before considering the branch shippable. Closes one correctness bug (token-consume race), one wire-format bug (disabled-middleware plain-text 403), one policy bug (admins can be disabled), one design issue (invite creates account up-front), plus error-mapping cleanups, audit logging, naming consistency, and self-delete prevention.

**Architecture:** Surgical fixes to existing handlers, queries, middleware, and Vue components. One additive migration (028) for the invite-as-template template columns. No new packages, no new infrastructure.

**Tech Stack:** Same as Phase 1-3 — Go 1.26 / Huma v2 / pgx / sqlc / goose; Vue 3 / vue-query / openapi-fetch; Postgres / KeyDB.

**Branch:** `feat-user-system` (continuation; no new branch).

**Decisions made up-front (user-approved):**
- Q1 invite model → **B: invite-as-template** (account created at consume time).
- Q2 admin disable policy → **A: admins cannot be disabled** (demote first, then disable).
- Q3 reveal-once safety → confirm on panel close when URL is visible.

---

## Task 1: Atomic enrollment-token consume (C1)

**Goal:** A single enrollment token can never be successfully consumed twice. Concurrent registers on the same token: one wins, the other gets `enrollment_consumed`.

**Files:**
- Modify: `db/queries/enrollment.sql` — change `MarkEnrollmentConsumed` semantics; add a `ConsumeEnrollmentTx` query.
- Regenerate: `pkg/db/enrollment.sql.go` via sqlc.
- Modify: `pkg/auth/enrollment.go` — make `ConsumeEnrollment` return `*db.Enrollment` and an error; map "zero rows" to `ErrEnrollmentConsumed`.
- Modify: `pkg/server/handle_enrollment.go` — consume FIRST inside the TX (before credential insert), abort on miss. Re-derive `e.Intent` and `e.TargetAccountID` from the consume return value (not the pre-TX `LoadEnrollment`).

**Acceptance Criteria:**
- [ ] `MarkEnrollmentConsumed` is conditional on `consumed_at IS NULL` and returns the row.
- [ ] `handle_enrollment.go::handleEnrollmentCompleteHTTP` consumes the token at the top of the TX. If consume returns no row, the handler writes `auth.ErrEnrollmentConsumed()` and aborts (rollback).
- [ ] Existing single-shot flow (one tab, one click) still succeeds.
- [ ] `go build ./...`, `go test ./pkg/auth ./pkg/server` clean.

**Verify:** Unit test in `pkg/auth/enrollment_test.go` that issues a token, calls `ConsumeEnrollment` twice from the same TX (or one TX + one outside), asserts second call returns `ErrEnrollmentConsumed`.

**Steps:**

- [ ] **Step 1: SQL change.** Replace existing `MarkEnrollmentConsumed` with two queries: keep `MarkEnrollmentConsumed :execrows` (still useful for tests + the `enroll-admin --reset` audit log later), add `ConsumeEnrollmentReturning :one`.

```sql
-- db/queries/enrollment.sql

-- name: GetEnrollmentByToken :one
SELECT * FROM enrollment WHERE token = $1;

-- name: InsertEnrollment :one
INSERT INTO enrollment (token, intent, target_account_id, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ConsumeEnrollment :one
-- Atomic single-use consume. Returns the row only if it was unconsumed.
-- Callers detect the "already consumed" branch via pgx.ErrNoRows.
UPDATE enrollment
SET consumed_at = now()
WHERE token = $1 AND consumed_at IS NULL
RETURNING *;
```

(Delete the old `MarkEnrollmentConsumed :exec` query — no other callers.)

- [ ] **Step 2: Regenerate.**

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- sqlc generate
```

Verify `pkg/db/enrollment.sql.go` now has `func (q *Queries) ConsumeEnrollment(ctx, token string) (Enrollment, error)`.

- [ ] **Step 3: Update `pkg/auth/enrollment.go`.** Replace the helper:

```go
// ConsumeEnrollment atomically marks a token consumed and returns the row.
// Returns ErrEnrollmentConsumed if the token was already consumed, missing, or
// expired (the conditional UPDATE handles consumed; LoadEnrollment-style
// expiry checking happens here too for symmetry with the preview path).
//
// Must be called inside the same TX as the credential / account insert so a
// crash between operations doesn't allow the same token to be reused.
func ConsumeEnrollment(ctx context.Context, q db.Querier, token string) (*db.Enrollment, error) {
	row, err := q.ConsumeEnrollment(ctx, token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEnrollmentConsumed()
		}
		return nil, fmt.Errorf("enrollment: consume: %w", err)
	}
	if !row.ExpiresAt.Valid || time.Now().After(row.ExpiresAt.Time) {
		return nil, ErrEnrollmentExpired()
	}
	return &row, nil
}
```

- [ ] **Step 4: Refactor `pkg/server/handle_enrollment.go::handleEnrollmentCompleteHTTP`.** Move `ConsumeEnrollment` to the TOP of the TX. The pre-TX `LoadEnrollment` is still used to pull the intent for ceremony state lookup (KV stash + parse credential body), but the **authoritative consume** happens inside the TX. After consume, re-derive `e.Intent` and `e.TargetAccountID` from the returned row so we never act on a stale pre-TX view.

```go
func (s *Server) handleEnrollmentCompleteHTTP(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")

	// Pre-TX read to surface 404/expired before we open a connection. The
	// authoritative consume happens inside the TX (see below).
	if _, err := auth.LoadEnrollment(r.Context(), s.queries, token); err != nil {
		writeAuthErr(w, err)
		return
	}

	raw, err := s.kvStore.Get(r.Context(), "webauthn_ceremony:enroll:"+token)
	if err != nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony("ceremony expired"))
		return
	}
	var stash enrollCeremonyStash
	if err := json.Unmarshal([]byte(raw), &stash); err != nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony("ceremony corrupt"))
		return
	}

	parsed, err := protocol.ParseCredentialCreationResponseBody(r.Body)
	if err != nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error()))
		return
	}

	tx, err := s.dbPool.Begin(r.Context())
	if err != nil {
		writeAuthErr(w, fmt.Errorf("enrollment/complete: begin tx: %w", err))
		return
	}
	defer tx.Rollback(r.Context()) //nolint:errcheck

	qtx := s.queries.WithTx(tx)

	// Atomic consume — if another tab/replay beat us here, this returns
	// ErrEnrollmentConsumed and we abort. No credential or account is created.
	consumed, err := auth.ConsumeEnrollment(r.Context(), qtx, token)
	if err != nil {
		writeAuthErr(w, err)
		return
	}

	var account db.Account

	switch consumed.Intent {
	case auth.IntentBootstrap:
		// ... existing body, but using `consumed` instead of `e` ...
	case auth.IntentInvite:
		if !consumed.TargetAccountID.Valid { /* template branch — see Task 5 */ }
		// ...
	case auth.IntentReset:
		if !consumed.TargetAccountID.Valid {
			writeAuthErr(w, auth.ErrEnrollmentConsumed())
			return
		}
		// ...
	default:
		writeAuthErr(w, auth.ErrEnrollmentConsumed())
		return
	}

	// (No separate ConsumeEnrollment call at the end — already consumed at the top.)

	if err := tx.Commit(r.Context()); err != nil {
		writeAuthErr(w, fmt.Errorf("enrollment/complete: commit: %w", err))
		return
	}

	_ = s.kvStore.Del(r.Context(), "webauthn_ceremony:enroll:"+token)
	if consumed.Intent == auth.IntentReset {
		_, _ = s.sessionStore.RevokeAllForAccount(r.Context(), account.ID)
	}

	// Issue session...
}
```

Note: replace EVERY `e.Intent`, `e.TargetAccountID` reference in this function with `consumed.*`. The variable named `e` is gone. Also delete the bottom-of-function `auth.ConsumeEnrollment(r.Context(), qtx, token)` call.

- [ ] **Step 5: Write the race test.** Add to `pkg/auth/enrollment_test.go`:

```go
func TestConsumeEnrollment_OncePerToken(t *testing.T) {
	ctx, q, _ := newTestQueries(t) // existing helper
	tok, _, err := IssueEnrollment(ctx, q, IntentBootstrap, nil, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := ConsumeEnrollment(ctx, q, tok); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	_, err = ConsumeEnrollment(ctx, q, tok)
	if !errors.Is(err, &AuthError{}) || AsAuthError(err).Code != "enrollment_consumed" {
		t.Fatalf("second consume should be enrollment_consumed; got %v", err)
	}
}
```

(Match the existing test harness style — check `pkg/auth/enrollment_test.go` for `newTestQueries` or equivalent and adapt the helper name.)

- [ ] **Step 6: Verify.**

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- sqlc generate
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
```

Manually: bootstrap a fresh DB, run `enroll-admin`, open the URL in two browser tabs, click 注册 Passkey on tab 1 → success. Click on tab 2 → server returns `enrollment_consumed`.

- [ ] **Step 7: Commit.**

```bash
git add db/queries/enrollment.sql pkg/db/ pkg/auth/enrollment.go pkg/auth/enrollment_test.go pkg/server/handle_enrollment.go
git commit -m "fix(auth): atomic enrollment-token consume to prevent double-use race"
```

---

## Task 2: Disabled-middleware JSON envelope + public-route bypass (C2)

**Goal:** When a disabled account's session is detected, return the project's standard JSON error envelope (so `ApiRequestError.code === "account_disabled"` works), AND don't block public routes — a disabled user with a stale cookie should still be able to consume a reset URL or sign out cleanly.

**Files:**
- Modify: `pkg/auth/middleware.go` — split the disable handling into "always revoke + clear cookie" (in LoadSession) + "reject on protected routes" (in Check).

**Acceptance Criteria:**
- [ ] A disabled user's cookie is revoked on the next request regardless of route.
- [ ] On a public route (`/auth/logout`, `/auth/status`, `/auth/login/*`, `/enrollments/*`), the disabled user continues through unauthenticated (no 403).
- [ ] On a session/admin/permission route, the disabled user gets `403 account_disabled` in the standard JSON envelope.
- [ ] Existing tests still pass.

**Verify:** Add a test in `pkg/auth/middleware_test.go` that constructs a request with a disabled-account cookie, runs it through LoadSession + a public handler → expects 200 (or whatever the public handler returns) + the cookie is cleared. Same setup through Check with `AuthSession` → expects `*AuthError{Code: "account_disabled"}`.

**Steps:**

- [ ] **Step 1: Move the disabled check.** In `LoadSession`, drop the disabled-account 403 branch entirely; instead, just revoke + clear cookie and continue WITHOUT attaching the session to context. That way, downstream public routes see no session (work fine) and protected routes' `Check(s, req)` sees nil session → returns `ErrNoSession`. The user then sees the standard 401 envelope.

Wait — we want disabled users on protected routes to see `account_disabled`, not `no_session`. So: attach a sentinel that Check can recognize. Cleanest: attach the session with `Account.Disabled == true` (i.e. don't drop it), and have Check refuse it with `ErrAccountDisabled`.

```go
// In LoadSession, replace the disabled block:
if account.Disabled {
	// Revoke the session in KV regardless of route — disabled users are
	// terminally locked out as far as the session store is concerned.
	_ = store.Revoke(r.Context(), accountID, token)
	http.SetCookie(w, ClearedSessionCookie(cfg, r))
	// Continue WITHOUT attaching a session. The route's Check will see nil
	// (treated as unauthenticated). Public routes work; protected routes
	// reject with no_session.
	next.ServeHTTP(w, r)
	return
}
```

Wait — that loses the `account_disabled` machine-readable code on protected routes (they'd see `no_session` instead). Better:

- [ ] **Step 1b: Attach a sentinel session with Disabled=true, and teach Check to map it.** Replace the LoadSession disabled block with:

```go
if account.Disabled {
	// Revoke the persistent session immediately — they should be locked out.
	_ = store.Revoke(r.Context(), accountID, token)
	http.SetCookie(w, ClearedSessionCookie(cfg, r))
	// Attach a session sentinel so Check can return account_disabled instead
	// of no_session on protected routes. Public routes ignore the session.
	ctx := WithSession(r.Context(), &Session{Account: &account, Token: "", Data: nil})
	next.ServeHTTP(w, r.WithContext(ctx))
	return
}
```

And in Check, add a disabled-check before any other branch:

```go
func Check(s *Session, req contract.AuthRequirement) error {
	if req.Kind == contract.AuthPublic {
		return nil
	}
	if s == nil {
		return ErrNoSession()
	}
	if s.Account.Disabled {
		return ErrAccountDisabled()
	}
	switch req.Kind {
	// existing cases...
	}
}
```

- [ ] **Step 2: Update tests** in `pkg/auth/middleware_test.go` (or create it if it doesn't have the disabled-handling test):

```go
func TestLoadSession_DisabledAccount_PublicRouteContinues(t *testing.T) {
	// Setup: insert a disabled account + a live session; make a request to a
	// public route through LoadSession + a no-op next handler; assert:
	//   - response is 200 (no 403)
	//   - Set-Cookie: picotera_session=; Max-Age=-1
	//   - SessionFromContext(ctx) returns a session with Account.Disabled == true
	//
	// Then run Check(session, AuthRequirement{Kind: AuthPublic}) → nil
	// And run Check(session, AuthRequirement{Kind: AuthSession}) → *AuthError{Code: "account_disabled"}
}
```

(Match the existing test style. Use whatever helper builds queries + session store for the package; if none, do a minimal in-memory wiring.)

- [ ] **Step 3: Verify.**

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
```

Manually: log in as admin, disable yourself via direct DB UPDATE (`UPDATE account SET disabled=true WHERE id=...`), refresh — protected-route requests 403 `account_disabled` JSON; `/auth/logout` POST → 204 + cookie cleared. (Then re-enable yourself in DB and sign in again to recover.)

- [ ] **Step 4: Commit.**

```bash
git add pkg/auth/middleware.go pkg/auth/middleware_test.go
git commit -m "fix(auth): disabled middleware uses JSON envelope; public routes bypass"
```

---

## Task 3: Admin-cannot-be-disabled rule (C3)

**Goal:** Server rejects any update that would result in `role=admin AND disabled=true`. Dashboard hides the disable toggle on admin rows. Operators who want to disable an admin must demote them to `user` first.

**Files:**
- Modify: `pkg/auth/errors.go` — add `ErrAdminCannotBeDisabled()` constructor.
- Modify: `pkg/server/handle_account.go::handleUpdateAccount` — reject the forbidden combination before opening the TX.
- Modify: `dashboard/src/views/AccountsView.vue` — hide the disable IconButton when `a.role === 'admin'`.
- Modify: `dashboard/src/components/AccountForm.vue` — disable the "disabled" checkbox when the form's role is admin; clear `form.disabled` if user flips role from user→admin.

**Acceptance Criteria:**
- [ ] `PUT /accounts/{id}` with `{role: "admin", disabled: true}` → 409 `admin_cannot_be_disabled`.
- [ ] Admin rows in AccountsView don't show the enable/disable IconButton.
- [ ] AccountForm: while role is "admin", the disabled checkbox is disabled (greyed out) and unchecked.
- [ ] Going from user→admin in the form clears any pre-existing `disabled: true`.

**Verify:** Manually via dashboard. Server: `curl -X PUT .../accounts/1 -d '{"role":"admin","disabled":true,"displayName":"x","permissions":{...}}'` (with admin cookie) → 409.

**Steps:**

- [ ] **Step 1: Add the error.** In `pkg/auth/errors.go`, near `ErrLastAdmin`:

```go
// ErrAdminCannotBeDisabled rejects an update that would leave an account in
// the role=admin AND disabled=true state. Admins must be demoted to 'user'
// before they can be disabled. Keeps the active-admin set cleanly defined.
func ErrAdminCannotBeDisabled() *AuthError {
	return &AuthError{Code: "admin_cannot_be_disabled", Status: 409,
		Message: "admin accounts cannot be disabled; demote to user first"}
}
```

- [ ] **Step 2: Enforce in the handler.** In `pkg/server/handle_account.go::handleUpdateAccount`, immediately after the displayName validation and BEFORE the TX opens:

```go
if in.Body.Role == "admin" && in.Body.Disabled {
	return nil, authErrToHuma(auth.ErrAdminCannotBeDisabled())
}
```

- [ ] **Step 3: Update AccountsView.** Hide the toggle:

```vue
<!-- dashboard/src/views/AccountsView.vue, in the row actions block -->
<IconButton
  v-if="a.role !== 'admin'"
  :title="a.disabled ? '启用' : '禁用'"
  :aria-label="a.disabled ? '启用' : '禁用'"
  @click="confirmToggleDisabled(a)"
>
  <Icon :name="a.disabled ? 'eye-off' : 'eye'" :size="13" />
</IconButton>
```

- [ ] **Step 4: Update AccountForm.** Add a `<watch>` or computed that forces `form.disabled = false` when role is admin, and disable the checkbox:

```vue
<!-- dashboard/src/components/AccountForm.vue, in the form template -->
<label class="flex items-center gap-2 text-sm text-ink-muted cursor-pointer">
  <input
    v-model="form.disabled"
    :disabled="form.role === 'admin'"
    type="checkbox"
    class="accent-accent"
  />
  <span :class="form.role === 'admin' ? 'text-ink-faint' : ''">
    禁用账户
    <span v-if="form.role === 'admin'" class="text-2xs">（管理员不可禁用）</span>
  </span>
</label>
```

And in the script setup, add a watch so flipping to admin clears the flag:

```ts
import { watch } from 'vue'
// ...
watch(
  () => form.value.role,
  (role) => {
    if (role === 'admin' && form.value.disabled) form.value.disabled = false
  },
)
```

- [ ] **Step 5: Verify.**

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
```

- [ ] **Step 6: Commit.**

```bash
git add pkg/auth/errors.go pkg/server/handle_account.go dashboard/src/views/AccountsView.vue dashboard/src/components/AccountForm.vue
git commit -m "feat(auth): admins cannot be disabled — demote first"
```

---

## Task 4: Invite-as-template schema (I1, migration only)

**Goal:** Migration 028 adds the columns needed to carry an invite's role + permissions template without creating an account row up-front. Bootstrap and reset behavior unchanged.

**Files:**
- Create: `db/migrations/028_invite_template.sql`
- Modify: `db/queries/enrollment.sql` — update `InsertEnrollment` to take the new template columns.
- Regenerate: `pkg/db/enrollment.sql.go`.

**Acceptance Criteria:**
- [ ] Migration adds `template_role TEXT NULL`, `template_can_view_own_usage BOOLEAN NULL`, `template_can_manage_own_api_keys BOOLEAN NULL`, `template_can_view_models BOOLEAN NULL`, `template_can_view_own_traces BOOLEAN NULL`, `template_username TEXT NULL`, `template_display_name TEXT NULL` to `enrollment`. All nullable.
- [ ] The existing CHECK constraint on `enrollment` is updated: invite no longer requires `target_account_id IS NOT NULL`. Reset still does. Bootstrap still requires it to be NULL.
- [ ] `InsertEnrollment` accepts the new template params (all nullable). Tests pass.
- [ ] Down migration cleanly removes the columns and restores the original CHECK.

**Verify:** `MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...` clean. Apply + roll back the migration locally: `go run ./cmd/picotera/main.go` (which auto-applies) then manually `goose -dir db/migrations down` to verify roll-back.

**Steps:**

- [ ] **Step 1: Write the migration.** `db/migrations/028_invite_template.sql`:

```sql
-- +goose Up

ALTER TABLE enrollment
  ADD COLUMN template_role                     TEXT,
  ADD COLUMN template_can_view_own_usage       BOOLEAN,
  ADD COLUMN template_can_manage_own_api_keys  BOOLEAN,
  ADD COLUMN template_can_view_models          BOOLEAN,
  ADD COLUMN template_can_view_own_traces      BOOLEAN,
  ADD COLUMN template_username                 TEXT,
  ADD COLUMN template_display_name             TEXT;

-- Drop the old CHECK that required invite to have a target, and replace with
-- a constraint that only ties reset to target. Bootstrap still requires
-- target IS NULL. Invite no longer requires (or forbids) target.
ALTER TABLE enrollment DROP CONSTRAINT enrollment_intent_target_check;
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
ALTER TABLE enrollment ADD CONSTRAINT enrollment_intent_target_check CHECK (
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
```

Verify the constraint name `enrollment_intent_target_check` matches what migration 027 declared. If 027 used an anonymous constraint, this needs adjusting — read `db/migrations/027_auth_system.sql` and use the actual name. If it's anonymous, `ALTER TABLE enrollment DROP CONSTRAINT <generated_name>` won't work; instead query `pg_constraint` to find the name, or use the table-level CHECK pattern via a column-less constraint that the migration named explicitly. Easiest fallback: the 027 migration likely added it inline; rename or rewrite as needed.

If 027's CHECK is anonymous, prepend this to the Up section:

```sql
DO $$
DECLARE c text;
BEGIN
  SELECT conname INTO c FROM pg_constraint
    WHERE conrelid = 'enrollment'::regclass AND contype = 'c'
      AND pg_get_constraintdef(oid) LIKE '%intent = ''bootstrap''%';
  IF c IS NOT NULL THEN
    EXECUTE 'ALTER TABLE enrollment DROP CONSTRAINT ' || quote_ident(c);
  END IF;
END$$;
```

Then add the new named constraint. Apply the inverse in Down. (Confirm by inspecting 027 first.)

- [ ] **Step 2: Update sqlc query.** Change `InsertEnrollment`:

```sql
-- name: InsertEnrollment :one
INSERT INTO enrollment (
  token, intent, target_account_id, expires_at,
  template_role,
  template_can_view_own_usage, template_can_manage_own_api_keys,
  template_can_view_models, template_can_view_own_traces,
  template_username, template_display_name
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;
```

- [ ] **Step 3: Regenerate + verify.**

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- sqlc generate
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
```

This will FAIL on `pkg/auth/enrollment.go::IssueEnrollment` (the existing helper) and any callers. Don't fix in this task — Task 5 wires it. Instead, in this task only, add the new optional params with default-zero values to `IssueEnrollment` so the call sites still compile:

```go
// IssueEnrollment now accepts an optional template. Callers that don't have
// one pass nil. Task 5 wires invite handlers to use this.
type EnrollmentTemplate struct {
	Role        string
	Perms       contract.Permissions
	Username    string // optional suggestion
	DisplayName string // optional suggestion
}

func IssueEnrollment(
	ctx context.Context,
	q db.Querier,
	intent string,
	targetAccountID *int32,
	ttl time.Duration,
	tpl *EnrollmentTemplate, // NEW — nil for bootstrap/reset
) (string, time.Time, error) {
	// existing body, but populate the new template_* columns via tpl when set.
	// All template columns are NULL when tpl == nil.
}
```

Update all call sites to pass `nil`:
- `cmd/picotera/main.go` (3 IssueEnrollment calls for --reset / --new / default).
- `pkg/server/handle_enrollment.go` if any.
- `pkg/server/handle_account.go::handleReissueEnrollment` and `handleCreateInvitation`.

The invite handler will use the real template in Task 5; for now it can pass `nil` so the build is clean — Task 5 immediately replaces this.

- [ ] **Step 4: Commit.**

```bash
git add db/migrations/028_invite_template.sql db/queries/enrollment.sql pkg/db/ pkg/auth/enrollment.go cmd/picotera/main.go pkg/server/handle_enrollment.go pkg/server/handle_account.go
git commit -m "feat(auth): schema for invite-as-template (migration 028)"
```

---

## Task 5: Invite-as-template backend handlers (I1)

**Goal:** Invite flow no longer creates the account at invite time. Account is created when the invitee consumes the URL, using the template's role + permissions and the invitee's chosen username + display name.

**Files:**
- Modify: `pkg/contract/auth.go` — `InvitationResponse` no longer carries an `Account`; the account doesn't exist yet at invite time. Replace with a simpler shape OR drop the field.
- Modify: `pkg/server/handle_account.go::handleCreateInvitation` — drop the account insert; just call `IssueEnrollment` with the template.
- Modify: `pkg/server/handle_enrollment.go::handlePreviewEnrollment` — for invite intent, return `target` populated from the template's suggested username/displayName (or absent if not suggested).
- Modify: `pkg/server/handle_enrollment.go` — `enrollBeginBody` now carries `username + displayName` for invite intent too (not only bootstrap). `handleEnrollmentBeginHTTP` invite branch validates them.
- Modify: `pkg/server/handle_enrollment.go::handleEnrollmentCompleteHTTP` — invite branch creates the account in the TX from the ceremony stash, using the consumed enrollment's template_role + template_can_*. The bootstrap branch stays as-is.
- Modify: `enrollCeremonyStash` to carry an `Invite` field analogous to `Bootstrap` (username + displayName + webauthn_user_handle).

**Acceptance Criteria:**
- [ ] `POST /api/picotera/invitations` does NOT insert an `account` row.
- [ ] `GET /api/picotera/enrollments/{token}` for invite intent returns `EnrollmentPreview { intent: "invite", target: {username, displayName}?, expiresAt }` — target is present only if admin provided a template username/displayName, otherwise absent.
- [ ] `POST /api/picotera/enrollments/{token}/register/begin` for invite intent accepts `{username, displayName}`, validates them, and stashes them for complete.
- [ ] `POST /api/picotera/enrollments/{token}/register/complete` for invite intent creates an account using the template's role + permissions + the stash's username + displayName, then inserts the credential, then commits.
- [ ] Username collision at consume time returns `409 username_taken` and the enrollment row is NOT consumed (rolled back).
- [ ] `InvitationResponse` returned from `/invitations` no longer contains `account`. The new shape is `{ url, expiresAt, templateUsername?: string }` (the template username, echoed back so admin can copy/share if they pinned one).
- [ ] OpenAPI regenerates cleanly. Dashboard types update.

**Verify:** Manually: admin creates an invitation with `{username: "alice", displayName: "Alice", role: "user", permissions: {...}}` → reveal-once URL appears (no account row in DB). Open URL in private window → form prefilled with "alice"/"Alice", invitee can change them. Submit → account row appears post-ceremony with the typed values + template role/perms.

**Steps:**

- [ ] **Step 1: Update `pkg/contract/auth.go`.**

```go
// InvitationResponse is returned by POST /invitations. The account does NOT
// exist yet — it's created when the invitee consumes the enrollment URL.
// Reveal-once: the URL is never retrievable from any other endpoint after this
// response.
type InvitationResponse struct {
	URL              string    `json:"url"`
	ExpiresAt        time.Time `json:"expiresAt"`
	TemplateUsername string    `json:"templateUsername,omitempty"`
}
```

(Drop the `Account AccountView` field. This is a breaking API change to the unreleased invite endpoint — dashboard is the only consumer and updates in Task 6.)

- [ ] **Step 2: Update `handleCreateInvitation`.**

```go
func (s *Server) handleCreateInvitation(ctx context.Context, in *createInvitationIn) (*invitationOut, error) {
	if err := auth.ValidateUsername(in.Body.Username); err != nil {
		return nil, authErrToHuma(err)
	}
	if err := auth.ValidateDisplayName(in.Body.DisplayName); err != nil {
		return nil, authErrToHuma(err)
	}
	// Pre-check uniqueness for the template username. It's a soft reservation
	// — the actual UNIQUE check fires at consume time, so two simultaneous
	// invites with the same template username can both succeed; one of them
	// will then fail at consume. That's fine; admin sees the dup at consume.
	if _, err := s.queries.GetAccountByUsername(ctx, in.Body.Username); err == nil {
		return nil, authErrToHuma(auth.ErrUsernameTaken())
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("handleCreateInvitation: precheck username: %w", err)
	}

	tpl := &auth.EnrollmentTemplate{
		Role:        in.Body.Role,
		Perms:       in.Body.Permissions,
		Username:    in.Body.Username,
		DisplayName: in.Body.DisplayName,
	}
	token, exp, err := auth.IssueEnrollment(ctx, s.queries, auth.IntentInvite, nil, 0, tpl)
	if err != nil {
		return nil, fmt.Errorf("handleCreateInvitation: issue: %w", err)
	}
	url := s.config.PublicOrigins[0] + "/enroll/" + token
	return &invitationOut{
		Body: contract.InvitationResponse{
			URL:              url,
			ExpiresAt:        exp,
			TemplateUsername: in.Body.Username,
		},
	}, nil
}
```

Note: no transaction needed now (no account insert).

- [ ] **Step 3: Update `handlePreviewEnrollment`.** When intent is invite, populate `Target` from the template fields (if set), not from the account row:

```go
func (s *Server) handlePreviewEnrollment(ctx context.Context, in *previewIn) (*previewOut, error) {
	e, err := auth.LoadEnrollment(ctx, s.queries, in.Token)
	if err != nil {
		return nil, authErrToHuma(err)
	}
	out := contract.EnrollmentPreview{
		Intent:    e.Intent,
		ExpiresAt: e.ExpiresAt.Time,
	}
	switch e.Intent {
	case auth.IntentBootstrap:
		// no target
	case auth.IntentInvite:
		// Target is the template suggestion if admin supplied one. The invitee
		// may override at register/begin time.
		if e.TemplateUsername.Valid && e.TemplateDisplayName.Valid {
			out.Target = &contract.EnrollmentTarget{
				Username:    e.TemplateUsername.String,
				DisplayName: e.TemplateDisplayName.String,
			}
		}
	case auth.IntentReset:
		if e.TargetAccountID.Valid {
			if a, gerr := s.queries.GetAccountByID(ctx, e.TargetAccountID.Int32); gerr == nil {
				out.Target = &contract.EnrollmentTarget{
					Username:    a.Username,
					DisplayName: a.DisplayName,
				}
			}
			// (Server errors here are swallowed — if the row is gone, the
			// consume will fail with enrollment_consumed anyway. Don't leak
			// 500 from the preview path.)
		}
	}
	return &previewOut{Body: out}, nil
}
```

- [ ] **Step 4: Update `enrollBeginBody` + the invite branch of `handleEnrollmentBeginHTTP`.**

The body shape now requires username+displayName for both bootstrap and invite (reset stays empty):

```go
type enrollBeginBody struct {
	Username    string `json:"username,omitempty"`    // bootstrap + invite
	DisplayName string `json:"displayName,omitempty"` // bootstrap + invite
}
```

Add an `Invite` field to the stash:

```go
type enrollCeremonyStash struct {
	Data      webauthn.SessionData `json:"data"`
	Bootstrap *bootstrapCeremony   `json:"bootstrap,omitempty"`
	Invite    *inviteCeremony      `json:"invite,omitempty"`
}

type inviteCeremony struct {
	Username           string `json:"username"`
	DisplayName        string `json:"displayName"`
	WebauthnUserHandle []byte `json:"webauthn_user_handle"`
}
```

Replace the invite branch in `handleEnrollmentBeginHTTP`:

```go
case auth.IntentInvite:
	if err := auth.ValidateUsername(body.Username); err != nil {
		writeAuthErr(w, err)
		return
	}
	if err := auth.ValidateDisplayName(body.DisplayName); err != nil {
		writeAuthErr(w, err)
		return
	}
	// Soft pre-check for uniqueness. The hard check is at consume time inside
	// the TX so two invitees racing on the same username produce a clean 409
	// on the loser rather than two accounts.
	if _, err := s.queries.GetAccountByUsername(r.Context(), body.Username); err == nil {
		writeAuthErr(w, auth.ErrUsernameTaken())
		return
	} else if !errors.Is(err, pgx.ErrNoRows) {
		writeAuthErr(w, fmt.Errorf("enrollment/begin invite: precheck: %w", err))
		return
	}
	handle, err := auth.GenerateUserHandle()
	if err != nil {
		writeAuthErr(w, err)
		return
	}
	wu = &auth.WebAuthnAccount{
		Account: &db.Account{
			ID:                 0,
			Username:           body.Username,
			DisplayName:        body.DisplayName,
			WebauthnUserHandle: handle,
			Role:               e.TemplateRole.String, // from the template
		},
	}
	stash.Invite = &inviteCeremony{
		Username:           body.Username,
		DisplayName:        body.DisplayName,
		WebauthnUserHandle: handle,
	}
```

- [ ] **Step 5: Update the invite branch of `handleEnrollmentCompleteHTTP`.** Already inside the post-Task-1 atomic-consume TX. Build the account from `consumed.TemplateRole`/`TemplateCan*` + `stash.Invite`:

```go
case auth.IntentInvite:
	if stash.Invite == nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony("missing invite ceremony state"))
		return
	}
	wu := &auth.WebAuthnAccount{Account: &db.Account{
		Username:           stash.Invite.Username,
		DisplayName:        stash.Invite.DisplayName,
		WebauthnUserHandle: stash.Invite.WebauthnUserHandle,
	}}
	cred, err := s.webauthn.CreateCredential(wu, stash.Data, parsed)
	if err != nil {
		writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error()))
		return
	}
	role := "user"
	if consumed.TemplateRole.Valid {
		role = consumed.TemplateRole.String
	}
	a, err := qtx.InsertAccount(r.Context(), db.InsertAccountParams{
		Username:            stash.Invite.Username,
		DisplayName:         stash.Invite.DisplayName,
		WebauthnUserHandle:  stash.Invite.WebauthnUserHandle,
		Role:                role,
		CanViewOwnUsage:     consumed.TemplateCanViewOwnUsage.Bool,
		CanManageOwnApiKeys: consumed.TemplateCanManageOwnApiKeys.Bool,
		CanViewModels:       consumed.TemplateCanViewModels.Bool,
		CanViewOwnTraces:    consumed.TemplateCanViewOwnTraces.Bool,
		Disabled:            false,
	})
	if err != nil {
		// Task 7 will replace this with specific 23505 detection.
		writeAuthErr(w, auth.ErrUsernameTaken())
		return
	}
	account = a
	if _, err := insertCredentialForTx(qtx, r.Context(), a.ID, cred); err != nil {
		writeAuthErr(w, fmt.Errorf("enrollment/complete: insert credential: %w", err))
		return
	}
```

- [ ] **Step 6: Regenerate OpenAPI + dashboard types.**

```bash
MISE_DISABLE_TOOLS=pnpm mise run openapi
pnpm --dir dashboard generate-openapi
```

- [ ] **Step 7: Verify backend.**

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
```

- [ ] **Step 8: Commit.**

```bash
git add pkg/contract/auth.go pkg/server/handle_account.go pkg/server/handle_enrollment.go openapi.yaml dashboard/src/openapi-types.d.ts
git commit -m "feat(auth): invite-as-template — account created at consume time"
```

---

## Task 6: Invite-as-template dashboard UX (I1)

**Goal:** AccountForm invite mode no longer asks for username/displayName; admin sets only role + permissions (and optional suggested username/displayName as a template). EnrollView invite branch shows an editable username + displayName form (matching bootstrap) prefilled from `preview.target` if present.

**Files:**
- Modify: `dashboard/src/components/AccountForm.vue` — invite mode is now "role + permissions + optional template username/display name" only. Edit mode unchanged.
- Modify: `dashboard/src/views/EnrollView.vue` — invite branch renders the full bootstrap-style form (username pattern + displayName) instead of the previous read-only display. Prefill from `preview.target` if set; otherwise leave blank.
- Modify: `dashboard/src/api/client.ts` — `createInvitation` request body unchanged (still `username + displayName + role + permissions`); response shape no longer has `account` — adjust if any consumer reads it.
- Modify: `dashboard/src/views/AccountsView.vue` — the invite success doesn't show "新建账户" in the list immediately (because the account doesn't exist yet). The dashboard should not refetch the accounts list after invite success — only after the invitee consumes. OR (simpler) we keep the invalidation but the list won't have a new row until then. Pick the simpler "keep the invalidate" approach.

**Acceptance Criteria:**
- [ ] AccountForm invite mode still asks for username + displayName + role + permissions; backend treats them as template, dashboard label them as "建议用户名/显示名（受邀者可修改）".
- [ ] EnrollView invite branch renders username + displayName inputs (editable). Prefilled from `preview.target` when admin supplied template values. Pattern + required validation match bootstrap (the `\-` fix from earlier applies).
- [ ] After invite success, AccountsView's accounts list doesn't show a new row until the invitee actually registers.
- [ ] type-check + build + lint clean.

**Verify:** Manual smoke: invite "alice" with role=user, permissions=manage_own_api_keys. URL appears. Open in private window. Form shows "alice" prefilled, invitee changes to "alicia". Submits. Backend creates account "alicia" with the template's permissions. Admin's AccountsView list refresh now shows "alicia".

**Steps:**

- [ ] **Step 1: Update AccountForm labels.** Inside the invite-mode form section, change the username + displayName field labels to clarify they're suggestions:

```vue
<Field label="建议用户名（受邀者可修改）">
  <Input
    v-model.trim="form.username"
    mono
    required
    pattern="[a-z0-9_\-]{2,32}"
    placeholder="例如 alice"
  />
</Field>
<Field label="建议显示名（受邀者可修改）">
  <Input v-model.trim="form.displayName" required maxlength="80" />
</Field>
```

Edit-mode labels stay as they are (those are the actual account's username/displayName).

- [ ] **Step 2: Update EnrollView invite branch.** Replace the read-only display with an editable form, mirroring bootstrap:

```vue
<form
  v-else-if="preview.data.value?.intent === 'invite'"
  class="flex flex-col gap-4"
  @submit="onSubmit"
>
  <h1 class="text-xl font-semibold text-ink">接受邀请</h1>
  <p v-if="hasTemplate" class="text-sm text-ink-muted">
    管理员建议了用户名和显示名，你可以直接使用或修改。
  </p>
  <Field label="用户名">
    <Input
      v-model.trim="inviteForm.username"
      mono
      required
      pattern="[a-z0-9_\-]{2,32}"
    />
  </Field>
  <Field label="显示名">
    <Input v-model.trim="inviteForm.displayName" required maxlength="80" />
  </Field>
  <Button type="submit" :disabled="submitDisabled">注册 Passkey</Button>
  <p v-if="errorMessage" class="text-sm text-err">{{ errorMessage }}</p>
</form>
```

Add to the script setup:

```ts
const inviteForm = reactive({ username: '', displayName: '' })
const hasTemplate = computed(() => {
  const t = preview.data.value?.target
  return !!t && (t.username || t.displayName)
})

// When preview resolves, prefill from template (if present).
watchEffect(() => {
  const t = preview.data.value?.target
  if (t) {
    if (!inviteForm.username) inviteForm.username = t.username ?? ''
    if (!inviteForm.displayName) inviteForm.displayName = t.displayName ?? ''
  }
})
```

Update the `mutationFn` to use `inviteForm` for the invite body:

```ts
const body =
  intent === 'bootstrap'
    ? { username: bootstrapForm.username, displayName: bootstrapForm.displayName }
    : intent === 'invite'
      ? { username: inviteForm.username, displayName: inviteForm.displayName }
      : {}
```

- [ ] **Step 3: Reset-intent unchanged.** The reset form still displays the target account as read-only; the body stays `{}`.

- [ ] **Step 4: AccountsView invalidation.** No change required — the existing `invalidateAccounts(qc)` after invite success is fine; the list just won't include the new account until consume. Optionally add a small note in the reveal-once panel: "受邀者注册后将出现在用户列表中。"

- [ ] **Step 5: Verify.**

```bash
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
```

Manual smoke per the Verify section above.

- [ ] **Step 6: Commit.**

```bash
git add dashboard/src/components/AccountForm.vue dashboard/src/views/EnrollView.vue dashboard/src/views/AccountsView.vue
git commit -m "feat(dashboard): editable username/displayName during invite consume"
```

---

## Task 7: Specific 23505 / not-found error mapping (I2)

**Goal:** Stop pretending every `InsertAccount` failure means "username taken" and every `GetAccountByID` failure means "enrollment consumed." Use `pgconn.PgError.Code == "23505"` to detect the unique-violation; map other errors to a wrapped 500.

**Files:**
- Modify: `pkg/server/handle_enrollment.go` — replace blanket `auth.ErrUsernameTaken()` at the `InsertAccount` site (bootstrap branch + new invite branch from Task 5); replace blanket `auth.ErrEnrollmentConsumed()` at the `GetAccountByID` sites with a not-found + 500 split.
- Modify: `pkg/server/handle_account.go` — replace blanket `auth.ErrUsernameTaken()` at the `InsertAccount` site in `handleCreateInvitation` (per Task 5 this site moves; only the precheck call remains, which is already correctly mapping).

Note: extract `isUniqueViolation` somewhere reusable. Cheapest: copy the function from `handle_api_key.go:24-31` into a new `pkg/server/pgerr.go`, or just duplicate it in `handle_account.go` / `handle_enrollment.go`. Pick duplication — three sites, three lines each, clear intent.

**Acceptance Criteria:**
- [ ] In bootstrap and invite consume paths, `InsertAccount` errors are distinguished: `23505` → `ErrUsernameTaken`; everything else → wrapped 500 with the original error.
- [ ] `GetAccountByID` failures in invite/reset consume paths return 404-style `account_not_found` only on `pgx.ErrNoRows`; other errors surface as 500.
- [ ] Existing happy path unchanged.

**Verify:** `MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...` + `go test`. Manually: try to use a reset URL after the target account is deleted (race) → should get `account_not_found` or `enrollment_consumed`, not a misleading 500.

**Steps:**

- [ ] **Step 1: Extract a shared helper.** Create `pkg/server/pgerr.go`:

```go
package server

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation returns true iff err carries Postgres SQLSTATE 23505.
// Pulled out of handle_api_key.go so all "is this a dup-key constraint?"
// branches share one canonical check.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	// Defensive fallback for adapters that wrap the SQLSTATE differently.
	return strings.Contains(err.Error(), "23505")
}
```

Delete the duplicate in `handle_api_key.go`.

- [ ] **Step 2: Fix bootstrap-branch InsertAccount in `handle_enrollment.go`.**

```go
a, err := qtx.InsertAccount(r.Context(), db.InsertAccountParams{...})
if err != nil {
	if isUniqueViolation(err) {
		writeAuthErr(w, auth.ErrUsernameTaken())
		return
	}
	writeAuthErr(w, fmt.Errorf("enrollment/complete bootstrap: insert account: %w", err))
	return
}
```

Same for the invite branch.

- [ ] **Step 3: Fix `GetAccountByID` in reset branch.**

```go
case auth.IntentReset:
	if !consumed.TargetAccountID.Valid {
		writeAuthErr(w, auth.ErrEnrollmentConsumed())
		return
	}
	a, err := qtx.GetAccountByID(r.Context(), consumed.TargetAccountID.Int32)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeAuthErr(w, auth.ErrAccountNotFound())
			return
		}
		writeAuthErr(w, fmt.Errorf("enrollment/complete reset: get account: %w", err))
		return
	}
	// ... rest unchanged
```

Same for the invite branch's `GetAccountByID` if Task 5 still has any (it shouldn't — invite no longer reads an existing account; it creates one).

- [ ] **Step 4: Fix invitation pre-check in handleCreateInvitation.** The current pattern is fine (it maps `nil` → `ErrUsernameTaken`, `pgx.ErrNoRows` → continue, anything else → 500). Verify after Task 5 changes.

- [ ] **Step 5: Verify.**

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
```

- [ ] **Step 6: Commit.**

```bash
git add pkg/server/pgerr.go pkg/server/handle_api_key.go pkg/server/handle_enrollment.go pkg/server/handle_account.go
git commit -m "fix(auth): map only PG 23505 to username_taken; other errors propagate as 500"
```

---

## Task 8: Audit logs (I4)

**Goal:** Auth lifecycle events promised by `design.md §9` actually get logged. Operators have a forensic trail.

**Files:**
- Modify: `pkg/server/handle_auth.go` — log `auth.login_success` (with account_id, username, ip) on successful login complete; `auth.login_failure` (with reason, ip) on ceremony failure / disabled / etc.
- Modify: `pkg/server/handle_enrollment.go` — log `auth.enrollment_consumed` (with intent, account_id, token-prefix) on successful consume.
- Modify: `pkg/server/handle_account.go` — log on disable/enable, role change, delete, revoke-sessions, reissue-enrollment, create-invitation. Include the acting admin's id (from `auth.SessionFromContext`) and the target account id.
- Modify: `pkg/server/handle_me.go` — log `auth.credential_added` and `auth.credential_revoked` (with account_id, credential_id_suffix).

**Acceptance Criteria:**
- [ ] Every named event is emitted via `logx.WithContext(ctx).WithFields(...).Info(...)`.
- [ ] Fields are structured (no string-formatting key-value pairs in the message).
- [ ] Tokens, raw credential IDs, public keys, and other sensitive blobs are NEVER logged — only id integers + a short suffix when needed.

**Verify:** Tail `/tmp/picotera-dev.log` while running a manual smoke: bootstrap → login → invite → disable → role-change. Verify each event line appears with structured fields.

**Steps:**

- [ ] **Step 1: Pick the canonical event names.** Use a single naming scheme: `auth.<verb>_<noun>`.

| Event name | Site |
|---|---|
| `auth.login_success` | `handle_auth.go::handleLoginCompleteHTTP` |
| `auth.login_failure` | `handle_auth.go::handleLoginCompleteHTTP` (every writeAuthErr path) |
| `auth.logout` | `handle_auth.go::handleLogoutHTTP` (when a session was actually revoked) |
| `auth.enrollment_issued` | `handle_account.go::handleCreateInvitation`, `handleReissueEnrollment`; `cmd/picotera/main.go::enrollCmd` |
| `auth.enrollment_consumed` | `handle_enrollment.go::handleEnrollmentCompleteHTTP` (intent + new/existing account_id) |
| `auth.credential_added` | `handle_me.go::handleCredentialRegisterCompleteHTTP` |
| `auth.credential_revoked_self` | `handle_me.go::handleDeleteMyCredential` |
| `auth.credential_revoked_admin` | `handle_account.go::handleDeleteAccountCredential` |
| `auth.account_invited` | `handle_account.go::handleCreateInvitation` |
| `auth.account_updated` | `handle_account.go::handleUpdateAccount` (only when role/disabled/permissions changed; not when just displayName) |
| `auth.account_deleted` | `handle_account.go::handleDeleteAccount` |
| `auth.sessions_revoked` | `handle_account.go::handleRevokeAccountSessions` |

- [ ] **Step 2: Implement.** Example, in `handleLoginCompleteHTTP` after the cookie is set:

```go
logx.WithContext(r.Context()).WithFields(logx.Fields{
	"event":       "auth.login_success",
	"account_id":  resolvedAccount.ID,
	"username":    resolvedAccount.Username,
	"client_ip":   auth.ClientIP(r, s.config.TrustProxy),
}).Info("auth")
```

For failure paths, before each `writeAuthErr`:

```go
logx.WithContext(r.Context()).WithFields(logx.Fields{
	"event":     "auth.login_failure",
	"reason":    "webauthn_ceremony", // or "no_session", "account_disabled", etc.
	"client_ip": auth.ClientIP(r, s.config.TrustProxy),
}).Warn("auth")
```

Do similar for the other sites. Read `pkg/logx/logx.go` to confirm the available helpers (`Fields`, `WithFields`, `WithContext`, `Info`, `Warn`).

Critical:
- Never log the raw enrollment token. Log a 4-char prefix at most: `"token_prefix": token[:4]`.
- Never log the credential's public key, attestation object, or assertion signature.
- Never log session tokens.
- DO log: account IDs, usernames, intent strings, IP, role/permission changes (before+after), credential id suffix.

- [ ] **Step 3: Account update event detail.** In `handleUpdateAccount`, after the commit, compare `current` and `updated`:

```go
changes := map[string]any{}
if current.Role != updated.Role {
	changes["role"] = map[string]string{"from": current.Role, "to": updated.Role}
}
if current.Disabled != updated.Disabled {
	changes["disabled"] = map[string]bool{"from": current.Disabled, "to": updated.Disabled}
}
// ... per-permission diffs
if len(changes) > 0 {
	logx.WithContext(ctx).WithFields(logx.Fields{
		"event":       "auth.account_updated",
		"actor_id":    sess.Account.ID,
		"target_id":   updated.ID,
		"changes":     changes,
	}).Info("auth")
}
```

- [ ] **Step 4: Verify.** Run a manual smoke (bootstrap → login → invite "bob" → bob consumes → disable bob → role-change carol → delete bob). Tail the log and confirm each event line.

- [ ] **Step 5: Commit.**

```bash
git add pkg/server/handle_auth.go pkg/server/handle_enrollment.go pkg/server/handle_account.go pkg/server/handle_me.go cmd/picotera/main.go
git commit -m "feat(auth): structured audit logs for the auth lifecycle"
```

---

## Task 9: "用户" rename + role label consistency (I6)

**Goal:** Resource label is "用户" everywhere in the dashboard (replacing "账户"). Role labels collapse to "标准用户" (replacing "标准" / "普通用户" / "用户").

**Files:**
- Modify: `dashboard/src/components/AppSidebar.vue` — nav label.
- Modify: `dashboard/src/layouts/AppLayout.vue` — pageMeta title + hint for the `accounts` route.
- Modify: `dashboard/src/views/AccountsView.vue` — count label, button text ("邀请用户" stays — that's already correct), empty state, row text.
- Modify: `dashboard/src/components/AccountForm.vue` — panel kicker + role label rendering.
- Modify: `dashboard/src/views/MeView.vue` — role label rendering.

DB/contract terminology (`account`, `'admin'|'user'`) stays — only Chinese chrome strings change.

**Acceptance Criteria:**
- [ ] `grep -r "账户" dashboard/src` returns zero hits (except possibly the `useSession.ts` cache-key namespace, which is `accounts` — that's a code identifier, not user-visible Chinese text).
- [ ] All role-label sites show "管理员" or "标准用户" — never "标准" alone, never "普通用户", never bare "用户".
- [ ] type-check + build + lint clean.

**Verify:** Open dashboard, visit `/me`, `/accounts`, the AccountForm panel; eyeball each surface.

**Steps:**

- [ ] **Step 1: Replace "账户" with "用户" in the four files.** Specific replacements:

- `dashboard/src/components/AppSidebar.vue`:
  ```ts
  { name: 'accounts', label: '用户', icon: 'users', requires: 'admin' },
  ```

- `dashboard/src/layouts/AppLayout.vue`:
  ```ts
  accounts: { title: '用户', hint: '今天蹬的都是谁' },
  me: { title: '我的账号', hint: '今天蹬的是自己' }, // 'me' uses 账号 (the user's own identity context)
  ```

  (Pick one of "我的账号" / "我的资料" / "我" for the `/me` page — "我的账户" is fine if you'd rather keep symmetry. The audit's "rename all 账户 to 用户" rule applies to the resource label; the `/me` page is "my account" which is a different concept. Recommend "我的账号" — 账号 is "account/profile" in Chinese, distinct from the 用户 resource list.)

- `dashboard/src/views/AccountsView.vue`:
  - `{{ count }} 个账户` → `{{ count }} 个用户`
  - `<Tag>已禁用</Tag>` — unchanged
  - 空状态 `暂无账户` → `暂无用户`
  - Confirm dialogs: "确定要删除账户「...」吗？" → "确定要删除用户「...」吗？"; "确定要禁用账户「...」吗？" → "确定要禁用用户「...」吗？"; etc.

- `dashboard/src/components/AccountForm.vue`:
  - Panel `kicker` "账户" → "用户"
  - "编辑账户" → "编辑用户"
  - The 禁用 checkbox label "禁用账户" → "禁用用户" (and from Task 3, the parenthetical "（管理员不可禁用）")

- [ ] **Step 2: Collapse role labels to "管理员" / "标准用户".**

- `AccountsView.vue`:
  ```vue
  <Tag :variant="a.role === 'admin' ? 'accent' : 'default'">
    {{ a.role === 'admin' ? '管理员' : '标准用户' }}
  </Tag>
  ```

- `AccountForm.vue` SegmentedControl options:
  ```ts
  const roleOptions = [
    { value: 'admin', label: '管理员' },
    { value: 'user', label: '标准用户' },
  ]
  ```

- `MeView.vue` `roleLabel` computed:
  ```ts
  const roleLabel = computed(() =>
    session.user.value?.role === 'admin' ? '管理员' : '标准用户',
  )
  ```

- `AppSidebar.vue` profile menu role line:
  ```ts
  const roleLabel = computed(() =>
    session.user.value?.role === 'admin' ? '管理员' : '标准用户',
  )
  ```

- [ ] **Step 3: Verify.**

```bash
grep -r "账户" dashboard/src | grep -v 'queryKeys.accounts\|api/accounts\|accountId\|accountID\|account_id' || echo "ok"
grep -r "普通用户\|^标准$\|标准[^用]" dashboard/src || echo "ok"
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
```

The first grep should return zero hits (other than identifier names). The second should return zero hits in template/JSX visible strings.

- [ ] **Step 4: Commit.**

```bash
git add dashboard/src/
git commit -m "feat(dashboard): rename 账户→用户; collapse role labels to 管理员/标准用户"
```

---

## Task 10: Prevent self-delete (I8)

**Goal:** Admin cannot delete their own account row. UI hides the delete button on the caller's own row.

**Files:**
- Modify: `pkg/auth/errors.go` — add `ErrCannotDeleteSelf()`.
- Modify: `pkg/server/handle_account.go::handleDeleteAccount` — reject `in.Body.ID == sess.Account.ID`.
- Modify: `dashboard/src/views/AccountsView.vue` — hide delete IconButton when `a.id === session.user.value?.accountId`.

**Acceptance Criteria:**
- [ ] `POST /accounts/delete` with `id == caller's id` → 409 `cannot_delete_self`.
- [ ] AccountsView row for the current user shows no delete button.

**Verify:** Manual.

**Steps:**

- [ ] **Step 1: Add the error.**

```go
// In pkg/auth/errors.go
func ErrCannotDeleteSelf() *AuthError {
	return &AuthError{Code: "cannot_delete_self", Status: 409,
		Message: "you cannot delete your own account; ask another admin"}
}
```

- [ ] **Step 2: Enforce in handler.**

```go
// In handleDeleteAccount, immediately after pulling sess:
sess := auth.SessionFromContext(ctx)
if in.Body.ID == sess.Account.ID {
	return nil, authErrToHuma(auth.ErrCannotDeleteSelf())
}
```

- [ ] **Step 3: Hide UI button.**

```vue
<!-- AccountsView.vue, in the row actions block -->
<IconButton
  v-if="a.id !== session.user.value?.accountId"
  title="删除"
  aria-label="删除"
  @click="confirmDelete(a)"
>
  <Icon name="trash" :size="13" />
</IconButton>
```

Add `useSession` import at the top of AccountsView if not already present:

```ts
import { useSession } from '@/composables/useSession'
// ...
const session = useSession()
```

- [ ] **Step 4: Verify.**

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
```

- [ ] **Step 5: Commit.**

```bash
git add pkg/auth/errors.go pkg/server/handle_account.go dashboard/src/views/AccountsView.vue
git commit -m "feat(auth): admin cannot delete their own account"
```

---

## Task 11: Post-enroll redirect + reveal-once close confirm (M5 + Q3)

**Goal:** A freshly enrolled non-admin lands on a page they actually have permission for (not `/overview`). Closing the reveal-once panel while a URL is visible requires confirmation.

**Files:**
- Create: `dashboard/src/router/fallback.ts` — extract `fallbackFor` from `guard.ts` so it can be reused.
- Modify: `dashboard/src/router/guard.ts` — use the new shared `fallbackFor`.
- Modify: `dashboard/src/views/EnrollView.vue` — `onSuccess` calls `router.replace(fallbackFor(session))` instead of `/overview`.
- Modify: `dashboard/src/views/LoginView.vue` — `onSuccess` uses `safeNext()` if a `next` is present, else `fallbackFor(session)`.
- Modify: `dashboard/src/components/AccountForm.vue` — when `revealData` is set, intercept the panel close (or the "完成" button click) with a `useConfirm` confirmation.

**Acceptance Criteria:**
- [ ] Non-admin invitee with `manage_own_api_keys` only → after registration lands on `/api-keys` (not `/overview`).
- [ ] Admin → after login lands on `/overview` (unchanged).
- [ ] Closing AccountForm while reveal URL is shown prompts "URL 将永久消失，确认关闭？". Confirming closes; cancelling keeps the panel open.

**Verify:** Manual smoke.

**Steps:**

- [ ] **Step 1: Extract `fallbackFor`.** Create `dashboard/src/router/fallback.ts`:

```ts
import type { components } from '@/openapi-types'

type SessionView = components['schemas']['SessionView']

/**
 * Pick the best landing page for the given session. Admins go to /overview.
 * Otherwise we route to the most useful page their permissions allow.
 */
export function fallbackFor(me: SessionView): string {
  if (me.role === 'admin') return '/overview'
  if (me.permissions.view_own_usage) return '/requests'
  if (me.permissions.manage_own_api_keys) return '/api-keys'
  return '/me'
}
```

- [ ] **Step 2: Update `guard.ts` to import the shared helper.** Delete the local `fallbackFor` definition; add `import { fallbackFor } from './fallback'`.

- [ ] **Step 3: Update `EnrollView.vue`.** Replace the hard-coded redirect:

```ts
import { fallbackFor } from '@/router/fallback'
// ...
onSuccess(session) {
  qc.setQueryData(queryKeys.session.current, session)
  router.replace(fallbackFor(session))
}
```

- [ ] **Step 4: Update `LoginView.vue` for symmetry.** Currently uses `safeNext()` which returns `/overview` by default for non-admin. Change to:

```ts
onSuccess(session) {
  qc.setQueryData(queryKeys.session.current, session)
  // Prefer route.query.next if it's a safe relative path; else fall back to
  // the session's natural landing page.
  const safe = safeNext()
  const target = safe === '/overview' && session.role !== 'admin' ? fallbackFor(session) : safe
  router.replace(target)
}
```

(`safeNext()` already rejects open-redirects; this just adds the role-aware fallback.)

- [ ] **Step 5: Reveal-once close confirm in `AccountForm.vue`.** When the form has `revealData` set, intercept close. Use `useConfirm`:

```ts
import { useConfirm } from '@/composables/useConfirm'
// ...
const confirm = useConfirm()

async function onClose() {
  if (revealData.value) {
    // Confirm before discarding the reveal-once URL.
    let confirmed = false
    confirm.require({
      message: 'URL 将永久消失，无法再次获取。确认关闭？',
      accept: () => { confirmed = true },
    })
    // useConfirm.require resolves via the accept callback; if not confirmed,
    // don't close. Since accept is sync we can check confirmed after the
    // dialog flow — but to keep behavior right, we change the template so
    // the close button calls onClose() instead of emit('close').
    if (!confirmed) return
  }
  emit('close')
}
```

Wait — `useConfirm.require` doesn't return a promise (looking at the existing API: it calls `accept` callback on confirmation). So a synchronous "did the user confirm" check won't work. Better pattern: move the emit('close') INTO the accept callback:

```ts
function onClose() {
  if (!revealData.value) {
    emit('close')
    return
  }
  confirm.require({
    message: 'URL 将永久消失，无法再次获取。确认关闭？',
    accept: () => emit('close'),
  })
}
```

Then in the template, replace the SidePanel's `@close="emit('close')"` (or wherever close is wired) with `@close="onClose"`. Same for the "完成" button — switch it from `@click="emit('close')"` to `@click="onClose"`.

Note: the SidePanel primitive's close behavior may be tricky to intercept — the X button in the header is owned by `<SidePanel>` itself. If `<SidePanel>` emits a `@close` event, wire that to `onClose`. If it directly calls panel-close internally, we need to either modify SidePanel to emit `close` first, OR we accept that "X close" skips the prompt and only "完成" guards it. Tradeoff: doing the proper intercept requires touching SidePanel (cross-cutting). Reasonable to ship the simpler version where only the "完成" button gates and the X is a fast-escape — document this in the WHY comment.

- [ ] **Step 6: Verify.**

```bash
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
```

Manual: invite a user, get URL, click "完成" → see confirmation prompt. Cancel → URL still visible. Confirm → panel closes; URL gone.

- [ ] **Step 7: Commit.**

```bash
git add dashboard/src/router/fallback.ts dashboard/src/router/guard.ts dashboard/src/views/EnrollView.vue dashboard/src/views/LoginView.vue dashboard/src/components/AccountForm.vue
git commit -m "feat(dashboard): fallbackFor on enroll/login; confirm-on-close for reveal-once URL"
```

---

## Verify-and-Ship checkpoint (Phase 4)

After all 11 tasks land:

```bash
# Backend
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge

# Frontend
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
```

Manual end-to-end smoke:

1. **C1 race**: open the bootstrap URL in two tabs, click both — one succeeds, other gets `enrollment_consumed`.
2. **C2 envelope**: log in as admin, disable yourself via DB UPDATE, refresh the dashboard — see a clean `account_disabled` error (not "请求失败").
3. **C3 admin disable**: try to disable an admin via PUT /accounts/{id} with `role=admin, disabled=true` → 409.
4. **I1 invite-as-template**: invite "alice" with template, open URL in private window, change username to "alicia", submit → account "alicia" appears in admin's list with the template's role/perms; no orphan "alice" row anywhere.
5. **I2 error precision**: simulate a DB error during invite (e.g. drop the network) → server returns a 500 with the wrapped error, not 409 username_taken.
6. **I4 audit**: tail `/tmp/picotera-dev.log` during the steps above; see structured `event` lines.
7. **I6 naming**: visit `/accounts` — title is "用户", count is "N 个用户", role labels are "管理员" / "标准用户".
8. **I8 self-delete**: as the only admin, try to delete yourself from the dashboard — no delete button on your own row; direct API call returns 409 `cannot_delete_self`.
9. **M5 fallback**: invite a non-admin with `manage_own_api_keys` only; consume URL; land on `/api-keys` (not `/overview`).
10. **Q3 reveal-close**: invite someone, click "完成" on the reveal panel → see confirmation prompt.

If all green, Phase 4 is shippable.

# Auth & Account System — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers-extended-cc:subagent-driven-development` (recommended) or `superpowers-extended-cc:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace picotera's currently-unauthenticated management API with a WebAuthn-gated dashboard backed by an account model, and add a small per-user permission system so non-admin users can see scoped views of their own usage / API keys / models.

**Architecture:** A new `pkg/auth` package wraps `github.com/go-webauthn/webauthn`, owns sessions in the existing `pkg/kv` layer, and exposes a `registerOp[I,O]` helper at the server layer that ties every Huma operation to a typed `AuthRequirement` (public / session / admin / permission). Account and credential state lives in three new tables; one nullable column links existing `api_key` rows to their owner. Dashboard adds a `useSession` composable, route-meta-driven layout selection, and four new views. The migration ships the full schema in one go; auth enforcement is on from Phase 1 even though only admin accounts exist initially, so Phases 2 and 3 are purely additive.

**Tech Stack:**
- Go: `github.com/go-webauthn/webauthn` (WebAuthn ceremonies), existing `pgx` + `sqlc`, Huma v2 + chi, Viper, cobra.
- Frontend: Vue 3 (beta, pinned) + vue-router 4 + `@tanstack/vue-query` + Tailwind v4 + `openapi-fetch` / `openapi-typescript`.
- Browser native: `navigator.credentials.create` / `.get`.
- DB: PostgreSQL via TimescaleDB image (existing).
- KV: existing `pkg/kv` (Redis when available, in-memory fallback).

**References:**
- `specs/2026-05-23-auth-system/proposal.md`
- `specs/2026-05-23-auth-system/design.md` — full design rationale
- `specs/2026-05-23-auth-system/api.md` — endpoint catalog
- `CLAUDE.md` — project conventions
- `specs/2026-05-03-api-key-management/` — closest analogous spec (auth-adjacent CRUD)

**Worktree note:** Implementation should happen in a dedicated git worktree (`EnterWorktree` or `git worktree add`) to keep the master branch clean while the multi-phase change progresses. The execution skill will offer this.

**Testing posture:** The project has no postgres-backed Go test harness (per `CLAUDE.md`). Unit tests live next to the code they test (`pkg/server/*_test.go`, `pkg/llmbridge/*_test.go`). For this plan:
- **pkg/auth helpers** get unit tests (no DB needed for token gen, KV operations using in-memory fallback, permission checks).
- **Handler integration** is verified via `curl` smoke tests against a running dev stack (the executor brings up `podman-compose` and runs the server).
- **Frontend** is verified via `pnpm --dir dashboard build`, `pnpm --dir dashboard type-check`, and manual browser walk-throughs against a local server.

**Database migration discipline:** Migration 027 ships in Phase 1. Down migration must reverse cleanly. Existing api_key rows survive with `account_id = NULL`.

---

## Phase 1 — Auth foundation

Closes the open-management-API hole. After Phase 1, operators must run `picotera enroll-admin` to use the dashboard. Only the admin role exists; permission gates are wired but dormant.

### Task 1: Database migration + sqlc scaffolding

**Goal:** Land the schema and generate the basic CRUD queries. No business logic yet.

**Files:**
- Create: `db/migrations/027_auth_system.sql`
- Create: `db/queries/account.sql`
- Create: `db/queries/webauthn_credential.sql`
- Create: `db/queries/enrollment.sql`
- Modify: `db/queries/api_key.sql` (no behavioral change yet — Phase 3 adds scoped variants)
- Regenerated: `pkg/db/*.go` (via `sqlc generate`)

**Acceptance Criteria:**
- [ ] `mise run server` (or equivalent) applies migration 027 cleanly on a fresh DB.
- [ ] `sqlc generate` produces typed methods on `Querier` for each new query.
- [ ] `go build ./...` passes.
- [ ] Down migration reverses without error against a populated DB.

**Verify:** `psql "$PICOTERA_DATABASE_URL" -c '\dt' | grep -E 'account|webauthn_credential|enrollment'` shows three new tables; `psql "$PICOTERA_DATABASE_URL" -c '\d api_key' | grep account_id` shows the new column.

**Steps:**

- [ ] **Step 1: Write the migration.**

Create `db/migrations/027_auth_system.sql`:

```sql
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
DROP TABLE enrollment;
DROP INDEX IF EXISTS webauthn_credential_account_idx;
DROP TABLE webauthn_credential;
DROP TABLE account;
```

- [ ] **Step 2: Write the sqlc query files.**

`db/queries/account.sql`:

```sql
-- name: GetAccountByID :one
SELECT * FROM account WHERE id = $1;

-- name: GetAccountByUsername :one
SELECT * FROM account WHERE username = $1;

-- name: GetAccountByWebauthnUserHandle :one
SELECT * FROM account WHERE webauthn_user_handle = $1;

-- name: InsertAccount :one
INSERT INTO account (
  username, display_name, webauthn_user_handle, role,
  can_view_own_usage, can_manage_own_api_keys, can_view_models, can_view_own_traces,
  disabled
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: HasAnyActiveAdmin :one
SELECT EXISTS(SELECT 1 FROM account WHERE role = 'admin' AND NOT disabled) AS bootstrapped;
```

`db/queries/webauthn_credential.sql`:

```sql
-- name: ListCredentialsByAccount :many
SELECT * FROM webauthn_credential WHERE account_id = $1 ORDER BY created_at DESC;

-- name: GetCredentialByCredentialID :one
SELECT * FROM webauthn_credential WHERE credential_id = $1;

-- name: InsertCredential :one
INSERT INTO webauthn_credential (
  account_id, credential_id, public_key, sign_count, transports,
  aaguid, attestation_type, backup_eligible, backup_state, nickname
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: UpdateCredentialUsage :exec
UPDATE webauthn_credential
SET sign_count = $2, last_used_at = now()
WHERE id = $1;

-- name: DeleteCredentialByID :exec
DELETE FROM webauthn_credential
WHERE id = $1 AND account_id = $2;

-- name: CountCredentialsByAccount :one
SELECT COUNT(*) FROM webauthn_credential WHERE account_id = $1;
```

`db/queries/enrollment.sql`:

```sql
-- name: GetEnrollmentByToken :one
SELECT * FROM enrollment WHERE token = $1;

-- name: InsertEnrollment :one
INSERT INTO enrollment (token, intent, target_account_id, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: MarkEnrollmentConsumed :exec
UPDATE enrollment SET consumed_at = now() WHERE token = $1;
```

- [ ] **Step 3: Run sqlc.**

```bash
sqlc generate
```

Expected: `pkg/db/account.sql.go`, `pkg/db/webauthn_credential.sql.go`, `pkg/db/enrollment.sql.go` created; `pkg/db/querier.go` extended.

- [ ] **Step 4: Verify build.**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 5: Bring up DB and run migration via server.**

```bash
podman-compose up -d
MISE_DISABLE_TOOLS=pnpm mise run server &
sleep 3
psql "$PICOTERA_DATABASE_URL" -c '\dt' | grep -E 'account|webauthn_credential|enrollment'
kill %1
```

Expected output includes three new tables.

- [ ] **Step 6: Commit.**

```bash
git add db/migrations/027_auth_system.sql db/queries/ pkg/db/ sqlc.yaml
git commit -m "feat(auth): add account, webauthn_credential, enrollment tables

Migration 027 introduces the schema for the WebAuthn-based admin auth and
non-admin user system. api_key gains a nullable account_id column with
ON DELETE SET NULL semantics so legacy keys are retained when their owner
is removed."
```

---

### Task 2: Config keys

**Goal:** Introduce the four new env-backed config knobs that the auth code will read at startup.

**Files:**
- Modify: `pkg/configx/configx.go`

**Acceptance Criteria:**
- [ ] `configx.Config` exposes `PublicOrigins []string`, `WebAuthnRPID string`, `SessionTTL time.Duration`, `TrustProxy bool`.
- [ ] `PICOTERA_PUBLIC_ORIGIN=https://a,https://b` parses into `["https://a", "https://b"]`.
- [ ] Defaults: `PublicOrigins = ["http://localhost:9898"]`, `WebAuthnRPID = "localhost"`, `SessionTTL = 24h`, `TrustProxy = false`.
- [ ] `PICOTERA_SESSION_TTL=12h` parses as a duration.

**Verify:** Add a temporary `fmt.Printf("%+v", cfg)` line in `cmd/picotera/main.go`'s `OnStart`, `mise run server`, observe values; remove the line.

**Steps:**

- [ ] **Step 1: Add fields and parsing.**

Modify `pkg/configx/configx.go` — add to the `Config` struct:

```go
type Config struct {
    // ... existing fields ...

    PublicOrigins   []string      `mapstructure:"public_origin"`
    WebAuthnRPID    string        `mapstructure:"webauthn_rp_id"`
    SessionTTL      time.Duration `mapstructure:"session_ttl"`
    TrustProxy      bool          `mapstructure:"trust_proxy"`
}
```

In the `Parse()` function, after the existing `viper.SetDefault` calls, add:

```go
viper.SetDefault("public_origin", "http://localhost:9898")
viper.SetDefault("webauthn_rp_id", "localhost")
viper.SetDefault("session_ttl", 24*time.Hour)
viper.SetDefault("trust_proxy", false)
```

`PICOTERA_PUBLIC_ORIGIN` carries a comma-separated list. Viper handles single strings; for the list, after `viper.Unmarshal`, post-process:

```go
if raw := viper.GetString("public_origin"); raw != "" {
    cfg.PublicOrigins = nil
    for _, s := range strings.Split(raw, ",") {
        s = strings.TrimSpace(s)
        if s != "" {
            cfg.PublicOrigins = append(cfg.PublicOrigins, s)
        }
    }
}
if cfg.WebAuthnRPID == "" && len(cfg.PublicOrigins) > 0 {
    if u, err := url.Parse(cfg.PublicOrigins[0]); err == nil {
        cfg.WebAuthnRPID = u.Hostname()
    }
}
```

(Imports: `strings`, `net/url`, `time`.)

- [ ] **Step 2: Build.**

```bash
go build ./...
```

- [ ] **Step 3: Smoke-check defaults.**

Temporarily add to `cmd/picotera/main.go` after `configx.Parse()` (or in `server.NewServer`):

```go
fmt.Printf("auth-config: PublicOrigins=%v RPID=%s SessionTTL=%s TrustProxy=%t\n",
    cfg.PublicOrigins, cfg.WebAuthnRPID, cfg.SessionTTL, cfg.TrustProxy)
```

Run `MISE_DISABLE_TOOLS=pnpm mise run server` with no auth env vars; expect:

```
auth-config: PublicOrigins=[http://localhost:9898] RPID=localhost SessionTTL=24h0m0s TrustProxy=false
```

Then with `PICOTERA_PUBLIC_ORIGIN=https://a.example.com,https://b.example.com PICOTERA_SESSION_TTL=12h MISE_DISABLE_TOOLS=pnpm mise run server`:

```
auth-config: PublicOrigins=[https://a.example.com https://b.example.com] RPID=a.example.com SessionTTL=12h0m0s TrustProxy=false
```

Remove the printf line.

- [ ] **Step 4: Commit.**

```bash
git add pkg/configx/configx.go
git commit -m "feat(configx): add PublicOrigins, WebAuthnRPID, SessionTTL, TrustProxy

PUBLIC_ORIGIN accepts a comma-separated list (first used to compose
enrollment URLs, all passed to webauthn.Config.RPOrigins). WebAuthnRPID
defaults to the first PublicOrigin's hostname. SessionTTL is a Viper
duration string."
```

---

### Task 3: `pkg/contract/auth.go` — types + operation contracts

**Goal:** Land all the contract-layer types (`Permission`, `AuthRequirement`, view types) and operation signatures the rest of the plan refers to. Operations are defined but not yet registered — they're referenced from later handler tasks.

**Files:**
- Create: `pkg/contract/auth.go`

**Acceptance Criteria:**
- [ ] `Permission`, `AuthRequirement` types exported.
- [ ] All view types match `api.md` shapes: `SessionView`, `AccountView`, `CredentialView`, `EnrollmentPreview`, `AuthStatus`, `InvitationResponse`, `EnrollmentURLResponse`.
- [ ] One `huma.Operation` constant per endpoint in `api.md` (about 25 in total).
- [ ] `go build ./...` passes.

**Verify:** `go build ./pkg/contract` succeeds; `grep -c "var Operation" pkg/contract/auth.go` returns ≥25.

**Steps:**

- [ ] **Step 1: Write the file.**

Create `pkg/contract/auth.go`:

```go
package contract

import (
    "time"

    "github.com/danielgtaylor/huma/v2"
)

// Permission is the typed key used by AuthRequirement when Kind == AuthPermission.
type Permission string

const (
    PermViewOwnUsage      Permission = "view_own_usage"
    PermManageOwnAPIKeys  Permission = "manage_own_api_keys"
    PermViewModels        Permission = "view_models"
    PermViewOwnTraces     Permission = "view_own_traces"
)

// AuthKind discriminates the AuthRequirement variants.
type AuthKind uint8

const (
    AuthPublic AuthKind = iota
    AuthSession
    AuthAdmin
    AuthPermissionKind
)

type AuthRequirement struct {
    Kind       AuthKind
    Permission Permission
}

func RequirePermission(p Permission) AuthRequirement {
    return AuthRequirement{Kind: AuthPermissionKind, Permission: p}
}

// View types --------------------------------------------------------------------

type Permissions struct {
    ViewOwnUsage     bool `json:"view_own_usage"`
    ManageOwnAPIKeys bool `json:"manage_own_api_keys"`
    ViewModels       bool `json:"view_models"`
    ViewOwnTraces    bool `json:"view_own_traces"`
}

type SessionView struct {
    ID          int32       `json:"id"`
    Username    string      `json:"username"`
    DisplayName string      `json:"displayName"`
    Role        string      `json:"role"`
    Permissions Permissions `json:"permissions"`
}

type AccountView struct {
    ID           int32       `json:"id"`
    Username     string      `json:"username"`
    DisplayName  string      `json:"displayName"`
    Role         string      `json:"role"`
    Permissions  Permissions `json:"permissions"`
    Disabled     bool        `json:"disabled"`
    CreatedAt    time.Time   `json:"createdAt"`
    UpdatedAt    time.Time   `json:"updatedAt"`
    LastSignInAt *time.Time  `json:"lastSignInAt,omitempty"`
}

type CredentialView struct {
    ID                 int32      `json:"id"`
    CredentialIDSuffix string     `json:"credentialIdSuffix"` // last 4 chars of base64url(credential_id)
    Nickname           *string    `json:"nickname,omitempty"`
    Transports         []string   `json:"transports"`
    BackupState        bool       `json:"backupState"`
    AttestationType    string     `json:"attestationType"`
    CreatedAt          time.Time  `json:"createdAt"`
    LastUsedAt         *time.Time `json:"lastUsedAt,omitempty"`
}

type EnrollmentTarget struct {
    Username    string `json:"username"`
    DisplayName string `json:"displayName"`
}

type EnrollmentPreview struct {
    Intent    string            `json:"intent"`
    Target    *EnrollmentTarget `json:"target,omitempty"`
    ExpiresAt time.Time         `json:"expiresAt"`
}

type AuthStatus struct {
    Bootstrapped bool `json:"bootstrapped"`
}

type EnrollmentURLResponse struct {
    URL       string    `json:"url"`
    ExpiresAt time.Time `json:"expiresAt"`
}

type InvitationResponse struct {
    Account   AccountView `json:"account"`
    URL       string      `json:"url"`
    ExpiresAt time.Time   `json:"expiresAt"`
}

// Operations --------------------------------------------------------------------
// Paths are under /api/picotera; the prefix is added by huma at registration time.

var OperationAuthStatus = huma.Operation{
    OperationID: "getAuthStatus",
    Method:      "GET",
    Path:        "/auth/status",
    Summary:     "Whether at least one admin account exists.",
}

var OperationLoginBegin = huma.Operation{
    OperationID: "beginLogin",
    Method:      "POST",
    Path:        "/auth/login/begin",
    Summary:     "Start a discoverable-credential login ceremony.",
}

var OperationLoginComplete = huma.Operation{
    OperationID: "completeLogin",
    Method:      "POST",
    Path:        "/auth/login/complete",
    Summary:     "Finish the login ceremony and issue a session.",
}

var OperationLogout = huma.Operation{
    OperationID: "logout",
    Method:      "POST",
    Path:        "/auth/logout",
    Summary:     "Revoke the current session (idempotent).",
}

var OperationGetMe = huma.Operation{
    OperationID: "getMe",
    Method:      "GET",
    Path:        "/me",
    Summary:     "Return the authenticated session.",
}

var OperationListMyCredentials = huma.Operation{
    OperationID: "listMyCredentials",
    Method:      "GET",
    Path:        "/me/credentials",
}

var OperationAddCredentialBegin = huma.Operation{
    OperationID: "beginAddCredential",
    Method:      "POST",
    Path:        "/me/credentials/register/begin",
}

var OperationAddCredentialComplete = huma.Operation{
    OperationID: "completeAddCredential",
    Method:      "POST",
    Path:        "/me/credentials/register/complete",
}

var OperationDeleteMyCredential = huma.Operation{
    OperationID: "deleteMyCredential",
    Method:      "POST",
    Path:        "/me/credentials/delete",
}

var OperationPreviewEnrollment = huma.Operation{
    OperationID: "previewEnrollment",
    Method:      "GET",
    Path:        "/enrollments/{token}",
}

var OperationEnrollmentRegisterBegin = huma.Operation{
    OperationID: "beginEnrollmentRegistration",
    Method:      "POST",
    Path:        "/enrollments/{token}/register/begin",
}

var OperationEnrollmentRegisterComplete = huma.Operation{
    OperationID: "completeEnrollmentRegistration",
    Method:      "POST",
    Path:        "/enrollments/{token}/register/complete",
}

// Admin account operations (Phase 2 wiring, but defined now)

var OperationListAccounts = huma.Operation{
    OperationID: "listAccounts",
    Method:      "GET",
    Path:        "/accounts",
}

var OperationGetAccount = huma.Operation{
    OperationID: "getAccount",
    Method:      "GET",
    Path:        "/accounts/{id}",
}

var OperationUpdateAccount = huma.Operation{
    OperationID: "updateAccount",
    Method:      "PUT",
    Path:        "/accounts/{id}",
}

var OperationDeleteAccount = huma.Operation{
    OperationID: "deleteAccount",
    Method:      "POST",
    Path:        "/accounts/delete",
}

var OperationDeleteAccountCredential = huma.Operation{
    OperationID: "deleteAccountCredential",
    Method:      "POST",
    Path:        "/accounts/credentials/delete",
}

var OperationRevokeAccountSessions = huma.Operation{
    OperationID: "revokeAccountSessions",
    Method:      "POST",
    Path:        "/accounts/revoke-sessions",
}

var OperationReissueEnrollment = huma.Operation{
    OperationID: "reissueEnrollment",
    Method:      "POST",
    Path:        "/accounts/reissue-enrollment",
}

var OperationCreateInvitation = huma.Operation{
    OperationID: "createInvitation",
    Method:      "POST",
    Path:        "/invitations",
}
```

- [ ] **Step 2: Build.**

```bash
go build ./pkg/contract
```

- [ ] **Step 3: Commit.**

```bash
git add pkg/contract/auth.go
git commit -m "feat(contract): auth + account view types and operation defs

Defines Permission enum, AuthRequirement discriminated struct,
SessionView/AccountView/CredentialView/EnrollmentPreview shapes,
and huma.Operation constants for every endpoint in
specs/2026-05-23-auth-system/api.md. Handlers and dashboard
typegen consume these in later tasks."
```

---

### Task 4: `pkg/auth` package skeleton — errors, account helpers

**Goal:** Two small files that the session and webauthn code will lean on. Includes the `db.Account.Permits(Permission) bool` helper and the typed error catalogue.

**Files:**
- Create: `pkg/auth/errors.go`
- Create: `pkg/auth/account.go`
- Create: `pkg/auth/account_test.go`

**Acceptance Criteria:**
- [ ] `auth.Errors` exposes constructors like `auth.ErrNoSession`, `auth.ErrAccountDisabled`, etc., each carrying the HTTP status and code string from `design.md` §8.
- [ ] `auth.Permits(account, permission)` returns true for any admin; checks the specific boolean for users.
- [ ] `auth.GenerateUserHandle()` returns 64 random bytes from `crypto/rand`.
- [ ] `go test ./pkg/auth` passes (1+ test).

**Verify:** `go test ./pkg/auth -run TestPermits -v` shows green.

**Steps:**

- [ ] **Step 1: Write `pkg/auth/errors.go`.**

```go
package auth

import (
    "errors"
    "fmt"
    "net/http"
)

type AuthError struct {
    Status  int
    Code    string
    Message string
}

func (e *AuthError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func newErr(status int, code, message string) *AuthError {
    return &AuthError{Status: status, Code: code, Message: message}
}

// Each constructor returns a fresh value so callers can decorate without sharing state.

func ErrNoSession() *AuthError         { return newErr(http.StatusUnauthorized, "no_session", "no session") }
func ErrNotAdmin() *AuthError          { return newErr(http.StatusForbidden, "not_admin", "admin required") }
func ErrPermissionDenied() *AuthError  { return newErr(http.StatusForbidden, "permission_denied", "permission denied") }
func ErrAccountDisabled() *AuthError   { return newErr(http.StatusForbidden, "account_disabled", "account disabled") }
func ErrLastAdmin() *AuthError         { return newErr(http.StatusConflict, "last_admin", "cannot demote, disable, or delete the only admin") }
func ErrUsernameTaken() *AuthError     { return newErr(http.StatusConflict, "username_taken", "username already exists") }
func ErrEnrollmentExpired() *AuthError { return newErr(http.StatusGone, "enrollment_expired", "enrollment link expired") }
func ErrEnrollmentConsumed() *AuthError{ return newErr(http.StatusGone, "enrollment_consumed", "enrollment link already used") }
func ErrInvalidUsername() *AuthError   { return newErr(http.StatusBadRequest, "invalid_username", "username must match ^[a-z0-9_-]{2,32}$") }
func ErrInvalidDisplayName() *AuthError{ return newErr(http.StatusBadRequest, "invalid_display_name", "display name must be 1-128 chars with no control characters") }
func ErrUsernameImmutable() *AuthError { return newErr(http.StatusBadRequest, "username_immutable", "username cannot be changed") }
func ErrLastPasskey() *AuthError       { return newErr(http.StatusBadRequest, "last_passkey", "cannot delete the only passkey on an account") }
func ErrWebAuthnCeremony(detail string) *AuthError {
    return newErr(http.StatusBadRequest, "webauthn_ceremony_failed", "webauthn ceremony failed: "+detail)
}
func ErrAccountNotFound() *AuthError    { return newErr(http.StatusNotFound, "account_not_found", "account not found") }
func ErrCredentialNotFound() *AuthError { return newErr(http.StatusNotFound, "credential_not_found", "credential not found") }
func ErrNotBootstrapped() *AuthError    { return newErr(http.StatusServiceUnavailable, "not_bootstrapped", "no admin enrolled; run `picotera enroll-admin`") }

// AsAuthError unwraps an error chain to find an *AuthError, returning nil if none.
func AsAuthError(err error) *AuthError {
    var a *AuthError
    if errors.As(err, &a) {
        return a
    }
    return nil
}
```

- [ ] **Step 2: Write `pkg/auth/account.go`.**

```go
package auth

import (
    "crypto/rand"
    "fmt"
    "regexp"

    "picotera/pkg/contract"
    "picotera/pkg/db"
)

var usernameRegex = regexp.MustCompile(`^[a-z0-9_-]{2,32}$`)

func ValidateUsername(s string) error {
    if !usernameRegex.MatchString(s) {
        return ErrInvalidUsername()
    }
    return nil
}

func ValidateDisplayName(s string) error {
    if len(s) < 1 || len(s) > 128 {
        return ErrInvalidDisplayName()
    }
    for _, r := range s {
        if r < 0x20 || r == 0x7f {
            return ErrInvalidDisplayName()
        }
    }
    return nil
}

// GenerateUserHandle returns 64 cryptographically random bytes used as
// the WebAuthn user.id for a new account. Persisted in
// account.webauthn_user_handle.
func GenerateUserHandle() ([]byte, error) {
    b := make([]byte, 64)
    if _, err := rand.Read(b); err != nil {
        return nil, fmt.Errorf("generate user handle: %w", err)
    }
    return b, nil
}

// Permits returns true if the account is allowed to perform the action
// identified by p. Admins are unconditionally permitted.
func Permits(a *db.Account, p contract.Permission) bool {
    if a == nil {
        return false
    }
    if a.Role == "admin" {
        return true
    }
    switch p {
    case contract.PermViewOwnUsage:
        return a.CanViewOwnUsage
    case contract.PermManageOwnAPIKeys:
        return a.CanManageOwnApiKeys
    case contract.PermViewModels:
        return a.CanViewModels
    case contract.PermViewOwnTraces:
        return a.CanViewOwnTraces
    }
    return false
}

// PermissionsView projects an account's permission columns into the
// contract.Permissions view shape.
func PermissionsView(a *db.Account) contract.Permissions {
    if a == nil {
        return contract.Permissions{}
    }
    if a.Role == "admin" {
        return contract.Permissions{
            ViewOwnUsage: true, ManageOwnAPIKeys: true, ViewModels: true, ViewOwnTraces: true,
        }
    }
    return contract.Permissions{
        ViewOwnUsage:     a.CanViewOwnUsage,
        ManageOwnAPIKeys: a.CanManageOwnApiKeys,
        ViewModels:       a.CanViewModels,
        ViewOwnTraces:    a.CanViewOwnTraces,
    }
}
```

- [ ] **Step 3: Write `pkg/auth/account_test.go`.**

```go
package auth

import (
    "testing"

    "picotera/pkg/contract"
    "picotera/pkg/db"
)

func TestPermits_AdminAlwaysPasses(t *testing.T) {
    a := &db.Account{Role: "admin"}
    for _, p := range []contract.Permission{
        contract.PermViewOwnUsage,
        contract.PermManageOwnAPIKeys,
        contract.PermViewModels,
        contract.PermViewOwnTraces,
    } {
        if !Permits(a, p) {
            t.Errorf("admin should pass %s", p)
        }
    }
}

func TestPermits_UserChecksFields(t *testing.T) {
    a := &db.Account{Role: "user", CanViewOwnUsage: true}
    if !Permits(a, contract.PermViewOwnUsage) {
        t.Error("user with CanViewOwnUsage should pass")
    }
    if Permits(a, contract.PermManageOwnAPIKeys) {
        t.Error("user without CanManageOwnApiKeys should not pass")
    }
}

func TestValidateUsername(t *testing.T) {
    valid := []string{"alice", "bob_smith", "user-1", "ab", "a_-9"}
    for _, s := range valid {
        if err := ValidateUsername(s); err != nil {
            t.Errorf("%q should be valid: %v", s, err)
        }
    }
    invalid := []string{"", "a", "Alice", "alice@example", " alice", "alice ", "x" + string(make([]byte, 32))}
    for _, s := range invalid {
        if err := ValidateUsername(s); err == nil {
            t.Errorf("%q should be invalid", s)
        }
    }
}

func TestGenerateUserHandle(t *testing.T) {
    a, err := GenerateUserHandle()
    if err != nil { t.Fatal(err) }
    if len(a) != 64 { t.Errorf("want 64 bytes, got %d", len(a)) }
    b, _ := GenerateUserHandle()
    if string(a) == string(b) { t.Error("two generations should differ") }
}
```

- [ ] **Step 4: Test.**

```bash
go test ./pkg/auth -v
```

Expected: 4 tests pass.

- [ ] **Step 5: Commit.**

```bash
git add pkg/auth/
git commit -m "feat(auth): typed errors + account helpers

errors.go defines the canonical AuthError catalogue mapped to HTTP
status codes per design.md §8. account.go ships ValidateUsername /
ValidateDisplayName (CLAUDE.md fail-fast), GenerateUserHandle (64
crypto/rand bytes for the WebAuthn user.id), and Permits / PermissionsView
helpers that admins auto-pass."
```

---

### Task 5: `pkg/auth/session.go` — KV-backed sessions

**Goal:** Session issue / load (with sliding refresh) / revoke / revoke-all, keyed `session:<account_id>:<random>`.

**Files:**
- Create: `pkg/auth/session.go`
- Create: `pkg/auth/session_test.go`

**Acceptance Criteria:**
- [ ] `Issue` writes a KV entry and returns the random token (43 chars, base64url).
- [ ] `Load` returns 401-equivalent error when the entry is missing or expired.
- [ ] `Load` refreshes the TTL only if `expires_at - now < 25% * SessionTTL`.
- [ ] `Revoke(token)` deletes the specific entry.
- [ ] `RevokeAllForAccount(accountID)` scans `session:<id>:*` and deletes all matches.
- [ ] All tests pass against the in-memory KV fallback.

**Verify:** `go test ./pkg/auth -run TestSession -v` shows green.

**Steps:**

- [ ] **Step 1: Confirm KV interface.**

Read `pkg/kv/store.go` to confirm:
- `Get(ctx, key) (string, bool, error)`
- `Set(ctx, key, value string) error`
- `SetEx(ctx, key, value string, ttl time.Duration) error`
- `Del(ctx, key) error`
- `Scan(ctx, pattern string) ([]string, error)` — needed for prefix scan

If `Scan` is missing, add it. The Redis backend uses `SCAN MATCH`; the in-memory backend iterates and matches via `path.Match` or a simple prefix check (the only pattern we use is `prefix:*`).

- [ ] **Step 2: Write `pkg/auth/session.go`.**

```go
package auth

import (
    "context"
    "crypto/rand"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "strings"
    "time"

    "picotera/pkg/kv"
)

type SessionData struct {
    AccountID   int32     `json:"account_id"`
    IssuedAt    time.Time `json:"issued_at"`
    ExpiresAt   time.Time `json:"expires_at"`
    LastSeenIP  string    `json:"last_seen_ip"`
}

type SessionStore struct {
    kv      kv.Store
    ttl     time.Duration
    refresh time.Duration  // refresh threshold (= 25% of ttl)
}

func NewSessionStore(s kv.Store, ttl time.Duration) *SessionStore {
    return &SessionStore{kv: s, ttl: ttl, refresh: ttl / 4}
}

func newToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return base64.RawURLEncoding.EncodeToString(b), nil
}

func sessionKey(accountID int32, token string) string {
    return fmt.Sprintf("session:%d:%s", accountID, token)
}

func (s *SessionStore) Issue(ctx context.Context, accountID int32, ip string) (token string, data *SessionData, err error) {
    token, err = newToken()
    if err != nil {
        return "", nil, err
    }
    now := time.Now()
    data = &SessionData{
        AccountID:  accountID,
        IssuedAt:   now,
        ExpiresAt:  now.Add(s.ttl),
        LastSeenIP: ip,
    }
    payload, _ := json.Marshal(data)
    if err := s.kv.SetEx(ctx, sessionKey(accountID, token), string(payload), s.ttl); err != nil {
        return "", nil, fmt.Errorf("session issue: %w", err)
    }
    return token, data, nil
}

// Load returns the session data and a flag indicating whether the
// caller should re-emit a Set-Cookie (because the TTL was refreshed).
// On invalid/expired/missing returns ErrNoSession().
func (s *SessionStore) Load(ctx context.Context, accountID int32, token, ip string) (*SessionData, bool, error) {
    key := sessionKey(accountID, token)
    raw, ok, err := s.kv.Get(ctx, key)
    if err != nil {
        return nil, false, err
    }
    if !ok {
        return nil, false, ErrNoSession()
    }
    var data SessionData
    if err := json.Unmarshal([]byte(raw), &data); err != nil {
        // corrupted entry — treat as missing, also clean it up
        _ = s.kv.Del(ctx, key)
        return nil, false, ErrNoSession()
    }
    now := time.Now()
    if now.After(data.ExpiresAt) {
        _ = s.kv.Del(ctx, key)
        return nil, false, ErrNoSession()
    }

    refreshed := false
    if data.ExpiresAt.Sub(now) < s.refresh {
        data.ExpiresAt = now.Add(s.ttl)
        data.LastSeenIP = ip
        payload, _ := json.Marshal(&data)
        _ = s.kv.SetEx(ctx, key, string(payload), s.ttl)
        refreshed = true
    }
    return &data, refreshed, nil
}

func (s *SessionStore) Revoke(ctx context.Context, accountID int32, token string) error {
    return s.kv.Del(ctx, sessionKey(accountID, token))
}

// RevokeAllForAccount removes every session for the account. Best-effort:
// returns the count of deleted entries. Errors from individual deletes are logged
// by the caller (this returns the first error encountered).
func (s *SessionStore) RevokeAllForAccount(ctx context.Context, accountID int32) (int, error) {
    pattern := fmt.Sprintf("session:%d:*", accountID)
    keys, err := s.kv.Scan(ctx, pattern)
    if err != nil {
        return 0, err
    }
    var firstErr error
    deleted := 0
    for _, k := range keys {
        if err := s.kv.Del(ctx, k); err != nil {
            if firstErr == nil {
                firstErr = err
            }
            continue
        }
        deleted++
    }
    return deleted, firstErr
}

// ParseCookieValue splits a "<account_id>.<token>" cookie value into its parts.
// We embed accountID in the cookie so Load can construct the prefixed key directly
// without an extra round-trip lookup. Format: "<int>.<base64url>".
func ParseCookieValue(v string) (int32, string, bool) {
    dot := strings.IndexByte(v, '.')
    if dot < 1 || dot == len(v)-1 {
        return 0, "", false
    }
    var id int32
    _, err := fmt.Sscanf(v[:dot], "%d", &id)
    if err != nil || id <= 0 {
        return 0, "", false
    }
    return id, v[dot+1:], true
}

func CookieValue(accountID int32, token string) string {
    return fmt.Sprintf("%d.%s", accountID, token)
}
```

Design note: the cookie carries `<account_id>.<token>` (e.g. `42.AbCdEf...`) so `Load` can read the account from the cookie itself and construct the KV key without a separate index lookup. Both halves are needed and neither is sensitive (the random `token` is the secret).

- [ ] **Step 3: Write `pkg/auth/session_test.go`.**

```go
package auth

import (
    "context"
    "testing"
    "time"

    "picotera/pkg/kv"
)

func newTestStore(t *testing.T) *SessionStore {
    t.Helper()
    return NewSessionStore(kv.NewMemory(), 1*time.Hour)
}

func TestSession_IssueAndLoad(t *testing.T) {
    s := newTestStore(t)
    token, data, err := s.Issue(context.Background(), 42, "127.0.0.1")
    if err != nil { t.Fatal(err) }
    if data.AccountID != 42 { t.Errorf("AccountID = %d", data.AccountID) }
    if len(token) != 43 { t.Errorf("token len = %d", len(token)) }

    loaded, refreshed, err := s.Load(context.Background(), 42, token, "127.0.0.1")
    if err != nil { t.Fatal(err) }
    if refreshed { t.Error("fresh session should not refresh") }
    if loaded.AccountID != 42 { t.Errorf("loaded AccountID = %d", loaded.AccountID) }
}

func TestSession_Missing(t *testing.T) {
    s := newTestStore(t)
    _, _, err := s.Load(context.Background(), 42, "bogus", "127.0.0.1")
    if err == nil { t.Fatal("missing session should error") }
    if AsAuthError(err) == nil || AsAuthError(err).Code != "no_session" {
        t.Errorf("want no_session, got %v", err)
    }
}

func TestSession_Revoke(t *testing.T) {
    s := newTestStore(t)
    token, _, _ := s.Issue(context.Background(), 42, "127.0.0.1")
    if err := s.Revoke(context.Background(), 42, token); err != nil { t.Fatal(err) }
    _, _, err := s.Load(context.Background(), 42, token, "127.0.0.1")
    if err == nil { t.Error("revoked session should not load") }
}

func TestSession_RevokeAllForAccount(t *testing.T) {
    s := newTestStore(t)
    ctx := context.Background()
    for i := 0; i < 3; i++ {
        s.Issue(ctx, 42, "127.0.0.1")
    }
    s.Issue(ctx, 99, "127.0.0.1")
    n, err := s.RevokeAllForAccount(ctx, 42)
    if err != nil { t.Fatal(err) }
    if n != 3 { t.Errorf("deleted %d, want 3", n) }
    // 99's session still works
    keys, _ := s.kv.Scan(ctx, "session:99:*")
    if len(keys) != 1 { t.Errorf("account 99 should still have 1 session, got %d", len(keys)) }
}

func TestSession_Refresh(t *testing.T) {
    s := NewSessionStore(kv.NewMemory(), 100*time.Millisecond)
    ctx := context.Background()
    token, _, _ := s.Issue(ctx, 42, "")
    // 80ms in, refresh threshold (25%) = 25ms, so 80ms remaining < 25ms? No, expiring at +100ms, so 20ms remaining.
    time.Sleep(80 * time.Millisecond)
    _, refreshed, err := s.Load(ctx, 42, token, "")
    if err != nil { t.Fatal(err) }
    if !refreshed { t.Error("close-to-expiry load should refresh") }
}

func TestParseCookieValue(t *testing.T) {
    id, tok, ok := ParseCookieValue("42.xyz")
    if !ok || id != 42 || tok != "xyz" { t.Errorf("got %d %q %v", id, tok, ok) }
    _, _, ok = ParseCookieValue("notanint.xyz")
    if ok { t.Error("non-int prefix should fail") }
    _, _, ok = ParseCookieValue(".xyz")
    if ok { t.Error("empty prefix should fail") }
    _, _, ok = ParseCookieValue("42.")
    if ok { t.Error("empty token should fail") }
}
```

- [ ] **Step 4: Test.**

```bash
go test ./pkg/auth -v
```

Expected: all tests pass.

- [ ] **Step 5: Commit.**

```bash
git add pkg/auth/session.go pkg/auth/session_test.go
# also add pkg/kv if Scan was added in step 1
git commit -m "feat(auth): KV-backed session store

session:<account_id>:<random> entries keyed in pkg/kv with sliding TTL.
Refresh runs only when expires_at - now < 25% * TTL to limit write
amplification. RevokeAllForAccount uses Scan to enumerate by prefix.
Cookie value is <account_id>.<token> so Load avoids a secondary lookup."
```

---

### Task 6: `pkg/auth/webauthn.go` — library wrapper

**Goal:** Construct and expose the WebAuthn library with the split UV-per-ceremony config; implement the `WebAuthnAccount` adapter so go-webauthn can read account + credential state.

**Files:**
- Create: `pkg/auth/webauthn.go`
- Modify: `go.mod` / `go.sum` (add `github.com/go-webauthn/webauthn`)

**Acceptance Criteria:**
- [ ] `auth.NewWebAuthn(cfg)` returns `*webauthn.WebAuthn` configured with `RPID`, `RPDisplayName="PicoTera"`, `RPOrigins=cfg.PublicOrigins`, `AttestationPreference=PreferNoAttestation`, `Timeouts` per design.md §5.
- [ ] `WebAuthnAccount` satisfies `webauthn.User` (returns `WebAuthnID`, `WebAuthnName`, `WebAuthnDisplayName`, `WebAuthnCredentials`).
- [ ] `BeginRegistration(account, exclude)` uses `UserVerificationRequired` + `ResidentKeyRequirementRequired`.
- [ ] `BeginLogin()` uses `UserVerificationPreferred` and starts a discoverable-credential flow (no allowCredentials).
- [ ] `go build ./...` passes.

**Verify:** `go vet ./pkg/auth` clean; `go build ./...` clean.

**Steps:**

- [ ] **Step 1: Add dependency.**

```bash
go get github.com/go-webauthn/webauthn
go mod tidy
```

- [ ] **Step 2: Write `pkg/auth/webauthn.go`.**

```go
package auth

import (
    "fmt"
    "time"

    "github.com/go-webauthn/webauthn/protocol"
    "github.com/go-webauthn/webauthn/webauthn"

    "picotera/pkg/configx"
    "picotera/pkg/db"
)

func NewWebAuthn(cfg *configx.Config) (*webauthn.WebAuthn, error) {
    if len(cfg.PublicOrigins) == 0 {
        return nil, fmt.Errorf("webauthn: PICOTERA_PUBLIC_ORIGIN must be set")
    }
    rpid := cfg.WebAuthnRPID
    if rpid == "" {
        return nil, fmt.Errorf("webauthn: PICOTERA_WEBAUTHN_RP_ID must be set (or derivable from PUBLIC_ORIGIN)")
    }
    return webauthn.New(&webauthn.Config{
        RPID:          rpid,
        RPDisplayName: "PicoTera",
        RPOrigins:     cfg.PublicOrigins,
        AttestationPreference: protocol.PreferNoAttestation,
        Timeouts: webauthn.TimeoutsConfig{
            Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: 60 * time.Second},
            Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: 120 * time.Second},
        },
    })
}

// WebAuthnAccount adapts a db.Account + its credentials so go-webauthn can
// produce protocol-correct WebAuthn user metadata.
type WebAuthnAccount struct {
    Account     *db.Account
    Credentials []db.WebauthnCredential
}

func (w *WebAuthnAccount) WebAuthnID() []byte                    { return w.Account.WebauthnUserHandle }
func (w *WebAuthnAccount) WebAuthnName() string                  { return w.Account.Username }
func (w *WebAuthnAccount) WebAuthnDisplayName() string           { return w.Account.DisplayName }
func (w *WebAuthnAccount) WebAuthnIcon() string                  { return "" }

func (w *WebAuthnAccount) WebAuthnCredentials() []webauthn.Credential {
    out := make([]webauthn.Credential, 0, len(w.Credentials))
    for _, c := range w.Credentials {
        var transports []protocol.AuthenticatorTransport
        for _, t := range c.Transports {
            transports = append(transports, protocol.AuthenticatorTransport(t))
        }
        out = append(out, webauthn.Credential{
            ID:              c.CredentialID,
            PublicKey:       c.PublicKey,
            AttestationType: c.AttestationType,
            Transport:       transports,
            Flags: webauthn.CredentialFlags{
                BackupEligible: c.BackupEligible,
                BackupState:    c.BackupState,
            },
            Authenticator: webauthn.Authenticator{
                AAGUID:    c.Aaguid,
                SignCount: uint32(c.SignCount),
            },
        })
    }
    return out
}

// RegistrationOptions returns the options used for BeginRegistration so
// callers (handlers + tests) can centralize the UV policy.
func RegistrationOptions(exclude []protocol.CredentialDescriptor) []webauthn.RegistrationOption {
    return []webauthn.RegistrationOption{
        webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
            ResidentKey:      protocol.ResidentKeyRequirementRequired,
            UserVerification: protocol.VerificationRequired,
        }),
        webauthn.WithExclusions(exclude),
    }
}

func LoginOptions() []webauthn.LoginOption {
    return []webauthn.LoginOption{
        webauthn.WithUserVerification(protocol.VerificationPreferred),
        // No allowCredentials => discoverable-credential flow.
    }
}
```

- [ ] **Step 3: Build.**

```bash
go build ./...
```

Expected: succeeds; `go.sum` updated.

- [ ] **Step 4: Commit.**

```bash
git add pkg/auth/webauthn.go go.mod go.sum
git commit -m "feat(auth): wrap go-webauthn with split UV policy

Registration ceremonies use VerificationRequired + ResidentKeyRequired
(so credentials are discoverable and UV-flagged forever). Login uses
VerificationPreferred for ergonomic platform-authenticator UX.
WebAuthnAccount adapter projects db.Account + credentials into the
webauthn.User interface."
```

---

### Task 7: `pkg/auth/enrollment.go` — token issuance / consume helpers

**Goal:** Helpers that wrap the DB so handlers can issue, preview, and consume enrollment tokens without duplicating policy.

**Files:**
- Create: `pkg/auth/enrollment.go`
- Create: `pkg/auth/enrollment_test.go`

**Acceptance Criteria:**
- [ ] `auth.IssueEnrollment(ctx, q, intent, targetAccountID, ttl)` inserts an enrollment row with a 32-byte base64url token and returns `(token, expiresAt, err)`.
- [ ] `auth.LoadEnrollment(ctx, q, token)` returns the row or `ErrEnrollmentExpired` / `ErrEnrollmentConsumed`.
- [ ] `auth.ConsumeEnrollment(ctx, q, token)` marks `consumed_at = now()` via the existing sqlc method.
- [ ] Constants for `IntentBootstrap`, `IntentInvite`, `IntentReset` exported.
- [ ] Tests use an in-memory or interface-mocked `db.Querier` and pass.

**Verify:** `go test ./pkg/auth -v` (5+ tests pass).

**Steps:**

- [ ] **Step 1: Write `pkg/auth/enrollment.go`.**

```go
package auth

import (
    "context"
    "crypto/rand"
    "database/sql"
    "encoding/base64"
    "errors"
    "fmt"
    "time"

    "picotera/pkg/db"
)

const (
    IntentBootstrap = "bootstrap"
    IntentInvite    = "invite"
    IntentReset     = "reset"

    DefaultEnrollmentTTL = 24 * time.Hour
)

func newEnrollmentToken() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return base64.RawURLEncoding.EncodeToString(b), nil
}

// IssueEnrollment inserts a new enrollment row. For bootstrap, targetAccountID
// must be nil; for invite/reset it must be set.
func IssueEnrollment(
    ctx context.Context,
    q db.Querier,
    intent string,
    targetAccountID *int32,
    ttl time.Duration,
) (token string, expiresAt time.Time, err error) {
    if ttl <= 0 {
        ttl = DefaultEnrollmentTTL
    }
    token, err = newEnrollmentToken()
    if err != nil {
        return "", time.Time{}, err
    }
    expiresAt = time.Now().Add(ttl)
    var tgt sql.NullInt32
    if targetAccountID != nil {
        tgt = sql.NullInt32{Int32: *targetAccountID, Valid: true}
    }
    if _, err := q.InsertEnrollment(ctx, db.InsertEnrollmentParams{
        Token:           token,
        Intent:          intent,
        TargetAccountID: tgt,
        ExpiresAt:       expiresAt,
    }); err != nil {
        return "", time.Time{}, fmt.Errorf("insert enrollment: %w", err)
    }
    return token, expiresAt, nil
}

// LoadEnrollment fetches and validates an enrollment by token. Returns
// ErrEnrollmentConsumed if the row exists but is consumed (or target deleted
// — the CASCADE means the row is gone in that case, and we synthesize the
// same external error). ErrEnrollmentExpired if past expires_at.
func LoadEnrollment(ctx context.Context, q db.Querier, token string) (*db.Enrollment, error) {
    row, err := q.GetEnrollmentByToken(ctx, token)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            // could be never-issued or cascade-deleted; same UX
            return nil, ErrEnrollmentConsumed()
        }
        return nil, err
    }
    if row.ConsumedAt.Valid {
        return nil, ErrEnrollmentConsumed()
    }
    if time.Now().After(row.ExpiresAt) {
        return nil, ErrEnrollmentExpired()
    }
    return &row, nil
}

// ConsumeEnrollment marks the token as used. Must be called inside the
// same TX as the credential insert.
func ConsumeEnrollment(ctx context.Context, q db.Querier, token string) error {
    return q.MarkEnrollmentConsumed(ctx, token)
}
```

- [ ] **Step 2: Write `pkg/auth/enrollment_test.go`.**

Use a fake `db.Querier` implemented via a struct that holds rows in memory. Show only the relevant methods (the executor can fill in the rest with `t.Skip`'d stubs):

```go
package auth

import (
    "context"
    "database/sql"
    "testing"
    "time"

    "picotera/pkg/db"
)

type fakeQ struct {
    db.Querier
    enrollments map[string]db.Enrollment
}

func newFakeQ() *fakeQ { return &fakeQ{enrollments: map[string]db.Enrollment{}} }

func (f *fakeQ) InsertEnrollment(_ context.Context, p db.InsertEnrollmentParams) (db.Enrollment, error) {
    e := db.Enrollment{
        Token: p.Token, Intent: p.Intent, TargetAccountID: p.TargetAccountID,
        CreatedAt: time.Now(), ExpiresAt: p.ExpiresAt,
    }
    f.enrollments[p.Token] = e
    return e, nil
}
func (f *fakeQ) GetEnrollmentByToken(_ context.Context, t string) (db.Enrollment, error) {
    if e, ok := f.enrollments[t]; ok {
        return e, nil
    }
    return db.Enrollment{}, sql.ErrNoRows
}
func (f *fakeQ) MarkEnrollmentConsumed(_ context.Context, t string) error {
    e := f.enrollments[t]
    e.ConsumedAt.Valid = true
    e.ConsumedAt.Time = time.Now()
    f.enrollments[t] = e
    return nil
}

func TestEnrollment_RoundTrip(t *testing.T) {
    q := newFakeQ()
    tok, exp, err := IssueEnrollment(context.Background(), q, IntentBootstrap, nil, time.Hour)
    if err != nil { t.Fatal(err) }
    if len(tok) != 43 { t.Errorf("token len = %d", len(tok)) }
    if exp.Before(time.Now()) { t.Error("expires in the past") }

    e, err := LoadEnrollment(context.Background(), q, tok)
    if err != nil { t.Fatal(err) }
    if e.Intent != IntentBootstrap { t.Errorf("intent = %q", e.Intent) }

    if err := ConsumeEnrollment(context.Background(), q, tok); err != nil { t.Fatal(err) }
    _, err = LoadEnrollment(context.Background(), q, tok)
    if AsAuthError(err) == nil || AsAuthError(err).Code != "enrollment_consumed" {
        t.Errorf("want enrollment_consumed, got %v", err)
    }
}

func TestEnrollment_Expired(t *testing.T) {
    q := newFakeQ()
    tok, _, _ := IssueEnrollment(context.Background(), q, IntentBootstrap, nil, -1*time.Hour)
    _, err := LoadEnrollment(context.Background(), q, tok)
    if AsAuthError(err) == nil || AsAuthError(err).Code != "enrollment_expired" {
        t.Errorf("want enrollment_expired, got %v", err)
    }
}

func TestEnrollment_MissingTreatedAsConsumed(t *testing.T) {
    q := newFakeQ()
    _, err := LoadEnrollment(context.Background(), q, "bogus")
    if AsAuthError(err) == nil || AsAuthError(err).Code != "enrollment_consumed" {
        t.Errorf("want enrollment_consumed (proxy for cascade-deleted), got %v", err)
    }
}
```

- [ ] **Step 3: Test.**

```bash
go test ./pkg/auth -v
```

Expected: tests pass.

- [ ] **Step 4: Commit.**

```bash
git add pkg/auth/enrollment.go pkg/auth/enrollment_test.go
git commit -m "feat(auth): enrollment token helpers

IssueEnrollment / LoadEnrollment / ConsumeEnrollment wrap the
sqlc-generated CRUD with the intent/target invariants from
design.md §1. Token is base64url(crypto/rand 32 bytes), 43 chars.
Missing rows surface as ErrEnrollmentConsumed (CASCADE-deleted on
account removal) to match the user-facing UX."
```

---

### Task 8: `pkg/auth/middleware.go` + `pkg/server/operations.go` — enforcement

**Goal:** Two small files that connect the session store to chi (`LoadSession` middleware) and Huma (`registerOp` helper).

**Files:**
- Create: `pkg/auth/middleware.go`
- Create: `pkg/server/operations.go`

**Acceptance Criteria:**
- [ ] `LoadSession` reads the `picotera_session` cookie, calls `SessionStore.Load`, fetches the account fresh via `GetAccountByID`, attaches `*auth.Session` to the request context. On `disabled=true`, returns 403.
- [ ] `auth.SessionFromContext(ctx)` returns the attached `*Session` or nil.
- [ ] `registerOp[I, O](api, op, handler, req)` wraps `huma.Register` with a per-operation middleware that checks `req` against the context session and returns the canonical auth error on failure.
- [ ] `go build ./...` passes.

**Verify:** `go build ./...` + `go vet ./...` clean.

**Steps:**

- [ ] **Step 1: Write `pkg/auth/middleware.go`.**

```go
package auth

import (
    "context"
    "net/http"
    "strings"

    "picotera/pkg/configx"
    "picotera/pkg/contract"
    "picotera/pkg/db"
)

const SessionCookieName = "picotera_session"
const CeremonyCookieName = "picotera_ceremony"

type Session struct {
    Account *db.Account
    Token   string
    Data    *SessionData
}

type ctxKey struct{ name string }

var sessionKey = ctxKey{name: "session"}

func WithSession(ctx context.Context, s *Session) context.Context {
    return context.WithValue(ctx, sessionKey, s)
}

func SessionFromContext(ctx context.Context) *Session {
    s, _ := ctx.Value(sessionKey).(*Session)
    return s
}

// LoadSession returns a chi middleware that reads the session cookie,
// validates it against the SessionStore, fetches the live account row,
// and attaches *Session to the request context. It does NOT reject
// missing sessions — that's per-route via registerOp's check. It DOES
// reject disabled accounts with 403 account_disabled, because a disabled
// account never has authority regardless of route.
func LoadSession(cfg *configx.Config, q db.Querier, store *SessionStore) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            c, err := r.Cookie(SessionCookieName)
            if err != nil || c.Value == "" {
                next.ServeHTTP(w, r)
                return
            }
            accountID, token, ok := ParseCookieValue(c.Value)
            if !ok {
                next.ServeHTTP(w, r)
                return
            }
            ip := clientIP(r, cfg.TrustProxy)
            data, refreshed, err := store.Load(r.Context(), accountID, token, ip)
            if err != nil {
                // Invalid/expired session: clear the cookie and continue unauthenticated.
                http.SetCookie(w, ClearedSessionCookie(cfg, r))
                next.ServeHTTP(w, r)
                return
            }
            account, err := q.GetAccountByID(r.Context(), accountID)
            if err != nil {
                // account vanished — bail out
                http.SetCookie(w, ClearedSessionCookie(cfg, r))
                _ = store.Revoke(r.Context(), accountID, token)
                next.ServeHTTP(w, r)
                return
            }
            if account.Disabled {
                http.Error(w, "account disabled", http.StatusForbidden)
                _ = store.Revoke(r.Context(), accountID, token)
                http.SetCookie(w, ClearedSessionCookie(cfg, r))
                return
            }
            if refreshed {
                http.SetCookie(w, FreshSessionCookie(cfg, r, accountID, token, store.ttl))
            }
            ctx := WithSession(r.Context(), &Session{Account: &account, Token: token, Data: data})
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// Check returns the canonical auth error for the requirement against the
// (possibly nil) session. Returns nil on success.
func Check(s *Session, req contract.AuthRequirement) error {
    if req.Kind == contract.AuthPublic {
        return nil
    }
    if s == nil {
        return ErrNoSession()
    }
    switch req.Kind {
    case contract.AuthSession:
        return nil
    case contract.AuthAdmin:
        if s.Account.Role != "admin" {
            return ErrNotAdmin()
        }
        return nil
    case contract.AuthPermissionKind:
        if !Permits(s.Account, req.Permission) {
            return ErrPermissionDenied()
        }
        return nil
    }
    return ErrNoSession()
}

// ---- cookie helpers --------------------------------------------------------

func FreshSessionCookie(cfg *configx.Config, r *http.Request, accountID int32, token string, ttl int64Duration) *http.Cookie {
    return &http.Cookie{
        Name:     SessionCookieName,
        Value:    CookieValue(accountID, token),
        Path:     "/api/picotera",
        HttpOnly: true,
        Secure:   isSecure(r, cfg.TrustProxy),
        SameSite: http.SameSiteLaxMode,
        MaxAge:   int(ttl.Seconds()),
    }
}

func ClearedSessionCookie(cfg *configx.Config, r *http.Request) *http.Cookie {
    return &http.Cookie{
        Name:     SessionCookieName,
        Value:    "",
        Path:     "/api/picotera",
        HttpOnly: true,
        Secure:   isSecure(r, cfg.TrustProxy),
        SameSite: http.SameSiteLaxMode,
        MaxAge:   -1,
    }
}

func CeremonyCookie(cfg *configx.Config, r *http.Request, value string) *http.Cookie {
    return &http.Cookie{
        Name:     CeremonyCookieName,
        Value:    value,
        Path:     "/api/picotera/auth",
        HttpOnly: true,
        Secure:   isSecure(r, cfg.TrustProxy),
        SameSite: http.SameSiteStrictMode,
        MaxAge:   300,
    }
}

func ClearedCeremonyCookie(cfg *configx.Config, r *http.Request) *http.Cookie {
    c := CeremonyCookie(cfg, r, "")
    c.MaxAge = -1
    return c
}

func isSecure(r *http.Request, trustProxy bool) bool {
    if r.TLS != nil {
        return true
    }
    if trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
        return true
    }
    return false
}

func clientIP(r *http.Request, trustProxy bool) string {
    if trustProxy {
        if v := r.Header.Get("X-Forwarded-For"); v != "" {
            if i := strings.IndexByte(v, ','); i > 0 {
                return strings.TrimSpace(v[:i])
            }
            return strings.TrimSpace(v)
        }
        if v := r.Header.Get("X-Real-IP"); v != "" {
            return v
        }
    }
    host := r.RemoteAddr
    if i := strings.LastIndexByte(host, ':'); i > 0 {
        host = host[:i]
    }
    return host
}

type int64Duration = time.Duration  // alias to avoid extra import in this snippet — keep as time.Duration in real code
```

**Note for the implementer:** the `int64Duration` alias above is just a snippet shorthand — in the actual file, declare the parameter as `ttl time.Duration` and `import "time"`. (Remove the alias from the final file.)

- [ ] **Step 2: Write `pkg/server/operations.go`.**

```go
package server

import (
    "context"
    "net/http"

    "github.com/danielgtaylor/huma/v2"

    "picotera/pkg/auth"
    "picotera/pkg/contract"
)

// registerOp wraps huma.Register so every operation declares its auth
// requirement at the call site. The middleware reads *auth.Session from
// the request context (placed there by auth.LoadSession on the chi
// router) and returns the canonical AuthError before invoking handler.
func registerOp[I, O any](
    api huma.API,
    op huma.Operation,
    handler func(context.Context, *I) (*O, error),
    req contract.AuthRequirement,
) {
    op.Middlewares = append(op.Middlewares, func(ctx huma.Context, next func(huma.Context)) {
        sess := auth.SessionFromContext(ctx.Context())
        if err := auth.Check(sess, req); err != nil {
            ae := auth.AsAuthError(err)
            _ = huma.WriteErr(api, ctx, ae.Status, ae.Message, &huma.ErrorDetail{Location: "", Message: ae.Code})
            return
        }
        next(ctx)
    })
    huma.Register(api, op, handler)
}

// registerOpHTTP wraps the operation for handlers that need raw http.ResponseWriter
// (e.g. /auth/logout that needs to Set-Cookie). Uses huma.Adapter for the same
// auth.Check gating.
//
// Use this when the handler returns ()->() instead of typed I/O.
func registerOpHTTP(
    router interface {
        Post(pattern string, h http.HandlerFunc)
    },
    method, path string,
    req contract.AuthRequirement,
    h http.HandlerFunc,
) {
    wrapped := func(w http.ResponseWriter, r *http.Request) {
        sess := auth.SessionFromContext(r.Context())
        if err := auth.Check(sess, req); err != nil {
            ae := auth.AsAuthError(err)
            http.Error(w, ae.Message, ae.Status)
            return
        }
        h(w, r)
    }
    switch method {
    case "POST":
        router.Post(path, wrapped)
    default:
        panic("registerOpHTTP: unsupported method " + method)
    }
}
```

(Note: Huma's `huma.Operation.Middlewares` field is the canonical hook. If the installed Huma version differs, fall back to a per-operation `OperationMiddleware` registered via `huma.Register` with an explicit `Middlewares` field on the operation struct — the exact API surface is library-version-specific. Confirm via `go doc github.com/danielgtaylor/huma/v2.Operation` before settling.)

- [ ] **Step 3: Build.**

```bash
go build ./...
```

If `op.Middlewares` doesn't exist in the pinned Huma version, switch to the alternative: implement the auth check inline as the first statement in each handler via a tiny `requireAuth(ctx, req)` helper. The downside is the call has to be in every handler — call out to the writer of Task 9 to use that helper consistently.

- [ ] **Step 4: Commit.**

```bash
git add pkg/auth/middleware.go pkg/server/operations.go
git commit -m "feat(auth): LoadSession middleware + registerOp helper

LoadSession reads picotera_session cookie, validates via SessionStore,
fetches account fresh from DB, attaches *Session to ctx. Disabled
accounts get 403 immediately. registerOp wraps huma.Register so every
operation's AuthRequirement is visible at the call site."
```

---

### Task 9: Migrate existing `registerOperations` to `registerOp`

**Goal:** Every existing `huma.Register(...)` becomes `registerOp(...)` with an explicit `AuthRequirement`. Auth enforcement is on starting now.

**Files:**
- Modify: `pkg/server/server.go` (the body of `registerOperations()`)

**Acceptance Criteria:**
- [ ] Every `huma.Register` call in `registerOperations()` becomes a `registerOp` call with the right `contract.AuthRequirement`.
- [ ] Admin-only operations use `contract.AuthRequirement{Kind: contract.AuthAdmin}`.
- [ ] Permission-gated operations use `contract.RequirePermission(...)`.
- [ ] The new auth/me/enrollment operations are NOT yet registered here (their handlers don't exist yet — covered in Tasks 10–12).
- [ ] `mise run server` boots successfully.
- [ ] Probing any existing endpoint with `curl http://localhost:9898/api/picotera/providers` (no cookie) returns 401 with `no_session` code.

**Verify:** `curl -i http://localhost:9898/api/picotera/providers | head` shows `HTTP/1.1 401`.

**Steps:**

- [ ] **Step 1: Map each existing operation.**

Inventory the existing `registerOperations` body (in `pkg/server/server.go:170` area). Each `huma.Register(mgmt, contract.OperationListProviders, s.handleListProviders)` becomes:

```go
registerOp(mgmt, contract.OperationListProviders, s.handleListProviders,
    contract.AuthRequirement{Kind: contract.AuthAdmin})
```

Use this mapping (from `design.md` §6 and `api.md` "Existing operations"):

| Op | Auth |
|---|---|
| Providers (all) | Admin |
| Models read (`List`, `Get`) | `RequirePermission(PermViewModels)` |
| Models write (`Put`, `Delete`) | Admin |
| Endpoints read | `RequirePermission(PermViewModels)` (models + endpoints reads share this gate) |
| Endpoints write | Admin |
| ProviderEndpoints (all) | Admin |
| FetchModels | Admin |
| Requests list/get/spans | `RequirePermission(PermViewOwnUsage)` for list/get; `RequirePermission(PermViewOwnTraces)` for spans |
| Traces (request_traces) | `RequirePermission(PermViewOwnTraces)` |
| ExchangeRates (all) | Admin |
| MatchPricing | Admin |
| ApiKeys (all) | `RequirePermission(PermManageOwnAPIKeys)` |
| Scripts (all) | Admin |
| Kv (all) | Admin |
| Projects (all) | Admin |
| Simulate | Admin |
| ListMappings (existing huma reg if present) | Admin |

(Walk the actual file and tag each one — adapt as needed.)

- [ ] **Step 2: Edit `pkg/server/server.go`.**

For each `huma.Register` in `registerOperations()`, replace with `registerOp` and add the `AuthRequirement`. For example:

```go
registerOp(mgmt, contract.OperationListProviders, s.handleListProviders,
    contract.AuthRequirement{Kind: contract.AuthAdmin})
registerOp(mgmt, contract.OperationListApiKeys, s.handleListApiKeys,
    contract.RequirePermission(contract.PermManageOwnAPIKeys))
// ... etc
```

Also: install `auth.LoadSession` as chi middleware near the top of `NewServer`:

```go
router.Use(auth.LoadSession(server.config, server.queries, server.sessionStore))
```

(Server struct will need `sessionStore *auth.SessionStore` — add it; it'll be initialized in Task 10.)

- [ ] **Step 3: Build and smoke-test.**

```bash
go build ./...
MISE_DISABLE_TOOLS=pnpm mise run server &
sleep 3
curl -s -o /dev/null -w 'no_cookie: %{http_code}\n' http://localhost:9898/api/picotera/providers
# expect: no_cookie: 401
kill %1
```

- [ ] **Step 4: Commit.**

```bash
git add pkg/server/server.go
git commit -m "feat(auth): gate all existing management ops behind registerOp

Every existing huma.Register call now declares its AuthRequirement
explicitly via registerOp. Provider/script/kv/etc. mutations require
admin; api-key/request/model reads are permission-gated (dormant in
Phase 1 because no non-admin accounts exist yet). The dashboard now
requires a session for the management API."
```

---

### Task 10: Auth HTTP handlers — `handle_auth.go`

**Goal:** Implement `/auth/status`, `/auth/login/begin`, `/auth/login/complete`, `/auth/logout`, plus the registrations in `registerOperations`.

**Files:**
- Create: `pkg/server/handle_auth.go`
- Modify: `pkg/server/server.go` (add registrations + initialize `sessionStore` + `webauthn`)
- Create: `pkg/server/handle_auth_test.go` (limited; ceremony coverage is integration-only)

**Acceptance Criteria:**
- [ ] `GET /api/picotera/auth/status` returns `{bootstrapped: false}` on fresh DB, `true` once an admin exists.
- [ ] `POST /api/picotera/auth/login/begin` returns request options + sets `picotera_ceremony` cookie. Fails with 503 `not_bootstrapped` when no admin exists.
- [ ] `POST /api/picotera/auth/login/complete` verifies an assertion via `go-webauthn`, issues a session, sets `picotera_session` cookie.
- [ ] `POST /api/picotera/auth/logout` is idempotent, returns 204, clears the session cookie with matching Path.

**Verify:** `curl -s http://localhost:9898/api/picotera/auth/status` returns `{"bootstrapped":false}` (or `true` after Task 13). Full ceremony tested manually in browser after Task 18.

**Steps:**

- [ ] **Step 1: Initialize `sessionStore` + `webauthn` in `NewServer`.**

In `pkg/server/server.go::NewServer`, after the config is parsed and queries created, add:

```go
sessionStore := auth.NewSessionStore(kvStore, cfg.SessionTTL)
wa, err := auth.NewWebAuthn(cfg)
if err != nil {
    return nil, fmt.Errorf("init webauthn: %w", err)
}
server.sessionStore = sessionStore
server.webauthn = wa
log.Infof("auth: RP ID = %s, origins = %v", cfg.WebAuthnRPID, cfg.PublicOrigins)
```

Add the fields:

```go
type Server struct {
    // ... existing fields
    sessionStore *auth.SessionStore
    webauthn     *webauthn.WebAuthn
}
```

- [ ] **Step 2: Write `pkg/server/handle_auth.go`.**

```go
package server

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"

    "github.com/go-webauthn/webauthn/protocol"
    "github.com/go-webauthn/webauthn/webauthn"

    "picotera/pkg/auth"
    "picotera/pkg/contract"
    "picotera/pkg/kv"
)

// --- GET /auth/status -------------------------------------------------------

type authStatusOut struct {
    Body contract.AuthStatus
}

func (s *Server) handleAuthStatus(ctx context.Context, _ *struct{}) (*authStatusOut, error) {
    res, err := s.queries.HasAnyActiveAdmin(ctx)
    if err != nil {
        return nil, err
    }
    return &authStatusOut{Body: contract.AuthStatus{Bootstrapped: res.Bootstrapped}}, nil
}

// --- POST /auth/login/begin -------------------------------------------------

type loginBeginIn struct {
    // empty body
}
type loginBeginOut struct {
    Body protocol.PublicKeyCredentialRequestOptions
}

const ceremonyTTL = 5 * 60 // seconds

func (s *Server) handleLoginBegin(ctx context.Context, _ *loginBeginIn) (*loginBeginOut, error) {
    res, err := s.queries.HasAnyActiveAdmin(ctx)
    if err != nil {
        return nil, err
    }
    if !res.Bootstrapped {
        return nil, auth.ErrNotBootstrapped()
    }

    options, sessionData, err := s.webauthn.BeginDiscoverableLogin(auth.LoginOptions()...)
    if err != nil {
        return nil, fmt.Errorf("begin login: %w", err)
    }

    token, err := newCeremonyToken()
    if err != nil {
        return nil, err
    }
    if err := s.kvStore.SetEx(ctx, "webauthn_ceremony:login:"+token, mustJSON(sessionData), 5*time.Minute); err != nil {
        return nil, err
    }

    // Set ceremony cookie on the response. Huma typed handlers can't set cookies
    // directly; do this via huma's Header / Cookie support, or via a raw chi route
    // — for typed handlers we add a "Set-Cookie" header to the output.
    // Easiest: declare loginBeginOut with a `SetCookie []string` raw header field.
    return &loginBeginOut{Body: options.Response}, nil
}
```

**Cookie-on-typed-output snag:** Huma's typed I/O doesn't trivially set cookies. Two paths:
1. Use a raw chi handler for `/auth/login/begin` and `.../complete` (drop the typed wrapper for these two), with manual JSON encoding.
2. Use Huma's response Header struct on the output type: `Headers struct { SetCookie []string \`header:"Set-Cookie"\` }`.

Go with (1) for these three endpoints (`login/begin`, `login/complete`, `logout`) plus `me/credentials/register/{begin,complete}` and the enrollment ones — all cookie-touching endpoints become raw chi handlers registered via `registerOpHTTP`. Documented in design — implementer adapts.

- [ ] **Step 3: Implement the rest.**

Replace the typed login-begin body with raw chi:

```go
// (in pkg/server/server.go::registerOperations or a new wiring spot)
registerOpHTTP(server.router, "POST", "/api/picotera/auth/login/begin",
    contract.AuthRequirement{Kind: contract.AuthPublic}, server.handleLoginBeginHTTP)
registerOpHTTP(server.router, "POST", "/api/picotera/auth/login/complete",
    contract.AuthRequirement{Kind: contract.AuthPublic}, server.handleLoginCompleteHTTP)
registerOpHTTP(server.router, "POST", "/api/picotera/auth/logout",
    contract.AuthRequirement{Kind: contract.AuthPublic}, server.handleLogoutHTTP)
registerOp(mgmt, contract.OperationAuthStatus, server.handleAuthStatus,
    contract.AuthRequirement{Kind: contract.AuthPublic})
```

Handler bodies (in `handle_auth.go`):

```go
func (s *Server) handleLoginBeginHTTP(w http.ResponseWriter, r *http.Request) {
    res, err := s.queries.HasAnyActiveAdmin(r.Context())
    if err != nil { writeAuthErr(w, err); return }
    if !res.Bootstrapped { writeAuthErr(w, auth.ErrNotBootstrapped()); return }

    options, sessionData, err := s.webauthn.BeginDiscoverableLogin(auth.LoginOptions()...)
    if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error())); return }

    token, _ := newCeremonyToken()
    payload, _ := json.Marshal(sessionData)
    if err := s.kvStore.SetEx(r.Context(), "webauthn_ceremony:login:"+token, string(payload), 5*time.Minute); err != nil {
        writeAuthErr(w, err); return
    }
    http.SetCookie(w, auth.CeremonyCookie(s.config, r, token))
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(options.Response)
}

func (s *Server) handleLoginCompleteHTTP(w http.ResponseWriter, r *http.Request) {
    cer, err := r.Cookie(auth.CeremonyCookieName)
    if err != nil || cer.Value == "" {
        writeAuthErr(w, auth.ErrWebAuthnCeremony("no ceremony cookie")); return
    }
    raw, ok, err := s.kvStore.Get(r.Context(), "webauthn_ceremony:login:"+cer.Value)
    if err != nil { writeAuthErr(w, err); return }
    if !ok { writeAuthErr(w, auth.ErrWebAuthnCeremony("ceremony expired")); return }
    var sessionData webauthn.SessionData
    if err := json.Unmarshal([]byte(raw), &sessionData); err != nil {
        writeAuthErr(w, auth.ErrWebAuthnCeremony("ceremony corrupt")); return
    }
    // Parse the assertion off the request body.
    parsed, err := protocol.ParseCredentialRequestResponseBody(r.Body)
    if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error())); return }

    // Discoverable login: look up account by userHandle (which is webauthn_user_handle).
    handle := parsed.Response.UserHandle
    account, err := s.queries.GetAccountByWebauthnUserHandle(r.Context(), handle)
    if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony("unknown account")); return }
    if account.Disabled { writeAuthErr(w, auth.ErrAccountDisabled()); return }

    creds, err := s.queries.ListCredentialsByAccount(r.Context(), account.ID)
    if err != nil { writeAuthErr(w, err); return }
    wu := &auth.WebAuthnAccount{Account: &account, Credentials: creds}
    cred, err := s.webauthn.ValidateDiscoverableLogin(
        func(rawID, _ []byte) (webauthn.User, error) { return wu, nil },
        sessionData, parsed,
    )
    if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error())); return }

    // Update credential usage; clean up ceremony entry.
    _ = s.queries.UpdateCredentialUsage(r.Context(), db.UpdateCredentialUsageParams{
        ID: matchCredentialID(creds, cred.ID), SignCount: int64(cred.Authenticator.SignCount),
    })
    _ = s.kvStore.Del(r.Context(), "webauthn_ceremony:login:"+cer.Value)
    http.SetCookie(w, auth.ClearedCeremonyCookie(s.config, r))

    // Issue session
    ip := auth.ClientIPHelperForHandler(r, s.config.TrustProxy) // helper from middleware.go (rename if needed)
    token, _, err := s.sessionStore.Issue(r.Context(), account.ID, ip)
    if err != nil { writeAuthErr(w, err); return }
    http.SetCookie(w, auth.FreshSessionCookie(s.config, r, account.ID, token, s.config.SessionTTL))

    // Respond with the new SessionView
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(sessionView(&account))
}

func (s *Server) handleLogoutHTTP(w http.ResponseWriter, r *http.Request) {
    if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
        if id, tok, ok := auth.ParseCookieValue(c.Value); ok {
            _ = s.sessionStore.Revoke(r.Context(), id, tok)
        }
    }
    http.SetCookie(w, auth.ClearedSessionCookie(s.config, r))
    w.WriteHeader(http.StatusNoContent)
}

// helpers
func newCeremonyToken() (string, error) {
    b := make([]byte, 16)
    if _, err := rand.Read(b); err != nil { return "", err }
    return base64.RawURLEncoding.EncodeToString(b), nil
}
func mustJSON(v interface{}) string { b, _ := json.Marshal(v); return string(b) }
func writeAuthErr(w http.ResponseWriter, err error) {
    ae := auth.AsAuthError(err)
    if ae == nil { http.Error(w, err.Error(), 500); return }
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(ae.Status)
    json.NewEncoder(w).Encode(map[string]string{"code": ae.Code, "message": ae.Message})
}
func sessionView(a *db.Account) contract.SessionView {
    return contract.SessionView{
        ID: a.ID, Username: a.Username, DisplayName: a.DisplayName,
        Role: a.Role, Permissions: auth.PermissionsView(a),
    }
}
func matchCredentialID(creds []db.WebauthnCredential, raw []byte) int32 {
    for _, c := range creds {
        if bytes.Equal(c.CredentialID, raw) { return c.ID }
    }
    return 0
}
```

(Imports get adjusted by `goimports`.)

- [ ] **Step 4: Build and smoke-test status.**

```bash
go build ./...
MISE_DISABLE_TOOLS=pnpm mise run server &
sleep 3
curl -s http://localhost:9898/api/picotera/auth/status
# expect: {"bootstrapped":false}
kill %1
```

- [ ] **Step 5: Commit.**

```bash
git add pkg/server/handle_auth.go pkg/server/server.go
git commit -m "feat(auth): /auth/status, /auth/login/{begin,complete}, /auth/logout

Login uses WebAuthn discoverable credentials (no allowCredentials). Ceremony
state lives in KV under webauthn_ceremony:login:<random> keyed by a 5-min
cookie. Logout is idempotent and matches the issue path so the browser
actually clears the cookie. Status surfaces whether an admin is enrolled,
so LoginView can branch on bootstrap state."
```

---

### Task 11: Enrollment HTTP handlers — `handle_enrollment.go`

**Goal:** Preview + register-begin + register-complete for all three intents (bootstrap / invite / reset). Phase 1 only the bootstrap path can actually be triggered (no invitations exist yet), but the complete handler must support all three from day one.

**Files:**
- Create: `pkg/server/handle_enrollment.go`
- Modify: `pkg/server/server.go` (register the three routes)

**Acceptance Criteria:**
- [ ] `GET /enrollments/{token}` returns `EnrollmentPreview` or 410.
- [ ] `POST /enrollments/{token}/register/begin` returns `PublicKeyCredentialCreationOptions`, stashes `SessionData` in KV under `webauthn_ceremony:enroll:<token>`.
- [ ] `POST /enrollments/{token}/register/complete` runs the intent-specific TX (bootstrap = INSERT account; invite = INSERT credential; reset = DELETE + INSERT credentials + revoke sessions after TX), issues session, returns `SessionView`.
- [ ] Username/display_name validation rejects with `400 invalid_username` / `400 invalid_display_name` (CLAUDE.md strict).
- [ ] Bootstrap path enforces unique username; if collision: `409 username_taken`.

**Verify:** After Task 13 (CLI), end-to-end: `picotera enroll-admin` → open URL → ceremony → session cookie present → `curl --cookie picotera_session=... http://localhost:9898/api/picotera/me` returns the new admin.

**Steps:**

- [ ] **Step 1: Write the preview handler (typed I/O, no cookies).**

```go
type previewIn struct {
    Token string `path:"token"`
}
type previewOut struct {
    Body contract.EnrollmentPreview
}

func (s *Server) handlePreviewEnrollment(ctx context.Context, in *previewIn) (*previewOut, error) {
    e, err := auth.LoadEnrollment(ctx, s.queries, in.Token)
    if err != nil { return nil, err }
    out := contract.EnrollmentPreview{
        Intent:    e.Intent,
        ExpiresAt: e.ExpiresAt,
    }
    if e.TargetAccountID.Valid {
        a, err := s.queries.GetAccountByID(ctx, e.TargetAccountID.Int32)
        if err == nil {
            out.Target = &contract.EnrollmentTarget{Username: a.Username, DisplayName: a.DisplayName}
        }
    }
    return &previewOut{Body: out}, nil
}
```

- [ ] **Step 2: Write the begin handler (raw chi — sets ceremony cookie/storage).**

```go
type enrollBeginBody struct {
    Username    string `json:"username,omitempty"`    // bootstrap only
    DisplayName string `json:"displayName,omitempty"` // bootstrap only
}

func (s *Server) handleEnrollmentBeginHTTP(w http.ResponseWriter, r *http.Request) {
    token := chi.URLParam(r, "token")
    e, err := auth.LoadEnrollment(r.Context(), s.queries, token)
    if err != nil { writeAuthErr(w, err); return }

    var body enrollBeginBody
    if r.ContentLength > 0 {
        _ = json.NewDecoder(r.Body).Decode(&body)
    }

    var wu *auth.WebAuthnAccount
    switch e.Intent {
    case auth.IntentBootstrap:
        if err := auth.ValidateUsername(body.Username); err != nil { writeAuthErr(w, err); return }
        if err := auth.ValidateDisplayName(body.DisplayName); err != nil { writeAuthErr(w, err); return }
        if _, err := s.queries.GetAccountByUsername(r.Context(), body.Username); err == nil {
            writeAuthErr(w, auth.ErrUsernameTaken()); return
        }
        handle, err := auth.GenerateUserHandle()
        if err != nil { writeAuthErr(w, err); return }
        wu = &auth.WebAuthnAccount{Account: &db.Account{
            ID: -1, Username: body.Username, DisplayName: body.DisplayName,
            WebauthnUserHandle: handle, Role: "admin",
        }}
    case auth.IntentInvite, auth.IntentReset:
        a, err := s.queries.GetAccountByID(r.Context(), e.TargetAccountID.Int32)
        if err != nil { writeAuthErr(w, err); return }
        creds, _ := s.queries.ListCredentialsByAccount(r.Context(), a.ID)
        wu = &auth.WebAuthnAccount{Account: &a, Credentials: creds}
    }

    options, sessionData, err := s.webauthn.BeginRegistration(wu, auth.RegistrationOptions(nil)...)
    if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error())); return }

    // Store the ceremony plus the pending user info for bootstrap (so /complete
    // doesn't have to re-validate the username).
    payload := struct {
        Data        webauthn.SessionData
        Bootstrap   *db.Account `json:",omitempty"`
    }{Data: *sessionData, Bootstrap: wu.Account}
    raw, _ := json.Marshal(payload)
    if err := s.kvStore.SetEx(r.Context(), "webauthn_ceremony:enroll:"+token, string(raw), 5*time.Minute); err != nil {
        writeAuthErr(w, err); return
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(options.Response)
}
```

- [ ] **Step 3: Write the complete handler.**

```go
func (s *Server) handleEnrollmentCompleteHTTP(w http.ResponseWriter, r *http.Request) {
    token := chi.URLParam(r, "token")
    e, err := auth.LoadEnrollment(r.Context(), s.queries, token)
    if err != nil { writeAuthErr(w, err); return }

    raw, ok, _ := s.kvStore.Get(r.Context(), "webauthn_ceremony:enroll:"+token)
    if !ok { writeAuthErr(w, auth.ErrWebAuthnCeremony("ceremony expired")); return }
    var stored struct {
        Data      webauthn.SessionData
        Bootstrap *db.Account
    }
    _ = json.Unmarshal([]byte(raw), &stored)

    parsed, err := protocol.ParseCredentialCreationResponseBody(r.Body)
    if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error())); return }

    var account db.Account
    var credentialRow db.WebauthnCredential

    tx, err := s.dbPool.Begin(r.Context())
    if err != nil { writeAuthErr(w, err); return }
    defer tx.Rollback(r.Context())
    qtx := s.queries.WithTx(tx)

    switch e.Intent {
    case auth.IntentBootstrap:
        wu := &auth.WebAuthnAccount{Account: stored.Bootstrap}
        cred, err := s.webauthn.CreateCredential(wu, stored.Data, parsed)
        if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error())); return }
        // Insert account first
        a, err := qtx.InsertAccount(r.Context(), db.InsertAccountParams{
            Username: stored.Bootstrap.Username, DisplayName: stored.Bootstrap.DisplayName,
            WebauthnUserHandle: stored.Bootstrap.WebauthnUserHandle,
            Role: "admin",
            CanViewOwnUsage: true, CanManageOwnApiKeys: true,
            CanViewModels: true, CanViewOwnTraces: true,
            Disabled: false,
        })
        if err != nil { writeAuthErr(w, auth.ErrUsernameTaken()); return }
        account = a
        credentialRow = insertCredentialFor(qtx, r.Context(), a.ID, cred, stored.Bootstrap.WebauthnUserHandle)

    case auth.IntentInvite:
        a, err := qtx.GetAccountByID(r.Context(), e.TargetAccountID.Int32)
        if err != nil { writeAuthErr(w, auth.ErrEnrollmentConsumed()); return }
        creds, _ := qtx.ListCredentialsByAccount(r.Context(), a.ID)
        wu := &auth.WebAuthnAccount{Account: &a, Credentials: creds}
        cred, err := s.webauthn.CreateCredential(wu, stored.Data, parsed)
        if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error())); return }
        account = a
        credentialRow = insertCredentialFor(qtx, r.Context(), a.ID, cred, nil)

    case auth.IntentReset:
        a, err := qtx.GetAccountByID(r.Context(), e.TargetAccountID.Int32)
        if err != nil { writeAuthErr(w, auth.ErrEnrollmentConsumed()); return }
        // Delete all existing credentials
        if err := qtx.DeleteAllCredentialsForAccount(r.Context(), a.ID); err != nil {
            writeAuthErr(w, err); return
        }
        wu := &auth.WebAuthnAccount{Account: &a, Credentials: nil}
        cred, err := s.webauthn.CreateCredential(wu, stored.Data, parsed)
        if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error())); return }
        account = a
        credentialRow = insertCredentialFor(qtx, r.Context(), a.ID, cred, nil)
    }

    if err := auth.ConsumeEnrollment(r.Context(), qtx, token); err != nil {
        writeAuthErr(w, err); return
    }
    if err := tx.Commit(r.Context()); err != nil { writeAuthErr(w, err); return }

    // Post-TX best-effort cleanup
    _ = s.kvStore.Del(r.Context(), "webauthn_ceremony:enroll:"+token)
    if e.Intent == auth.IntentReset {
        _, _ = s.sessionStore.RevokeAllForAccount(r.Context(), account.ID)
    }
    _ = credentialRow // for log/audit if desired

    ip := auth.ClientIPHelperForHandler(r, s.config.TrustProxy)
    sessionToken, _, err := s.sessionStore.Issue(r.Context(), account.ID, ip)
    if err != nil { writeAuthErr(w, err); return }
    http.SetCookie(w, auth.FreshSessionCookie(s.config, r, account.ID, sessionToken, s.config.SessionTTL))

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(sessionView(&account))
}

func insertCredentialFor(q db.Querier, ctx context.Context, accountID int32, cred *webauthn.Credential, _ []byte) db.WebauthnCredential {
    transports := make([]string, len(cred.Transport))
    for i, t := range cred.Transport { transports[i] = string(t) }
    row, _ := q.InsertCredential(ctx, db.InsertCredentialParams{
        AccountID: accountID,
        CredentialID: cred.ID, PublicKey: cred.PublicKey,
        SignCount: int64(cred.Authenticator.SignCount),
        Transports: transports,
        Aaguid: cred.Authenticator.AAGUID,
        AttestationType: cred.AttestationType,
        BackupEligible: cred.Flags.BackupEligible,
        BackupState: cred.Flags.BackupState,
    })
    return row
}
```

Add the sqlc query `DeleteAllCredentialsForAccount` to `db/queries/webauthn_credential.sql`:

```sql
-- name: DeleteAllCredentialsForAccount :exec
DELETE FROM webauthn_credential WHERE account_id = $1;
```

Then `sqlc generate`.

- [ ] **Step 4: Register the three routes.**

In `server.go::registerOperations`:

```go
registerOp(mgmt, contract.OperationPreviewEnrollment, server.handlePreviewEnrollment,
    contract.AuthRequirement{Kind: contract.AuthPublic})
registerOpHTTP(server.router, "POST", "/api/picotera/enrollments/{token}/register/begin",
    contract.AuthRequirement{Kind: contract.AuthPublic}, server.handleEnrollmentBeginHTTP)
registerOpHTTP(server.router, "POST", "/api/picotera/enrollments/{token}/register/complete",
    contract.AuthRequirement{Kind: contract.AuthPublic}, server.handleEnrollmentCompleteHTTP)
```

- [ ] **Step 5: Build.**

```bash
sqlc generate
go build ./...
```

- [ ] **Step 6: Commit.**

```bash
git add pkg/server/handle_enrollment.go pkg/server/server.go db/queries/webauthn_credential.sql pkg/db/
git commit -m "feat(auth): enrollment ceremony handlers (bootstrap/invite/reset)

GET /enrollments/:token previews; POST .../register/begin issues the
WebAuthn ceremony; POST .../register/complete runs the intent-specific
TX. Reset path deletes existing credentials and revokes all sessions
post-commit. Phase 1 only exercises bootstrap; invite/reset wire up but
remain unreachable until Phase 2 ships the issuer endpoints."
```

---

### Task 12: `/me` and `/me/credentials/*` handlers — `handle_me.go`

**Goal:** Self-management endpoints. Already-authenticated paths.

**Files:**
- Create: `pkg/server/handle_me.go`
- Modify: `pkg/server/server.go` (register)

**Acceptance Criteria:**
- [ ] `GET /me` returns the caller's `SessionView`.
- [ ] `GET /me/credentials` returns own credentials (transports + backup state + suffix).
- [ ] `POST /me/credentials/register/begin` returns options; stashes ceremony in KV under `webauthn_ceremony:add:<session_token>` (no new cookie).
- [ ] `POST /me/credentials/register/complete` verifies attestation, inserts credential, returns view.
- [ ] `POST /me/credentials/delete` body `{id}` deletes own credential; refuses if count would drop to zero.

**Verify:** After Task 18 (MeView), exercise via browser; for now: `curl -i --cookie picotera_session=... http://localhost:9898/api/picotera/me` returns 200.

**Steps:**

- [ ] **Step 1: Implement.**

Use the patterns from Task 11 (raw chi for add credential begin/complete; typed for /me, list, delete). Key wrinkle: ceremony KV key uses the caller's session token. The session token is in the request — `auth.SessionFromContext(r.Context()).Token`.

```go
func (s *Server) handleAddCredentialBeginHTTP(w http.ResponseWriter, r *http.Request) {
    sess := auth.SessionFromContext(r.Context())
    creds, _ := s.queries.ListCredentialsByAccount(r.Context(), sess.Account.ID)
    var exclude []protocol.CredentialDescriptor
    for _, c := range creds {
        exclude = append(exclude, protocol.CredentialDescriptor{
            Type: protocol.PublicKeyCredentialType, CredentialID: c.CredentialID,
        })
    }
    wu := &auth.WebAuthnAccount{Account: sess.Account, Credentials: creds}
    options, sessionData, err := s.webauthn.BeginRegistration(wu, auth.RegistrationOptions(exclude)...)
    if err != nil { writeAuthErr(w, auth.ErrWebAuthnCeremony(err.Error())); return }
    payload, _ := json.Marshal(sessionData)
    if err := s.kvStore.SetEx(r.Context(), "webauthn_ceremony:add:"+sess.Token, string(payload), 5*time.Minute); err != nil {
        writeAuthErr(w, err); return
    }
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(options.Response)
}

// completion: read body, look up ceremony by session token, verify, insert, return CredentialView.

func (s *Server) handleDeleteMyCredential(ctx context.Context, in *deleteCredIn) (*emptyOut, error) {
    sess := auth.SessionFromContext(ctx)
    count, err := s.queries.CountCredentialsByAccount(ctx, sess.Account.ID)
    if err != nil { return nil, err }
    if count <= 1 { return nil, auth.ErrLastPasskey() }
    if err := s.queries.DeleteCredentialByID(ctx, db.DeleteCredentialByIDParams{
        ID: in.Body.ID, AccountID: sess.Account.ID,
    }); err != nil {
        return nil, auth.ErrCredentialNotFound()
    }
    return &emptyOut{}, nil
}
```

(Wire up registrations as in earlier tasks.)

- [ ] **Step 2: Build and smoke-test.**

```bash
go build ./...
```

- [ ] **Step 3: Commit.**

```bash
git add pkg/server/handle_me.go pkg/server/server.go
git commit -m "feat(auth): /me + /me/credentials self-management"
```

---

### Task 13: `picotera enroll-admin` CLI subcommand

**Goal:** Add the cobra subcommand with `--new` and `--reset --username` modes.

**Files:**
- Modify: `cmd/picotera/main.go`

**Acceptance Criteria:**
- [ ] `picotera enroll-admin` (no flags) errors when any non-disabled admin exists.
- [ ] `picotera enroll-admin --new` always issues a new bootstrap enrollment.
- [ ] `picotera enroll-admin --reset --username alice` checks alice is admin (404 otherwise), issues reset enrollment.
- [ ] All variants print a URL composed from `cfg.PublicOrigins[0]`.
- [ ] Subcommand runs migrations before its DB queries.

**Verify:** End-to-end: `mise build && ./picotera enroll-admin` prints a URL; opening it in the browser works.

**Steps:**

- [ ] **Step 1: Refactor main.go.**

Add a `bootstrapHelper(ctx)` that does `configx.Parse() → connect DB → goose migrate → return *db.Queries`. Used by both `OnStart` (the server) and `enroll-admin`.

```go
func bootstrapHelper(ctx context.Context) (*configx.Config, *pgxpool.Pool, *db.Queries, error) {
    cfg, err := configx.Parse()
    if err != nil { return nil, nil, nil, err }
    pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
    if err != nil { return nil, nil, nil, err }
    if err := runMigrations(cfg.DatabaseURL); err != nil { return nil, nil, nil, err }
    return cfg, pool, db.New(pool), nil
}
```

- [ ] **Step 2: Add the cobra subcommand.**

```go
var enrollNew, enrollReset bool
var enrollUsername string

enrollCmd := &cobra.Command{
    Use:   "enroll-admin",
    Short: "Issue a passkey enrollment URL for an admin account.",
    Run: func(cmd *cobra.Command, args []string) {
        ctx := context.Background()
        cfg, pool, q, err := bootstrapHelper(ctx)
        if err != nil { log.Fatalf("bootstrap: %v", err) }
        defer pool.Close()

        if enrollReset {
            if enrollUsername == "" { log.Fatalf("--reset requires --username NAME") }
            a, err := q.GetAccountByUsername(ctx, enrollUsername)
            if err != nil { log.Fatalf("user %q not found", enrollUsername) }
            if a.Role != "admin" { log.Fatalf("user %q is not admin; use the dashboard's reissue-enrollment for non-admin accounts", enrollUsername) }
            token, exp, err := auth.IssueEnrollment(ctx, q, auth.IntentReset, &a.ID, 24*time.Hour)
            if err != nil { log.Fatalf("issue enrollment: %v", err) }
            fmt.Printf("Reset enrollment URL (expires %s):\n%s/enroll/%s\n", exp.Format(time.RFC3339), cfg.PublicOrigins[0], token)
            return
        }

        if !enrollNew {
            // default: error if any admin exists
            r, err := q.HasAnyActiveAdmin(ctx)
            if err != nil { log.Fatalf("status check: %v", err) }
            if r.Bootstrapped {
                log.Fatalf("an admin already exists; pass --new to add another admin, or --reset --username NAME to recover a specific admin")
            }
        }

        token, exp, err := auth.IssueEnrollment(ctx, q, auth.IntentBootstrap, nil, 24*time.Hour)
        if err != nil { log.Fatalf("issue enrollment: %v", err) }
        fmt.Printf("Bootstrap enrollment URL (expires %s):\n%s/enroll/%s\n", exp.Format(time.RFC3339), cfg.PublicOrigins[0], token)
    },
}
enrollCmd.Flags().BoolVar(&enrollNew, "new", false, "always create a new admin (do not error if admins exist)")
enrollCmd.Flags().BoolVar(&enrollReset, "reset", false, "reset a specific admin's passkeys")
enrollCmd.Flags().StringVar(&enrollUsername, "username", "", "username to reset (only with --reset)")
cli.Root().AddCommand(enrollCmd)
```

- [ ] **Step 3: Build and smoke-test.**

```bash
go build -o picotera ./cmd/picotera
podman-compose up -d
sleep 2
./picotera enroll-admin
# expect: Bootstrap enrollment URL: http://localhost:9898/enroll/<token>
```

- [ ] **Step 4: Commit.**

```bash
git add cmd/picotera/main.go
git commit -m "feat(cli): picotera enroll-admin subcommand

Default mode bootstraps a brand-new admin (errors if any admin exists).
--new always issues a fresh bootstrap enrollment. --reset --username NAME
issues a reset enrollment for an existing admin (errors on non-admin
target). All variants print a URL composed from PUBLIC_ORIGIN."
```

---

### Task 14: OpenAPI security scheme + regenerate dashboard types

**Goal:** Reflect the cookie security scheme in OpenAPI; regenerate the dashboard's TypeScript types.

**Files:**
- Modify: `pkg/server/server.go` (security scheme registration)
- Modify: `openapi.yaml` (via `mise run openapi`)
- Modify: `dashboard/src/openapi-types.d.ts` (via `pnpm --dir dashboard generate-openapi`)

**Acceptance Criteria:**
- [ ] `openapi.yaml` contains a `picoteraSession` cookie security scheme.
- [ ] Non-public operations reference it.
- [ ] Frontend type regen completes without error.

**Verify:** `grep -A2 'picoteraSession' openapi.yaml`; `pnpm --dir dashboard type-check`.

**Steps:**

- [ ] **Step 1: Register the security scheme.**

After `api := humachi.New(...)` in `NewServer`:

```go
api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{
    "picoteraSession": {
        Type: "apiKey", In: "cookie", Name: auth.SessionCookieName,
    },
}
```

In `registerOp`, attach the security requirement to non-public operations (Huma's `Operation.Security`).

- [ ] **Step 2: Regen.**

```bash
MISE_DISABLE_TOOLS=pnpm mise run openapi
pnpm --dir dashboard generate-openapi
pnpm --dir dashboard type-check
```

- [ ] **Step 3: Commit.**

```bash
git add openapi.yaml pkg/server/server.go dashboard/src/openapi-types.d.ts
git commit -m "feat(openapi): picoteraSession cookie security scheme"
```

---

### Task 15: Dashboard — `webauthn.ts` wrapper + queryKeys + client.ts fetchers + useSession

**Goal:** Lay the data-layer plumbing the new views will consume.

**Files:**
- Create: `dashboard/src/api/webauthn.ts`
- Modify: `dashboard/src/api/queryKeys.ts`
- Modify: `dashboard/src/api/client.ts`
- Create: `dashboard/src/composables/useSession.ts`

**Acceptance Criteria:**
- [ ] `webauthnCreate(options, signal?)` and `webauthnGet(options, signal?)` convert between server's URL-safe base64 strings and the browser's `ArrayBuffer` inputs/outputs.
- [ ] `webauthn.ts` exports a typed `WebAuthnUserCancelled` error mapped from `DOMException` `NotAllowedError` / `AbortError`.
- [ ] `queryKeys.session.current()`, `queryKeys.accounts.list()`, `queryKeys.credentials.mine()`, `queryKeys.enrollments.detail(token)` exist with typed return.
- [ ] `client.ts` exports `fetchAuthStatus`, `fetchMe`, `logout`, `beginLogin`/`completeLogin`, `fetchEnrollment`/`beginRegister`/`completeRegister`, `fetchMyCredentials`/`addCredentialBegin`/`addCredentialComplete`/`deleteMyCredential`.
- [ ] `useSession()` returns `{user, isPending, isAdmin, can(perm)}`.

**Verify:** `pnpm --dir dashboard type-check` passes.

**Steps:**

- [ ] **Step 1: Write `dashboard/src/api/webauthn.ts`.**

```ts
function b64urlToBuf(s: string): ArrayBuffer {
  const pad = '='.repeat((4 - (s.length % 4)) % 4)
  const b64 = (s + pad).replace(/-/g, '+').replace(/_/g, '/')
  const bin = atob(b64)
  const buf = new ArrayBuffer(bin.length)
  const view = new Uint8Array(buf)
  for (let i = 0; i < bin.length; i++) view[i] = bin.charCodeAt(i)
  return buf
}

function bufToB64url(buf: ArrayBuffer): string {
  const bin = String.fromCharCode(...new Uint8Array(buf))
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export class WebAuthnUserCancelled extends Error {
  constructor() { super('Passkey ceremony cancelled or timed out'); this.name = 'WebAuthnUserCancelled' }
}

export async function webauthnCreate(optionsJSON: any, signal?: AbortSignal): Promise<any> {
  const options = decodeCreationOptions(optionsJSON)
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.create({ publicKey: options, signal })) as PublicKeyCredential | null
  } catch (e: any) {
    if (e?.name === 'NotAllowedError' || e?.name === 'AbortError') throw new WebAuthnUserCancelled()
    throw e
  }
  if (!cred) throw new WebAuthnUserCancelled()
  return encodeCreationResponse(cred)
}

export async function webauthnGet(optionsJSON: any, signal?: AbortSignal): Promise<any> {
  const options = decodeRequestOptions(optionsJSON)
  let cred: PublicKeyCredential | null
  try {
    cred = (await navigator.credentials.get({ publicKey: options, signal })) as PublicKeyCredential | null
  } catch (e: any) {
    if (e?.name === 'NotAllowedError' || e?.name === 'AbortError') throw new WebAuthnUserCancelled()
    throw e
  }
  if (!cred) throw new WebAuthnUserCancelled()
  return encodeAssertionResponse(cred)
}

function decodeCreationOptions(j: any): PublicKeyCredentialCreationOptions {
  return {
    ...j,
    challenge: b64urlToBuf(j.challenge),
    user: { ...j.user, id: b64urlToBuf(j.user.id) },
    excludeCredentials: (j.excludeCredentials ?? []).map((c: any) => ({ ...c, id: b64urlToBuf(c.id) })),
  }
}
function decodeRequestOptions(j: any): PublicKeyCredentialRequestOptions {
  return {
    ...j,
    challenge: b64urlToBuf(j.challenge),
    allowCredentials: (j.allowCredentials ?? []).map((c: any) => ({ ...c, id: b64urlToBuf(c.id) })),
  }
}
function encodeCreationResponse(c: PublicKeyCredential): any {
  const r = c.response as AuthenticatorAttestationResponse
  return {
    id: c.id, rawId: bufToB64url(c.rawId), type: c.type,
    response: {
      clientDataJSON: bufToB64url(r.clientDataJSON),
      attestationObject: bufToB64url(r.attestationObject),
      transports: (r as any).getTransports?.() ?? [],
    },
    clientExtensionResults: c.getClientExtensionResults(),
    authenticatorAttachment: (c as any).authenticatorAttachment ?? null,
  }
}
function encodeAssertionResponse(c: PublicKeyCredential): any {
  const r = c.response as AuthenticatorAssertionResponse
  return {
    id: c.id, rawId: bufToB64url(c.rawId), type: c.type,
    response: {
      clientDataJSON: bufToB64url(r.clientDataJSON),
      authenticatorData: bufToB64url(r.authenticatorData),
      signature: bufToB64url(r.signature),
      userHandle: r.userHandle ? bufToB64url(r.userHandle) : null,
    },
    clientExtensionResults: c.getClientExtensionResults(),
  }
}
```

- [ ] **Step 2: Extend `queryKeys.ts`.**

```ts
export const queryKeys = {
  // ... existing keys ...
  session: {
    all: () => ['session'] as const,
    current: () => ['session', 'current'] as const,
  },
  accounts: {
    all: () => ['accounts'] as const,
    list: () => ['accounts', 'list'] as const,
    detail: (id: number) => ['accounts', 'detail', id] as const,
  },
  credentials: {
    all: () => ['credentials'] as const,
    mine: () => ['credentials', 'mine'] as const,
  },
  enrollments: {
    detail: (token: string) => ['enrollments', token] as const,
  },
} as const
```

- [ ] **Step 3: Extend `client.ts`.**

Add the named fetcher functions. Use the existing `api` (openapi-fetch instance) and `ApiRequestError` pattern. Example:

```ts
export async function fetchMe(): Promise<SessionView> {
  const { data, error } = await api.GET('/me')
  if (error) throw new ApiRequestError(error)
  return data
}
export async function logout(): Promise<void> {
  await api.POST('/auth/logout')
}
// etc.
export function invalidateSession() { return queryClient.removeQueries({ queryKey: queryKeys.session.all() }) }
export function invalidateOwnCredentials() { return queryClient.invalidateQueries({ queryKey: queryKeys.credentials.mine() }) }
export function invalidateAccounts() { return queryClient.invalidateQueries({ queryKey: queryKeys.accounts.all() }) }
```

- [ ] **Step 4: Write `useSession.ts`.**

```ts
import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { fetchMe } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import type { components } from '@/api'

type Session = components['schemas']['SessionView']
type Permission = keyof components['schemas']['Permissions']

export function useSession() {
  const q = useQuery<Session>({
    queryKey: queryKeys.session.current(),
    queryFn: fetchMe,
    retry: false,
    staleTime: 30_000,
  })
  return {
    user: computed(() => q.data.value ?? null),
    isPending: q.isPending,
    isAdmin: computed(() => q.data.value?.role === 'admin'),
    can(perm: Permission): boolean {
      const u = q.data.value
      return u ? u.role === 'admin' || !!u.permissions[perm] : false
    },
  }
}
```

- [ ] **Step 5: type-check.**

```bash
pnpm --dir dashboard type-check
```

- [ ] **Step 6: Commit.**

```bash
git add dashboard/src/api/webauthn.ts dashboard/src/api/queryKeys.ts dashboard/src/api/client.ts dashboard/src/composables/useSession.ts
git commit -m "feat(dashboard): webauthn wrappers, query keys, useSession composable"
```

---

### Task 16: Dashboard router augmentation + layouts + route guard

**Goal:** Every route declares `meta.auth` + `meta.layout`. App.vue switches layout via `<component :is>`. Route guard uses `queryClient.ensureQueryData`.

**Files:**
- Modify: `dashboard/src/router/index.ts`
- Modify: `dashboard/src/App.vue`
- Create: `dashboard/src/layouts/AppLayout.vue`
- Create: `dashboard/src/layouts/MinimalLayout.vue`
- Create: `dashboard/src/router/guard.ts`

**Acceptance Criteria:**
- [ ] `RouteMeta` interface augmented with `auth` and `layout`.
- [ ] Every existing route declares `meta.auth` (mostly `{kind:'admin'}` since they're currently admin-only; api-keys/requests/etc. get permission-gated to match server).
- [ ] Unauthenticated visit to `/overview` redirects to `/login?next=/overview`.
- [ ] `/login` and `/enroll/:token` render minimal layout.

**Verify:** Browser visits `/overview` with no cookie → redirect to `/login`.

**Steps:**

- [ ] **Step 1: Augment RouteMeta.**

`dashboard/src/router/types.ts`:

```ts
import 'vue-router'

type Permission = 'view_own_usage' | 'manage_own_api_keys' | 'view_models' | 'view_own_traces'

export type RouteAuth =
  | { kind: 'public' }
  | { kind: 'session' }
  | { kind: 'admin' }
  | { kind: 'permission'; perm: Permission }

declare module 'vue-router' {
  interface RouteMeta {
    auth: RouteAuth
    layout: 'app' | 'minimal'
  }
}
```

- [ ] **Step 2: Update each existing route in `router/index.ts`** with `meta.auth` and `meta.layout: 'app'`. Map per Task 9 (admin or perm).

- [ ] **Step 3: Write `guard.ts` and wire it into the router.**

```ts
import { queryClient } from '@/api/queryClient'
import { queryKeys } from '@/api/queryKeys'
import { fetchMe } from '@/api/client'

export async function authGuard(to) {
  const auth = to.meta.auth
  if (auth.kind === 'public') return true
  await queryClient.ensureQueryData({
    queryKey: queryKeys.session.current(),
    queryFn: fetchMe,
  }).catch(() => null)
  const me = queryClient.getQueryData(queryKeys.session.current())
  if (!me) return { path: '/login', query: { next: to.fullPath }}
  if (auth.kind === 'admin' && me.role !== 'admin') return fallbackFor(me)
  if (auth.kind === 'permission') {
    const ok = me.role === 'admin' || me.permissions[auth.perm]
    if (!ok) return fallbackFor(me)
  }
  return true
}
function fallbackFor(me) {
  if (me.role === 'admin') return '/overview'
  if (me.permissions.view_own_usage) return '/overview'
  if (me.permissions.manage_own_api_keys) return '/api-keys'
  return '/me'
}
```

`router/index.ts`: `router.beforeEach(authGuard)`.

- [ ] **Step 4: Write layouts.**

`AppLayout.vue` wraps existing `<AppSidebar />` + main slot. `MinimalLayout.vue` is a centered card.

- [ ] **Step 5: Update `App.vue`.**

```vue
<script setup>
import { useRoute } from 'vue-router'
import { useSession } from '@/composables/useSession'
import AppLayout from '@/layouts/AppLayout.vue'
import MinimalLayout from '@/layouts/MinimalLayout.vue'

const route = useRoute()
const session = useSession()
const layouts = { app: AppLayout, minimal: MinimalLayout }
</script>
<template>
  <SplashScreen v-if="session.isPending.value" />
  <component v-else :is="layouts[route.meta.layout ?? 'app']">
    <RouterView />
  </component>
</template>
```

- [ ] **Step 6: type-check + build.**

```bash
pnpm --dir dashboard type-check && pnpm --dir dashboard build
```

- [ ] **Step 7: Commit.**

```bash
git add dashboard/src/router/ dashboard/src/layouts/ dashboard/src/App.vue
git commit -m "feat(dashboard): route meta auth + layout, global guard"
```

---

### Task 17: Dashboard — LoginView

**Goal:** Renders bootstrap instruction when `/auth/status.bootstrapped=false`; sign-in button otherwise.

**Files:**
- Create: `dashboard/src/views/LoginView.vue`
- Modify: `dashboard/src/router/index.ts` (add `/login`, `meta.auth: { kind:'public' }`, `meta.layout: 'minimal'`)
- Add fetcher: `fetchAuthStatus`, `beginLogin`, `completeLogin` in `client.ts` (if not already done in Task 15)

**Acceptance Criteria:**
- [ ] Hitting `/login` on a fresh DB shows "Run `picotera enroll-admin`".
- [ ] After bootstrap, hitting `/login` shows "Sign in with passkey" button.
- [ ] Clicking the button runs the ceremony; on success navigates to `route.query.next || '/overview'`.

**Verify:** Full flow manually.

**Steps:** (template + script setup with `useMutation`, calls `webauthnGet`, sets `queryClient.setQueryData(queryKeys.session.current(), data)` on success). Use the `Button` and `Field` primitives from `src/ui/`.

Commit: `feat(dashboard): LoginView (bootstrap branch + sign-in)`.

---

### Task 18: Dashboard — EnrollView

**Goal:** Branches on enrollment intent and drives the registration ceremony.

**Files:**
- Create: `dashboard/src/views/EnrollView.vue`
- Modify: `dashboard/src/router/index.ts` (add `/enroll/:token`)

**Acceptance Criteria:**
- [ ] Bootstrap intent: shows username/display_name form with `pattern="[a-z0-9_-]{2,32}"` HTML validation.
- [ ] Invite intent: shows target username read-only + nickname.
- [ ] Reset intent: warning + confirm checkbox + nickname.
- [ ] Successful ceremony lands on `/overview`.
- [ ] Expired/consumed token shows friendly error.

**Verify:** End-to-end with `picotera enroll-admin` URL.

Commit: `feat(dashboard): EnrollView for bootstrap/invite/reset`.

---

### Task 19: Dashboard — MeView

**Goal:** Three DataCards (identity, permissions, passkeys), add-passkey ceremony, last-passkey protection, sign-out.

**Files:**
- Create: `dashboard/src/views/MeView.vue`
- Modify: `dashboard/src/router/index.ts` (add `/me`, `meta.auth: { kind:'session' }`)
- Modify: `dashboard/src/components/AppSidebar.vue` (bottom Profile/Sign out menu)

**Acceptance Criteria:**
- [ ] Identity card shows username, display_name, role.
- [ ] Permissions card shows checklist (read-only for self).
- [ ] Passkeys table shows nickname + transports + backup-state badge ("Synced" / "Device-bound") + last_used_at.
- [ ] Delete button disabled when count == 1 with tooltip.
- [ ] "Add another passkey" runs ceremony.
- [ ] Sign-out clears `queryClient.removeQueries({ queryKey: queryKeys.session.all() })` and navigates to `/login`.

Commit: `feat(dashboard): MeView`.

---

### Task 20: Dashboard — AppSidebar permission filtering

**Goal:** Apply `v-if="session.can(perm) || session.isAdmin"` per nav item.

**Files:**
- Modify: `dashboard/src/components/AppSidebar.vue`

**Acceptance Criteria:**
- [ ] Admin sees the existing full sidebar.
- [ ] Account icon at the bottom opens Profile / Sign out menu.

Commit: `feat(dashboard): permission-aware sidebar`.

---

### Phase 1 Verify-and-Ship checkpoint

After Tasks 1–20:

```bash
# 1. Backend builds + tests
go build ./... && go test ./...

# 2. Frontend builds + type-checks
pnpm --dir dashboard build && pnpm --dir dashboard type-check

# 3. End-to-end smoke
podman-compose up -d
MISE_DISABLE_TOOLS=pnpm mise run server &
sleep 3
./picotera enroll-admin     # in another shell, with built binary
# Open URL → register passkey → land on /overview
# Sign out → land on /login
# Click "Sign in" → ceremony → land on /overview
```

If all green, Phase 1 is shippable. The README's CAUTION can be lifted (or replaced with new auth instructions).

---

## Phase 2 — Admin account management

### Task 21: sqlc — account list/update/delete + last-admin guard

**Goal:** Add queries that handle the account CRUD with the last-admin invariant.

**Files:**
- Modify: `db/queries/account.sql`

**Acceptance Criteria:**
- [ ] `ListAccounts` returns rows with a `last_sign_in_at` subquery column.
- [ ] `UpdateAccount` covers display_name + role + 4 permission booleans + disabled.
- [ ] `CountActiveAdmins` with `FOR UPDATE` exists.
- [ ] `DeleteAccountByID :exec` exists.

Add the queries:

```sql
-- name: ListAccounts :many
SELECT
  a.*,
  (SELECT MAX(c.last_used_at) FROM webauthn_credential c WHERE c.account_id = a.id) AS last_sign_in_at
FROM account a
ORDER BY a.created_at ASC, a.id ASC;

-- name: UpdateAccount :one
UPDATE account SET
  display_name = $2, role = $3,
  can_view_own_usage = $4, can_manage_own_api_keys = $5,
  can_view_models = $6, can_view_own_traces = $7,
  disabled = $8, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteAccountByID :exec
DELETE FROM account WHERE id = $1;

-- name: CountActiveAdminsForUpdate :one
SELECT COUNT(*) FROM account WHERE role = 'admin' AND NOT disabled FOR UPDATE;
```

Run `sqlc generate`.

Commit: `feat(auth): account CRUD queries + last-admin invariant`.

---

### Task 22: handle_account.go — admin CRUD + invitations + reissue + revoke-sessions

**Goal:** All `/accounts/*` and `/invitations` endpoints from `api.md`.

**Files:**
- Create: `pkg/server/handle_account.go`
- Modify: `pkg/server/server.go` (registrations)

**Acceptance Criteria:**
- [ ] `GET /accounts` returns `AccountView[]` with `lastSignInAt`.
- [ ] `PUT /accounts/{id}` enforces last-admin via `CountActiveAdminsForUpdate` inside TX.
- [ ] `POST /accounts/delete` enforces last-admin; cascades credentials/enrollments; sets api_key.account_id to NULL; revokes sessions after commit.
- [ ] `POST /accounts/revoke-sessions` returns `{revoked: count}`.
- [ ] `POST /accounts/reissue-enrollment` returns reveal-once URL.
- [ ] `POST /invitations` validates input, inserts account + enrollment in TX, returns reveal-once URL.
- [ ] All wired with `AuthAdmin`.

Wire registrations:

```go
registerOp(mgmt, contract.OperationListAccounts,  server.handleListAccounts,  contract.AuthRequirement{Kind: contract.AuthAdmin})
registerOp(mgmt, contract.OperationGetAccount,    server.handleGetAccount,    contract.AuthRequirement{Kind: contract.AuthAdmin})
registerOp(mgmt, contract.OperationUpdateAccount, server.handleUpdateAccount, contract.AuthRequirement{Kind: contract.AuthAdmin})
registerOp(mgmt, contract.OperationDeleteAccount, server.handleDeleteAccount, contract.AuthRequirement{Kind: contract.AuthAdmin})
registerOp(mgmt, contract.OperationDeleteAccountCredential, server.handleDeleteAccountCredential, contract.AuthRequirement{Kind: contract.AuthAdmin})
registerOp(mgmt, contract.OperationRevokeAccountSessions,   server.handleRevokeAccountSessions,   contract.AuthRequirement{Kind: contract.AuthAdmin})
registerOp(mgmt, contract.OperationReissueEnrollment,       server.handleReissueEnrollment,       contract.AuthRequirement{Kind: contract.AuthAdmin})
registerOp(mgmt, contract.OperationCreateInvitation,        server.handleCreateInvitation,        contract.AuthRequirement{Kind: contract.AuthAdmin})
```

Implement each — UpdateAccount and DeleteAccount run inside a transaction with the count guard:

```go
func (s *Server) handleUpdateAccount(ctx context.Context, in *updateAccountIn) (*accountOut, error) {
    if in.Body.Username != "" { return nil, auth.ErrUsernameImmutable() }
    if err := auth.ValidateDisplayName(in.Body.DisplayName); err != nil { return nil, err }

    tx, _ := s.dbPool.Begin(ctx); defer tx.Rollback(ctx)
    q := s.queries.WithTx(tx)

    current, err := q.GetAccountByID(ctx, in.ID)
    if err != nil { return nil, auth.ErrAccountNotFound() }

    demoting := current.Role == "admin" && in.Body.Role != "admin"
    disabling := !current.Disabled && in.Body.Disabled
    if demoting || disabling {
        n, _ := q.CountActiveAdminsForUpdate(ctx)
        // would-be count excluding self if we're disabling/demoting an admin
        if current.Role == "admin" && !current.Disabled && n <= 1 {
            return nil, auth.ErrLastAdmin()
        }
    }
    updated, err := q.UpdateAccount(ctx, db.UpdateAccountParams{ /* fields from in.Body */ })
    if err != nil { return nil, err }
    if err := tx.Commit(ctx); err != nil { return nil, err }

    if disabling {
        _, _ = s.sessionStore.RevokeAllForAccount(ctx, updated.ID)
    }
    return &accountOut{Body: accountView(&updated)}, nil
}
```

Commit: `feat(auth): admin account CRUD, invitations, reissue, revoke-sessions`.

---

### Task 23: Dashboard — AccountsView + AccountForm

**Goal:** Admin UI for listing accounts, inviting, editing, reissuing, revoking, deleting.

**Files:**
- Create: `dashboard/src/views/AccountsView.vue`
- Create: `dashboard/src/components/AccountForm.vue`
- Modify: `dashboard/src/router/index.ts` (add `/accounts`, `meta.auth: {kind:'admin'}`)
- Modify: `dashboard/src/components/AppSidebar.vue` (add Accounts admin entry)

**Acceptance Criteria:**
- [ ] AccountsView lists accounts with `DataTable`; columns username, display_name, role, permissions summary, disabled, last sign-in.
- [ ] Row actions menu: Edit, Reissue enrollment, Revoke sessions, Disable, Delete (each with `useConfirm` confirm dialog).
- [ ] "Invite user" button opens `AccountForm` in invite mode.
- [ ] `AccountForm` invite-mode success shows reveal-once URL view with copy button + "Done".
- [ ] `AccountForm` edit-mode forbids username change (input shown read-only).
- [ ] Reissue success shows the same reveal-once UX.

Commit: `feat(dashboard): AccountsView + AccountForm with reveal-once URLs`.

---

### Phase 2 Verify-and-Ship checkpoint

- Admin can invite a `user`-role account → URL works → recipient registers → they can hit `/me` and see their permissions (no other dashboard yet works for them — that's Phase 3).
- Admin can edit own permissions but cannot disable / demote themselves if they're the only admin.

---

## Phase 3 — Scoped views

### Task 24: Scoped repository queries

**Goal:** Add the queries that let handlers serve admin-all vs user-scoped data.

**Files:**
- Modify: `db/queries/api_key.sql`
- Modify: `db/queries/request.sql`

**Acceptance Criteria:**

Add to `api_key.sql`:

```sql
-- name: ListApiKeysByAccount :many
SELECT * FROM api_key WHERE account_id = $1 ORDER BY id DESC;

-- name: GetApiKeyOwnedBy :one
SELECT * FROM api_key WHERE id = $1 AND account_id = $2;
```

Add to `request.sql`:

```sql
-- name: ListRequestsByAccount :many
SELECT r.* FROM request r
  JOIN api_key k ON k.id = r.api_key_id
  WHERE k.account_id = $1
  ORDER BY r.created_at DESC, r.id DESC
  LIMIT $2;
```

Run `sqlc generate`.

Commit: `feat(auth): scoped repository queries for non-admin views`.

---

### Task 25: Handler scoping + api_key auto-stamping

**Goal:** Each scoped handler branches on session role.

**Files:**
- Modify: `pkg/server/handle_api_key.go` (or wherever ApiKey handlers live; search for `s.handleListApiKeys`)
- Modify: `pkg/server/handle_request.go` (or wherever)

**Acceptance Criteria:**
- [ ] `handleListApiKeys`: if admin → ListApiKeysAll; else → ListApiKeysByAccount(session.Account.ID).
- [ ] `handleCreateApiKey`: non-admin caller's `account_id` is forced to their own ID; admin can set or leave NULL.
- [ ] `handleGetApiKey`/`UpdateApiKey`/`DeleteApiKey`: non-admin uses `GetApiKeyOwnedBy(id, account_id)`, returns 404 on mismatch.
- [ ] `handleListRequests`: similar split.

Pattern:

```go
sess := auth.SessionFromContext(ctx)
if sess.Account.Role == "admin" {
    rows, err = s.queries.ListApiKeysAll(ctx)
} else {
    rows, err = s.queries.ListApiKeysByAccount(ctx, sess.Account.ID)
}
```

Commit: `feat(auth): handler-level scoping for api-keys and requests`.

---

### Task 26: Dashboard — scoped variants in existing views

**Goal:** Reuse existing views; just rely on server-side scoping. Sidebar entries already permission-gated from Task 20.

**Files:**
- Modify: `dashboard/src/views/RequestsView.vue` (filter-by-API-key dropdown auto-scopes via the same `/api-keys` listing)
- Modify: `dashboard/src/views/OverviewView.vue` (no UI change; server returns scoped metrics)

**Acceptance Criteria:**
- [ ] A non-admin user lands on `/overview` after sign-in (if they have `view_own_usage`); sees only their own metrics.
- [ ] `/api-keys` lists only their own keys; new-key form auto-stamps `account_id`.
- [ ] `/requests` shows only their requests.

Commit: `feat(dashboard): rely on server scoping for non-admin views`.

---

### Phase 3 Verify-and-Ship checkpoint

- Create a `user`-role account via Phase 2 invitation flow with `view_own_usage` + `manage_own_api_keys` permissions.
- Sign in as that user.
- Verify: Overview shows zero (no traffic yet). Create an API key. Curl the gateway with it. Verify the request shows up in their Requests view.
- Sign in as admin in a separate browser. Verify admin can see the same request alongside everyone else's.

---

## Self-Review Summary (writer's pass)

**Spec coverage:** Every section of `design.md` and `api.md` maps to at least one task:
- §1 Data model → Task 1
- §2 Code organization → Tasks 3–8
- §3 Auth flows → Tasks 10–13 (Phase 1), 22 (Phase 2)
- §4 Session model → Tasks 5, 8
- §5 WebAuthn config → Task 6
- §6 Concurrency / TX / last-admin → Tasks 11, 22
- §7 Dashboard surface → Tasks 15–20, 23
- §8 Error model → Task 4 (errors.go)
- §10 Phase enforcement → Tasks 9, 22
- §13 New config keys → Task 2

**Placeholder scan:** No "TBD" / "implement later" left in. The Phase 1 tasks have full code; Phase 2/3 tasks include the queries + handler patterns explicitly. Some Vue templates (Tasks 17, 18, 19, 23) are described rather than rendered byte-for-byte — those tasks are bigger and the executor will compose templates from the `src/ui/` primitives following the existing patterns. If a future implementer wants more code in those tasks, expand them in-line.

**Type consistency:** `Permission` constants, `AuthRequirement` shape, `SessionView`/`AccountView`/`CredentialView`, KV key formats, cookie names — all consistent across tasks.

**Scope:** This plan covers one feature (auth + accounts) across three phases. Single implementation plan is appropriate; each phase has a clear "ship boundary" with verification steps.

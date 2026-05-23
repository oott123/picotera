# Design — Auth & Account System

## 1. Data model

Single migration `027_auth_system.sql`. All Phase 1/2/3 schema ships here.

```sql
-- +goose Up

-- account: anyone who can sign in to the dashboard
CREATE TABLE account (
  id                       SERIAL PRIMARY KEY,
  username                 TEXT NOT NULL UNIQUE,
  display_name             TEXT NOT NULL,
  webauthn_user_handle     BYTEA NOT NULL UNIQUE,            -- 64 random bytes, generated at account creation
  role                     TEXT NOT NULL CHECK (role IN ('admin','user')),
  can_view_own_usage       BOOLEAN NOT NULL DEFAULT FALSE,
  can_manage_own_api_keys  BOOLEAN NOT NULL DEFAULT FALSE,
  can_view_models          BOOLEAN NOT NULL DEFAULT FALSE,
  can_view_own_traces      BOOLEAN NOT NULL DEFAULT FALSE,
  disabled                 BOOLEAN NOT NULL DEFAULT FALSE,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- webauthn_credential: one row per registered passkey; multi-passkey per account
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

-- enrollment: single-use tokens for bootstrap / invite / reset flows
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

### Field rules

- **`username`** — must match `^[a-z0-9_-]{2,32}$`. Server rejects strictly per CLAUDE.md (no normalization, no whitespace trimming, no case-folding). Immutable after creation; `PUT /accounts/{id}` rejects `username` in the body.
- **`display_name`** — 1–128 characters, no control characters (`\x00-\x1f` or `\x7f`).
- **`webauthn_user_handle`** — 64 random bytes from `crypto/rand`. Generated server-side at account creation. Used as the WebAuthn `user.id` in registration/login ceremonies. Never returned in API responses.
- **`role`** — `admin` or `user`. `CHECK` enforced. Cannot be changed via `PUT /accounts/{id}` if the change would leave zero non-disabled admins.
- **`can_*` columns** — toggleable for `role='user'`; ignored for `role='admin'` (admins are implicitly permitted everything in code).
- **`disabled`** — when `true`, the account cannot start new sessions, and the session middleware revokes existing sessions on the next request (live lookup).
- **`token`** — plaintext, URL-safe base64 of 32 random bytes (`base64.RawURLEncoding.EncodeToString`). 43 characters. The same format is used for session tokens. Plaintext storage is consistent with `api_key.key` and matches the project's threat model (only admin accesses the DB).
- **`expires_at`** — 24h after `created_at` for all intents.

### Why `account` and not `user`

`user` is a SQL reserved word; quoting it everywhere is friction. `account` reads correctly for the domain ("the account that authenticates", which can be admin or user role) and aligns with the `pkg/auth/` package boundary.

## 2. Code organization

```
pkg/auth/
  account.go        CRUD helpers, permission methods on db.Account
  session.go        Issue / load / revoke. KV layout. Refresh policy.
  webauthn.go       webauthn.Config; Begin/Finish for registration + login
  enrollment.go     Token generation, consume helpers
  middleware.go     LoadSession (chi) + per-operation enforcement
  errors.go         Typed error codes mapped to HTTP

pkg/contract/
  auth.go           Permission type + constants; AuthRequirement struct;
                    Operation contracts for sessions, enrollments, accounts, /me

pkg/server/
  operations.go     registerOp[I,O] helper that wraps huma.Register with auth
  handle_auth.go    /auth/login/* and /auth/logout handlers
  handle_enrollment.go
  handle_me.go      /me, /me/credentials/*
  handle_account.go /accounts, /invitations
  middleware_auth.go  glue between huma operation metadata and pkg/auth middleware

cmd/picotera/main.go              add `enroll-admin` cobra subcommand

dashboard/src/
  views/LoginView.vue, EnrollView.vue, MeView.vue, AccountsView.vue
  components/AccountForm.vue       SidePanel form for invite + edit
  composables/useSession.ts
  api/webauthn.ts                  navigator.credentials wrappers + base64url helpers
  router/index.ts                  meta.auth + meta.layout on every route
```

### Why one `pkg/auth/` package and not split

`account`, `webauthn_credential`, `enrollment`, and `session` are tightly coupled — they all flow through the same ceremonies. Splitting into `pkg/user/` + `pkg/auth/` would just produce two packages that import each other.

### The `registerOp` helper

Auth enforcement is declared at every route's call site, not via hidden middleware tags:

```go
// pkg/server/operations.go
func registerOp[I, O any](
    api huma.API,
    op huma.Operation,
    handler func(context.Context, *I) (*O, error),
    auth contract.AuthRequirement,
)
```

`registerOperations()` becomes a wall of these calls but every entry carries its auth requirement on one visible line. Sample:

```go
registerOp(mgmt, contract.OperationListProviders, s.handleListProviders, contract.AuthAdmin)
registerOp(mgmt, contract.OperationListApiKeys,   s.handleListApiKeys,   contract.RequirePermission(contract.PermManageOwnApiKeys))
registerOp(mgmt, contract.OperationGetMe,         s.handleGetMe,         contract.AuthSession)
registerOp(mgmt, contract.OperationLoginBegin,    s.handleLoginBegin,    contract.AuthPublic)
```

The helper installs a per-operation middleware that reads `*pkg/auth.Session` off the request context (placed there by the global `LoadSession` middleware) and checks the requirement before invoking the handler. On failure, returns a typed error.

`contract.AuthRequirement` is a struct with a `Kind` discriminator and an optional `Permission` field, exported into the OpenAPI as an operation extension so the dashboard's `openapi-fetch` types stay in sync.

## 3. Auth flows

### Bootstrap (first admin)

1. Operator runs `picotera enroll-admin` on the host.
   - The CLI shares startup boilerplate with the server: `configx.Parse()` → connect DB → run goose migrations → execute its subcommand.
   - Errors if any active admin already exists (use `--new` to add an additional admin or `--reset --username X` to recover).
   - Inserts `enrollment(intent='bootstrap', target=NULL, token, expires=now+24h)`.
   - Prints `https://<PICOTERA_PUBLIC_ORIGIN>/enroll/<token>`.
2. Operator opens URL. SPA route `/enroll/:token` calls `GET /enrollments/:token`. Server returns `{intent: 'bootstrap'}`.
3. SPA renders the bootstrap form: username + display_name + optional passkey nickname.
4. SPA calls `POST /enrollments/:token/register/begin` → server validates token (unconsumed, unexpired) and the proposed username (regex), generates `PublicKeyCredentialCreationOptions` with `user.id = newly-generated 64-byte handle`, stashes `SessionData` in KV under `webauthn_ceremony:enroll:<token>` (5-min TTL), returns options.
5. Browser runs `navigator.credentials.create(options)` → attestation.
6. SPA calls `POST /enrollments/:token/register/complete` with attestation + final username/display_name.
7. Server, in one DB transaction: INSERT `account(role='admin', all can_* set TRUE, webauthn_user_handle = handle from step 4, disabled=false)`; INSERT `webauthn_credential`; UPDATE `enrollment.consumed_at = now()`. Then: issues session (KV write + Set-Cookie).
8. SPA navigates to `/overview`.

### CLI subcommand semantics

```
picotera enroll-admin                                  # error if any admin exists
picotera enroll-admin --new                            # always create a new admin (errors if username conflict)
picotera enroll-admin --reset --username NAME          # revoke NAME's credentials and active sessions; issue new enrollment
```

`--reset` only targets admin accounts — if the named account is `role='user'`, the CLI exits with an error directing the operator to use the dashboard's reissue-enrollment flow instead. The CLI is exclusively an admin-recovery tool; non-admin recovery is admin's job via the UI.

Username for the default and `--new` modes is collected at enrollment time (in the browser form); the CLI only issues the URL.

### Invitation (any role, by admin)

1. Admin opens `AccountForm` in `AccountsView`. Fills role and permission checkboxes (disabled when role=admin). No username or displayName at this stage.
2. SPA calls `POST /invitations` with `{ role, permissions }`.
3. Server inserts `enrollment(intent='invite', template_role, template_can_*, token, expires=now+24h)`. No `account` row is created yet — the account is deferred to consume time. Returns `{ url: 'https://.../enroll/<token>', expiresAt }`.
4. The `AccountForm` swaps to the URL-display view. The URL also appears in `GET /invitations` (listInvitations) so the admin can retrieve it again without reissuing.
5. Recipient opens URL → SPA `/enroll/:token` → `GET /enrollments/:token` returns `{intent: 'invite'}` (no target, since the account doesn't exist yet).
6. Form prompts the invitee for username, displayName, and optional passkey nickname.
7. SPA calls `POST /enrollments/:token/register/begin` with `{ username, displayName }`. Server validates username uniqueness and stashes ceremony data.
8. Browser registers passkey. SPA calls `POST /enrollments/:token/register/complete`. Server, in one DB transaction: validates username again; INSERT `account` (from stash.Invite.Username/DisplayName + template role/permissions); INSERT `webauthn_credential`; UPDATE `enrollment.consumed_at`. The atomic consume guarantees the URL cannot be used twice. Issues session.

### Reset (admin re-issues enrollment for an existing account)

1. Admin clicks "Reissue enrollment" on a row in `AccountsView` (or inside `AccountForm`).
2. SPA calls `POST /accounts/reissue-enrollment` with `{id}`.
3. Server creates `enrollment(intent='reset', target=id, token)`. Returns URL. Same reveal-once UX as invite.
4. Recipient opens URL → form shows warning "All existing passkeys for `<username>` will be revoked when you continue".
5. On `/complete`, in one DB transaction: DELETE all `webauthn_credential` WHERE `account_id = target`; INSERT new credential; UPDATE enrollment.consumed_at. After TX commits: revoke all sessions for the target by scanning `session:<account_id>:*` and deleting (best-effort; see §6).
6. Server issues a fresh session for the target user. Redirects to `/overview`.

### Login (discoverable credentials)

1. SPA on `/login` queries `GET /auth/status`. If `{ bootstrapped: false }`, the page shows "Run `picotera enroll-admin` on the host to create the first admin account" and hides the sign-in button.
2. Otherwise, user clicks "Sign in with passkey".
3. SPA calls `POST /auth/login/begin`. Server returns `PublicKeyCredentialRequestOptions` (no `allowCredentials` → discoverable). Stashes `SessionData` in KV under `webauthn_ceremony:login:<random>`. Sets `picotera_ceremony` cookie carrying that random (HttpOnly, Secure-when-applicable, SameSite=Strict, Path=`/api/picotera/auth`, Max-Age=300).
4. Browser runs `navigator.credentials.get(options)` → assertion with `response.userHandle = webauthn_user_handle`.
5. SPA calls `POST /auth/login/complete` with the assertion. Browser auto-sends `picotera_ceremony` cookie.
6. Server: load `SessionData` from KV; look up credential by `credential_id`; verify assertion via `go-webauthn`; load `account` by `webauthn_user_handle`; reject with `403 account_disabled` if `disabled`; UPDATE `webauthn_credential.sign_count`/`last_used_at`; issue session.
7. SPA navigates to `route.query.next || '/overview'`.

### Logout

`POST /auth/logout` is idempotent: always responds 204 with `Set-Cookie: picotera_session=; Max-Age=0; Path=/api/picotera`. **The `Path` must match the original Set-Cookie path** or browsers create a new empty cookie instead of clearing. If the request had a valid session, the KV entry is `DEL`-ed.

Client-side `signOut()` orchestrator also calls `queryClient.removeQueries({ queryKey: queryKeys.session.all() })` to drop cached session state before `router.push('/login')`.

## 4. Session model

### KV layout

```
session:<account_id>:<random>         JSON{ account_id, issued_at, expires_at, last_seen_ip }
webauthn_ceremony:enroll:<token>      webauthn.SessionData JSON (5-min TTL)
webauthn_ceremony:login:<random>      webauthn.SessionData JSON (5-min TTL)
webauthn_ceremony:add:<session_token> webauthn.SessionData JSON (5-min TTL, for "add another passkey")
```

The "add another passkey" ceremony is keyed by the caller's existing session token — no separate ceremony cookie is needed because the caller is already authenticated. `picotera_ceremony` cookie exists only for the login flow.

The session value does **not** snapshot role or permissions. The session middleware does a fresh `SELECT id, role, can_*, disabled FROM account WHERE id = $1` on every authenticated request. This costs one cheap pgx call per request and makes account-state changes propagate to active sessions immediately — no JWT-style revocation list, no snapshot drift.

The `<account_id>` in the session key prefix is what lets `POST /accounts/revoke-sessions` enumerate sessions for a single account via `session:<account_id>:*` glob (the existing `pkg/kv` API supports this — see `kv.Store.Scan`).

### Refresh policy

Default `SESSION_TTL = 24h`, configurable via `PICOTERA_SESSION_TTL=24h` (Viper duration string). A request that finds `expires_at - now() < 0.25 * SESSION_TTL` re-writes the KV entry with a fresh `expires_at` (sliding window, low write amplification). Other requests don't touch KV.

### Cookies

```
picotera_session    HttpOnly  Secure*  SameSite=Lax     Path=/api/picotera        Max-Age=session-ttl-secs
picotera_ceremony   HttpOnly  Secure*  SameSite=Strict  Path=/api/picotera/auth   Max-Age=300
```

`Secure*` is derived from request scheme:

```go
secure := r.TLS != nil
if cfg.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
    secure = true
}
```

Two new config keys:
- `PICOTERA_PUBLIC_ORIGIN` — comma-separated list of origins the dashboard is served from (e.g. `https://picotera.example.com,http://localhost:9898`). First entry is used to compose enrollment URLs. All entries are passed to `webauthn.Config.RPOrigins`.
- `PICOTERA_TRUST_PROXY` — boolean, default `false`. When true, `X-Forwarded-Proto: https` upgrades cookie Secure.

Localhost over HTTP works regardless because Chrome / Firefox treat `localhost` as a secure context and accept Secure cookies; `r.TLS == nil` simply yields `Secure=false`, which browsers also accept on localhost.

### CSRF

Same-origin SPA + JSON-only mutations + `SameSite=Lax` on the session cookie. Lax sends cookies on top-level navigations but blocks them on cross-site sub-resource POSTs and form-encoded POSTs. Cross-origin `fetch(..., {credentials:'include'})` triggers a CORS preflight that picotera refuses (no `Access-Control-Allow-Origin: <attacker>`). Together these close the classic CSRF vectors without a token.

This is documented in code comments next to the cookie helpers so future contributors don't re-add CSRF tokens "just in case." If we ever expose mutating GETs or move the dashboard to a separate origin, revisit.

## 5. WebAuthn configuration

`pkg/auth/webauthn.go` constructs one `*webauthn.WebAuthn` at startup from the config:

```go
&webauthn.Config{
    RPID:          cfg.WebAuthnRPID,                     // PICOTERA_WEBAUTHN_RP_ID
    RPDisplayName: "PicoTera",
    RPOrigins:     cfg.PublicOrigins,                    // []string parsed from PICOTERA_PUBLIC_ORIGIN
    AttestationPreference: protocol.PreferNoAttestation,
    Timeouts: webauthn.TimeoutsConfig{
        Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: 60*time.Second},
        Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: 120*time.Second},
    },
}
```

`UserVerification` differs per ceremony:

- **Registration** (`BeginRegistration` options): `UserVerification: protocol.VerificationRequired`. The credential is flagged as UV-capable forever after.
- **Login** (`BeginLogin` options): `UserVerification: protocol.VerificationPreferred`. Smoother UX without losing the UV guarantee the credential already encodes.

`AuthenticatorSelection.ResidentKey: protocol.ResidentKeyRequirementRequired` is set on registration so all credentials are discoverable — the discoverable-credentials login flow depends on this.

Startup logs `auth: RP ID = <id>, origins = [<a>, <b>]` so misconfigurations are visible. A wrong RP ID surfaces as an opaque browser DOM error otherwise.

## 6. Concurrency, transactions, races

### DB transactions

| Flow | Inside one TX |
|---|---|
| Bootstrap consume | INSERT account; INSERT credential; UPDATE enrollment.consumed_at |
| Invite create | INSERT enrollment (no account yet) |
| Invite consume | INSERT account (from stash.Invite username/displayName + template); INSERT credential; UPDATE enrollment.consumed_at |
| Reset consume | DELETE credentials WHERE account_id; INSERT credential; UPDATE enrollment.consumed_at |
| Account delete | DELETE account (CASCADE handles webauthn_credential + enrollment; SET NULL on api_key.account_id) |
| Role/disable update with last-admin check | `SELECT FOR UPDATE` count of active admins inside the TX before applying |

### Sessions are outside the TX (best-effort)

After a `reset` consume TX commits, the server scans `session:<account_id>:*` in KV and deletes each entry to kick existing sessions for the target. If KV is unavailable, the deletion fails silently — old sessions remain live until their TTL, but they can't make changes that require credentials (none exist anymore after the consume). For `disable` and `delete`, the live-lookup design means existing sessions are rejected on their next request regardless of KV state.

### Last-admin invariant

Server-side, enforced inside the same TX as the mutation:

```sql
-- name: CountActiveAdmins :one
SELECT COUNT(*) FROM account WHERE role = 'admin' AND NOT disabled FOR UPDATE;
```

`PUT /accounts/{id}` and `POST /accounts/delete` and `PUT /accounts/{id}` (disable) all check this. If the operation would leave zero active admins, reject with `409 last_admin`. The dashboard UI also disables the relevant controls when the admin is acting on the only-admin (themselves), but the server is authoritative.

### Race notes

| Race | Outcome |
|---|---|
| Two tabs register the same credential | UNIQUE on `webauthn_credential.credential_id` rejects the second; UI shows "this passkey is already registered" |
| Admin deletes user mid-enrollment | `enrollment.target_account_id ON DELETE CASCADE` removes the enrollment row; consume returns `410 enrollment_consumed` |
| User disabled mid-session | Next request hits the live lookup → `403 account_disabled`; client signs out |
| Concurrent role-change attempts | TX with `FOR UPDATE` serializes; last writer wins, only one passes the last-admin check |

## 7. Dashboard surface

### Layouts

```ts
declare module 'vue-router' {
  interface RouteMeta {
    auth: RouteAuth
    layout: 'app' | 'minimal'
  }
}
```

`App.vue` selects layout via `<component :is="layouts[route.meta.layout]">` and waits for session resolution before first paint:

```vue
<SplashScreen v-if="!sessionReady" />
<component :is="layouts[route.meta.layout]" v-else>
  <RouterView />
</component>
```

`layouts.app` = the existing sidebar + main shell. `layouts.minimal` = centered card (used by `LoginView` and `EnrollView`).

### Session composable

```ts
// src/composables/useSession.ts
export function useSession() {
  const q = useQuery({
    queryKey: queryKeys.session.current(),
    queryFn: fetchMe,
    retry: false,
    staleTime: MANAGEMENT_STALE_TIME,
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

Single vue-query subscription, shared across the app via the query cache.

### Router guard

```ts
router.beforeEach(async to => {
  const auth = to.meta.auth
  if (auth.kind === 'public') return true

  await queryClient.ensureQueryData({
    queryKey: queryKeys.session.current(),
    queryFn: fetchMe,
  }).catch(() => null)
  const me = queryClient.getQueryData<Session>(queryKeys.session.current())

  if (!me) return { path: '/login', query: { next: to.fullPath }}
  if (auth.kind === 'admin' && me.role !== 'admin') return fallbackFor(me)
  if (auth.kind === 'permission' && !canFor(me, auth.perm)) return fallbackFor(me)
  return true
})

function fallbackFor(me: Session): string {
  if (me.role === 'admin') return '/overview'
  if (canFor(me, 'view_own_usage')) return '/overview'
  if (canFor(me, 'manage_own_api_keys')) return '/api-keys'
  return '/me'   // always accessible to any session
}
```

A toast surfaces "You don't have permission to view X" so the redirect isn't silent. No dedicated `/access-denied` page.

### View additions

| View | Path | Layout | Purpose |
|---|---|---|---|
| `LoginView` | `/login` | minimal | Calls `/auth/status` on mount; renders sign-in button or bootstrap instruction |
| `EnrollView` | `/enroll/:token` | minimal | Reads enrollment intent; renders bootstrap / invite / reset form |
| `MeView` | `/me` | app | Whoami; own passkeys table; sign-out |
| `AccountsView` | `/accounts` | app | Admin: list accounts; row actions; "Invite user" |
| `AccountForm` (component) | — | side-panel | Invite + edit; reveal-once URL display on invite/reissue success |

### Sidebar

Existing `AppSidebar.vue` order is preserved. Per-item `v-if="session.can(perm) || session.isAdmin"`. One new bottom-grouped item "Accounts" (admin only). Bottom of sidebar gets an avatar with menu: "Profile" (→ `/me`) and "Sign out" (→ `signOut()` orchestrator).

### WebAuthn browser wrappers

`src/api/webauthn.ts` provides:

```ts
async function webauthnCreate(opts: PublicKeyCredentialCreationOptionsJSON, signal?: AbortSignal): Promise<AttestationResponseJSON>
async function webauthnGet(opts: PublicKeyCredentialRequestOptionsJSON, signal?: AbortSignal): Promise<AssertionResponseJSON>
```

Handles base64url ↔ ArrayBuffer conversion in one place, plumbs `AbortController` through for cancellation on route change, and maps `NotAllowedError` ("user cancelled" / "timeout") to a typed `WebAuthnUserCancelled` error that views render as a friendly inline message.

### Backup-state UX (v1)

Two-state display on passkey rows:
- `backup_state = true` → green `Tag` labeled "Synced"
- otherwise → neutral `Tag` labeled "Device-bound"

(go-webauthn surfaces a third state — `backup_eligible=true, backup_state=false` — meaning the platform could sync but hasn't. Rare; collapsed to "Device-bound" for v1 simplicity. Three-state can land later without schema change since both columns are already stored.)

### Last-passkey protection on `MeView`

When the account has exactly one credential, the row's delete button is disabled with tooltip "You need at least one passkey." Server-side, `POST /me/credentials/delete` also returns `400 last_passkey` if it would remove the last one. UI is defense-in-depth, server is authoritative.

## 8. Error model

Typed error codes returned in `ApiRequestError.code`. Listed in `api.md` per endpoint; summary:

| Status | Code | Trigger |
|---|---|---|
| 401 | `no_session` | Missing/invalid/expired session cookie |
| 403 | `not_admin` | Caller is not admin |
| 403 | `permission_denied` | Caller lacks the required permission |
| 403 | `account_disabled` | Caller's account is disabled |
| 409 | `last_admin` | Operation would leave zero active admins |
| 409 | `username_taken` | Username already exists |
| 410 | `enrollment_expired` | Token past `expires_at` |
| 410 | `enrollment_consumed` | Token already used (or its account deleted) |
| 400 | `invalid_username` | Username doesn't match regex |
| 400 | `invalid_display_name` | Display name violates length/control-char rules |
| 400 | `last_passkey` | Would remove the only passkey on an account |
| 400 | `webauthn_ceremony_failed` | go-webauthn rejected attestation / assertion |
| 400 | `username_immutable` | `PUT /accounts/{id}` body contains `username` |
| 404 | `account_not_found` | Admin endpoint referenced an unknown account |
| 404 | `credential_not_found` | Credential id not owned by caller (for /me) or doesn't exist (admin) |
| 503 | `not_bootstrapped` | `/auth/status` indicates no admin exists yet |

## 9. Logging

`pkg/logx` info-level events:

```
auth.login_success            account_id, credential_id_suffix
auth.login_failure            reason
auth.logout                   account_id
auth.enrollment_consumed      intent, account_id
auth.account_invited          role, permissions
auth.account_updated          account_id, changes (map of field→{from,to})
auth.account_deleted          account_id
auth.sessions_revoked         account_id, count, reason
auth.credential_revoked_admin account_id, credential_id, actor_id
auth.enrollment_issued        intent, target_account_id, expires_at
auth.credential_added         account_id, credential_id_suffix, backup_state
auth.credential_revoked_self  account_id, credential_id
auth.credential_renamed_self  account_id, credential_id
auth.invitation_revoked       token, actor_id
auth.webauthn_error           details
```

Note: `auth.account_disabled` and `auth.account_role_changed` are NOT emitted as separate events; both are covered by `auth.account_updated` with a `changes` field listing the fields that changed.

Never log: full credential IDs, full session tokens, full enrollment tokens. The last four characters of `credential_id` are sufficient for forensic correlation.

## 10. Phase enforcement note

Server-side auth enforcement is **on from Phase 1**, even though only `admin` accounts exist in Phase 1. This means Phase 2 (invitations) and Phase 3 (scoped views) are purely additive — they don't flip a "now enforce" switch. Concretely:

- Phase 1 calls every `registerOp(...)` with its proper `AuthRequirement`. Non-admin code paths exist but are unreached because no non-admin accounts exist.
- Phase 2 adds the invitation UI and account-management dashboard. The first non-admin account starts hitting the existing enforcement code.
- Phase 3 adds repository-layer scoping (the `ListApiKeysByAccount` / `GetApiKeyOwnedBy` queries) and dashboard variants that consume them.

This avoids a Phase-2 behavior reversal where suddenly-existing non-admin sessions discover that enforcement wasn't actually wired.

## 11. Migration / upgrade notes

**This is a breaking change for operators.** After upgrading to a build that includes Phase 1:

1. The management API stops accepting unauthenticated requests. Every existing call from a script / tool / browser tab returns `401`.
2. The dashboard SPA redirects to `/login`, which (with no admin bootstrapped) tells the operator to run `picotera enroll-admin` on the host.
3. The operator runs the CLI, opens the printed URL, registers a passkey, and is logged in. The dashboard now works as before.
4. **Gateway routes (`/v1/messages`, `/v1/chat/completions`, etc.) are unaffected.** API key authentication for gateway traffic is unchanged.

There is no `auth=off` opt-out — per CLAUDE.md's "no unsolicited compatibility layers", new behavior replaces old behavior cleanly. Operators who specifically don't want auth can pin to a pre-Phase-1 build.

## 12. Deferred / known limitations

- **Rate limiting** on `/auth/login/complete` and `/enrollments/:token/register/complete` — token entropy and discoverable-credential semantics make brute force impractical at this scale.
- **CSP headers** (`frame-ancestors 'none'` etc.) on the static handler — defense-in-depth against clickjacking of the login UI.
- **Cross-tab session sync** via `BroadcastChannel` — signing out in one tab doesn't immediately log out the others; they reconcile on the next 401.
- **Re-authentication ("recent ceremony") for sensitive ops** like adding/deleting passkeys, promoting to admin — standard at scale; overkill for v1 personal use.
- **Formal audit log table** — info-level logrus events are sufficient unless a deployment needs SOC2-style traceability.
- **OIDC / external IdP** — explicitly out of scope.
- **Per-user API key count limits** — none in v1; can be added later as an annotation-driven policy.
- **Permission UI staleness in the sidebar** — vue-query's 30s `staleTime` covers this; sub-30s updates would need SSE.

## 13. New config keys

| Key | Default | Purpose |
|---|---|---|
| `PICOTERA_PUBLIC_ORIGIN` | `http://localhost:9898` | Comma-separated origins; first is used to build enrollment URLs, all passed to `webauthn.Config.RPOrigins` |
| `PICOTERA_WEBAUTHN_RP_ID` | derived from first `PUBLIC_ORIGIN` hostname | WebAuthn Relying Party ID |
| `PICOTERA_SESSION_TTL` | `24h` | Sliding session TTL (Viper duration) |
| `PICOTERA_TRUST_PROXY` | `false` | When true, honor `X-Forwarded-Proto: https` for cookie Secure derivation |

## 14. New dependencies

- Go: `github.com/go-webauthn/webauthn` (BSD-2-Clause). De-facto WebAuthn library for Go.
- Frontend: no new dependencies; `navigator.credentials` is a browser API.

Reproducible install: `go get github.com/go-webauthn/webauthn` lands in `go.mod`.

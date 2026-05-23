# API — Auth & Account System

All paths under `/api/picotera`. Resource namespaces use kebab-case (matching `api-keys` / `provider-endpoints`).

Auth column legend: **public** (no session) · **session** (any valid session) · **admin** (session + role=admin) · **perm:X** (session + permission X, admin auto-passes).

## Shared view types

```ts
type Permission =
  | 'view_own_usage'
  | 'manage_own_api_keys'
  | 'view_models'
  | 'view_own_traces'

type AccountView = {
  id: number
  username: string
  displayName: string
  role: 'admin' | 'user'
  permissions: Record<Permission, boolean>
  disabled: boolean
  createdAt: string                 // RFC3339
  updatedAt: string                 // RFC3339
  lastSignInAt: string | null       // derived: MAX(webauthn_credential.last_used_at)
}

type CredentialView = {
  id: number
  credentialIdSuffix: string        // last 4 chars of base64url(credential_id), for display only
  nickname: string | null
  transports: string[]
  backupState: boolean
  attestationType: string
  createdAt: string
  lastUsedAt: string | null
}

type EnrollmentPreview = {
  intent: 'bootstrap' | 'invite' | 'reset'
  target?: { username: string; displayName: string }      // omitted for bootstrap
  expiresAt: string
}

type SessionView = {        // GET /me response
  id: number
  username: string
  displayName: string
  role: 'admin' | 'user'
  permissions: Record<Permission, boolean>
}
```

WebAuthn ceremony payloads use the JSON-friendly shapes defined by `@simplewebauthn/types` (which `go-webauthn` emits): `PublicKeyCredentialCreationOptionsJSON`, `PublicKeyCredentialRequestOptionsJSON`, `RegistrationResponseJSON`, `AuthenticationResponseJSON`. Encoded with URL-safe base64 for binary fields. Not re-spelled here.

---

## Public

### `GET /auth/status` — `getAuthStatus`

- Auth: **public**.
- Response: `{ bootstrapped: boolean }`. `bootstrapped` is true iff at least one non-disabled admin exists. Used by `LoginView` to decide between "Sign in with passkey" and "Run `picotera enroll-admin`".
- Never returns 503; this is the status probe.

### `POST /auth/login/begin` — `beginLogin`

- Auth: **public**.
- Body: `{}` (empty).
- Response: `PublicKeyCredentialRequestOptionsJSON`.
- Side effects: stores `webauthn.SessionData` in KV under `webauthn_ceremony:login:<random>` with 5-min TTL; sets `picotera_ceremony` cookie carrying `<random>`.
- Errors: `503 not_bootstrapped` if no admin exists.

### `POST /auth/login/complete` — `completeLogin`

- Auth: **public**.
- Body: `AuthenticationResponseJSON`. Browser auto-sends `picotera_ceremony` cookie.
- Response: `SessionView` (the freshly authenticated account).
- Side effects: deletes the ceremony KV entry; clears `picotera_ceremony` cookie; issues session (KV write + `Set-Cookie: picotera_session=...`).
- Errors:
  - `400 webauthn_ceremony_failed` — assertion didn't verify, or no ceremony cookie present, or ceremony KV expired.
  - `403 account_disabled` — credential resolved to a disabled account.

### `POST /auth/logout` — `logout`

- Auth: **public** (idempotent).
- Body: none.
- Response: 204.
- Side effects: if session cookie present and valid, deletes the KV entry; always sets `Set-Cookie: picotera_session=; Max-Age=0; Path=/api/picotera`. Path must match the issue path or the cookie isn't cleared.

### `GET /enrollments/{token}` — `previewEnrollment`

- Auth: **public**.
- Path: `token` (string).
- Response: `EnrollmentPreview`.
- Errors:
  - `410 enrollment_expired` — past `expires_at`.
  - `410 enrollment_consumed` — `consumed_at` is set, or the target account was deleted.

### `POST /enrollments/{token}/register/begin` — `beginEnrollmentRegistration`

- Auth: **public**.
- Path: `token` (string).
- Body:
  - **bootstrap**: `{ username: string; displayName: string; nickname?: string }`. Server validates username uniqueness.
  - **invite**: `{ username: string; displayName: string; nickname?: string }`. Invitee picks their own username/displayName since no account exists yet.
  - **reset**: `{ nickname?: string }` (optional). Target account is fixed; username/displayName are already set.
- Response: `PublicKeyCredentialCreationOptionsJSON`.
- Side effects: stores `webauthn.SessionData` in KV under `webauthn_ceremony:enroll:<token>`. For bootstrap and invite intents, validates that the proposed `username` is unique and matches `^[a-z0-9_-]{2,32}$`; stashes username/displayName alongside ceremony data. For reset, reads target from `enrollment.target_account_id`. Generates `webauthn_user_handle` for the future account (bootstrap/invite) or reuses the existing handle (reset).
- Errors:
  - `410 enrollment_expired` / `410 enrollment_consumed`.
  - `400 invalid_username` / `400 invalid_display_name` — bootstrap and invite only.
  - `409 username_taken` — bootstrap and invite only.

### `POST /enrollments/{token}/register/complete` — `completeEnrollmentRegistration`

- Auth: **public**.
- Path: `token` (string).
- Body: `RegistrationResponseJSON` (the attestation object directly, not wrapped).
- Response: `{ session: SessionView; newCredentialId: number }`.
  - `session`: the newly issued session (same shape as `GET /me`).
  - `newCredentialId`: the database ID of the newly inserted `webauthn_credential` row.
- Side effects:
  - **bootstrap**: TX inserts `account(role='admin', all can_*=TRUE, username/displayName from stash)`, inserts `webauthn_credential`, marks enrollment consumed. Issues session.
  - **invite**: TX inserts `account` (username/displayName/role/permissions from stash + template), inserts `webauthn_credential`, marks enrollment consumed. Issues session. The atomic consume guarantees the URL can't be reused.
  - **reset**: TX deletes all `webauthn_credential` for target, inserts new `webauthn_credential`, marks enrollment consumed. After commit, scans `session:<target_account_id>:*` in KV and deletes (best-effort). Issues fresh session.
- Errors:
  - `400 webauthn_ceremony_failed` — attestation didn't verify.
  - `410 enrollment_expired` / `410 enrollment_consumed`.
  - `409 username_taken` — bootstrap and invite (re-checked at TX time).

---

## Session (current user)

### `GET /me` — `getMe`

- Auth: **session**.
- Response: `SessionView`.
- Errors: `401 no_session` and `403 account_disabled` are returned by the `LoadSession` middleware before this handler runs; documented here for completeness.

### `GET /me/credentials` — `listMyCredentials`

- Auth: **session**.
- Response: `CredentialView[]`, ordered by `created_at DESC`.

### `POST /me/credentials/register/begin` — `beginAddCredential`

- Auth: **session**.
- Body: `{}`.
- Response: `PublicKeyCredentialCreationOptionsJSON` with `excludeCredentials` populated from the caller's existing credentials (so the same authenticator can't double-register).
- Side effects: stores `webauthn.SessionData` in KV under `webauthn_ceremony:add:<session_token>` (keyed by the caller's existing session token; no new cookie). 5-min TTL.

### `POST /me/credentials/register/complete` — `completeAddCredential`

- Auth: **session**.
- Query param: `nickname` (optional string). Validated server-side (1–60 chars, no control characters). Trimmed before storage; empty after trim → NULL.
- Body: `RegistrationResponseJSON` (the attestation object directly, not wrapped).
- Response: `CredentialView`.
- Errors: `400 webauthn_ceremony_failed`, `400 invalid_nickname`.

### `POST /me/credentials/rename` — `renameMyCredential`

- Auth: **session**.
- Body: `{ id: number; nickname?: string | null }`.
- Response: 204.
- Errors:
  - `400 invalid_nickname` — nickname exceeds 60 chars or contains control characters.
  - `404 credential_not_found` — the id doesn't belong to the caller.

### `POST /me/credentials/delete` — `deleteMyCredential`

- Auth: **session**.
- Body: `{ id: number }`.
- Response: 204.
- Errors: `400 last_passkey` if the operation would leave zero credentials; `404 credential_not_found` if it's not owned by the caller.

---

## Admin — accounts

### `GET /accounts` — `listAccounts`

- Auth: **admin**.
- Response: `AccountView[]`, ordered by `created_at ASC, id ASC`. `lastSignInAt` is computed via subquery on `webauthn_credential.last_used_at`.

### `GET /accounts/{id}` — `getAccount`

- Auth: **admin**.
- Path: `id` (int).
- Response: `AccountView`.
- Errors: `404 account_not_found`.

### `PUT /accounts/{id}` — `updateAccount`

- Auth: **admin**.
- Path: `id` (int).
- Body:
  ```ts
  {
    displayName: string
    role: 'admin' | 'user'
    permissions: Record<Permission, boolean>
    disabled: boolean
  }
  ```
  `username` and `id` in the body are rejected with `400 invalid_username`-style code `400 username_immutable` if present.
- Response: `AccountView` (with updates applied).
- Side effects:
  - If `disabled` flipped to true OR the operation involves disabling/demoting an admin, the last-admin invariant is checked with `SELECT FOR UPDATE` inside the TX.
  - If `disabled` flipped to true, after commit the server scans `session:<id>:*` in KV and deletes entries (best-effort).
  - Role downgrade and permission changes do NOT auto-revoke sessions; the live lookup in middleware picks them up on the next request.
- Errors:
  - `400 invalid_display_name` / `400 username_immutable`.
  - `409 last_admin` — operation would leave zero active admins.

### `POST /accounts/delete` — `deleteAccount`

- Auth: **admin**.
- Body: `{ id: number }`.
- Response: 204.
- Side effects: `DELETE FROM account WHERE id = $1` cascades to `webauthn_credential` and `enrollment` (via FK), sets `api_key.account_id = NULL` on owned keys. After commit, scans `session:<id>:*` in KV and deletes.
- Errors:
  - `404 account_not_found`.
  - `409 last_admin` — the only active admin.

### `POST /accounts/credentials/delete` — `deleteAccountCredential`

- Auth: **admin**.
- Body: `{ accountId: number; credentialId: number }`.
- Response: 204.
- Side effects: deletes the credential. If it was the only credential on the account, the account remains but cannot sign in until a reissue.
- Errors: `404 credential_not_found`.

### `POST /accounts/revoke-sessions` — `revokeAccountSessions`

- Auth: **admin**.
- Body: `{ id: number }`.
- Response: `{ revoked: number }` (best-effort count of sessions deleted from KV).
- Errors: `404 account_not_found`.

### `POST /accounts/reissue-enrollment` — `reissueEnrollment`

- Auth: **admin**.
- Body: `{ id: number }`.
- Response: `{ url: string; expiresAt: string }`. **Reveal-once**: the URL is only present in this response; no endpoint retrieves it later. If admin loses the URL, they reissue.
- Side effects: inserts `enrollment(intent='reset', target=id, token=random, expires=now+24h)`. Does NOT yet revoke existing credentials/sessions — that happens at consume time.
- Errors: `404 account_not_found`.

### `POST /invitations` — `createInvitation`

- Auth: **admin**.
- Body:
  ```ts
  {
    role: 'admin' | 'user'
    permissions: Record<Permission, boolean>
  }
  ```
- Response: `{ url: string; expiresAt: string }`.
- Side effects: Inserts `enrollment(intent='invite', template_role, template_can_*, token, expires=now+24h)`. No `account` row is created yet — the account is created when the invitee consumes the URL (see `completeEnrollmentRegistration`). The URL is also queryable via `GET /invitations` after creation.
- Errors: none beyond auth.

### `GET /invitations` — `listInvitations`

- Auth: **admin**.
- Response: `InvitationView[]`, ordered by `created_at DESC`.
  ```ts
  type InvitationView = {
    token: string
    url: string
    role: 'admin' | 'user'
    permissions: Record<Permission, boolean>
    createdAt: string    // RFC3339
    expiresAt: string    // RFC3339
  }
  ```
- Only returns invitations that are unconsumed and unexpired (pending invitations). Already-consumed or expired rows are not listed.

### `POST /invitations/revoke` — `revokeInvitation`

- Auth: **admin**.
- Body: `{ token: string }`.
- Response: 204.
- Side effects: marks the enrollment row consumed (`consumed_at = now()`), preventing the invitee from registering. Idempotent in the sense that once marked consumed it stays consumed.
- Errors:
  - `404 invitation_not_found` — token doesn't exist or is not a pending invite (already consumed/expired).

---

## Existing operations — new auth requirements

Every existing `huma.Register(...)` call moves to `registerOp(...)` with an explicit `AuthRequirement`:

| Resource group | Auth requirement | Notes |
|---|---|---|
| `providers`, `provider-endpoints`, `endpoints`, `models` (writes) | `admin` | Provider/endpoint/model configuration is operator-only. |
| `models` (reads), `endpoints` (reads) | `perm:view_models` | Non-admin users get read-only listings. |
| `api-keys` (all operations) | `perm:manage_own_api_keys` | Admin sees all rows; user sees rows where `account_id = me`. New rows auto-stamp the caller's `account_id` for non-admin; admin's new rows leave `account_id` NULL (system-level keys). Admin does not currently have an API surface for provisioning a key on behalf of another user. |
| `requests` list/detail/spans | `perm:view_own_usage` | Scoped via `api_key.account_id`. Spans is part of the same page tree. |
| `traces` | `perm:view_own_traces` | Standalone page; separate concept (cross-request span grouping). |
| `overview` metrics | `perm:view_own_usage` | Per-account scoping for overview/distribution/series is not yet implemented because `request_overview_hourly` (Timescale continuous aggregate) lacks an `account_id` column. The handler explicitly returns 403 to non-admin callers despite their `view_own_usage` permission — those permissions remain for forward-compat with future scoped overview support. |
| `scripts`, `kv`, `rates`, `projects`, `mappings`, `simulate`, `fetch-models`, `match-pricing` | `admin` | Operator-only surfaces. |

### Repository-layer scoping queries (added in `db/queries/`)

Phase 1 already defines these so Phase 3 just wires them up:

```sql
-- name: ListApiKeysAll :many                     (admin path)
SELECT * FROM api_key ORDER BY id DESC;

-- name: ListApiKeysByAccount :many               (non-admin path)
SELECT * FROM api_key WHERE account_id = $1 ORDER BY id DESC;

-- name: GetApiKeyOwnedBy :one                    (non-admin GET/PUT/DELETE)
SELECT * FROM api_key WHERE id = $1 AND account_id = $2;

-- name: ListRequestsAll :many                    (admin)
SELECT * FROM request ORDER BY (created_at, id) DESC LIMIT $1;

-- name: ListRequestsByAccount :many              (non-admin via account → api_keys → requests)
SELECT r.* FROM request r
  JOIN api_key k ON k.id = r.api_key_id
  WHERE k.account_id = $1
  ORDER BY (r.created_at, r.id) DESC
  LIMIT $2;
```

Cursor pagination tuples remain `(created_at, id)` per existing convention (the `request` table is a TimescaleDB hypertable; see CLAUDE.md).

---

## OpenAPI security scheme

A single security scheme `picoteraSession` (cookie auth on `picotera_session`) is declared in the OpenAPI document. Every non-public operation references it. Public operations omit the security requirement.

```yaml
securitySchemes:
  picoteraSession:
    type: apiKey
    in: cookie
    name: picotera_session
```

---

## Permission → page → endpoint mapping

This table documents which user-facing pages each permission unlocks and which backend endpoints those pages call. New endpoints should pick a permission per the page they belong to; new pages should reuse an existing permission rather than introducing a new one without updating this table.

| Permission              | Visible pages                       | API endpoints                                                       |
|-------------------------|-------------------------------------|---------------------------------------------------------------------|
| (admin)                 | All                                 | All                                                                 |
| `view_own_usage`        | `/requests`, `/requests/{id}`       | `GET /requests`, `GET /requests/{id}`, `GET /requests/{id}/spans`   |
| `view_own_traces`       | `/traces`                           | `GET /request-traces`                                               |
| `manage_own_api_keys`   | `/api-keys`                         | All `/api-keys/*`                                                   |
| `view_models`           | `/models` (read), `/endpoints` (r)  | `GET /models`, `GET /endpoints`                                     |
| (session — always)      | `/me`, `/me/credentials`            | All `/me/*` + `/me/credentials/*`                                   |

Admin auto-passes every permission gate AND every admin-only endpoint. Non-admin behavior is defined per-permission.

Huma's per-operation `Security` field carries this through `humachi`'s registration. The dashboard's `openapi-fetch` client picks it up; cookies are sent automatically by the browser thanks to same-origin defaults.

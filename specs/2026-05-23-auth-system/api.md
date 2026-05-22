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
- Body (bootstrap only): `{ username: string; displayName: string }`. Other intents have no body — target account is fixed.
- Response: `PublicKeyCredentialCreationOptionsJSON`.
- Side effects: stores `webauthn.SessionData` in KV under `webauthn_ceremony:enroll:<token>`. For bootstrap intent, validates that the proposed `username` is unique and matches `^[a-z0-9_-]{2,32}$`; otherwise reads target from `enrollment.target_account_id`. Generates `webauthn_user_handle` for the future account (bootstrap) or reuses the existing handle (invite/reset).
- Errors:
  - `410 enrollment_expired` / `410 enrollment_consumed`.
  - `400 invalid_username` / `400 invalid_display_name` — bootstrap only.
  - `409 username_taken` — bootstrap only.

### `POST /enrollments/{token}/register/complete` — `completeEnrollmentRegistration`

- Auth: **public**.
- Path: `token` (string).
- Body: `{ attestation: RegistrationResponseJSON; nickname: string | null }` plus, for bootstrap, `{ username: string; displayName: string }` (re-supplied so they're authoritative at TX time).
- Response: `SessionView` (the newly created or re-credentialed account).
- Side effects:
  - **bootstrap**: TX inserts `account(role='admin', all can_*=TRUE)`, inserts `webauthn_credential`, marks enrollment consumed. Issues session.
  - **invite**: TX inserts `webauthn_credential` linked to `target_account_id`, marks enrollment consumed. Issues session.
  - **reset**: TX deletes all `webauthn_credential` for target, inserts new `webauthn_credential`, marks enrollment consumed. After commit, scans `session:<target_account_id>:*` in KV and deletes (best-effort). Issues fresh session.
- Errors:
  - `400 webauthn_ceremony_failed` — attestation didn't verify.
  - `410 enrollment_expired` / `410 enrollment_consumed`.
  - `409 username_taken` — bootstrap only (re-checked at TX time).

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
- Body: `{ attestation: RegistrationResponseJSON; nickname: string | null }`.
- Response: `CredentialView`.
- Errors: `400 webauthn_ceremony_failed`.

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
    username: string
    displayName: string
    role: 'admin' | 'user'
    permissions: Record<Permission, boolean>
  }
  ```
- Response: `{ account: AccountView; url: string; expiresAt: string }`. **Reveal-once** for `url`.
- Side effects: TX inserts `account` (no credentials yet, fresh `webauthn_user_handle`) and `enrollment(intent='invite', target=new account, token)`.
- Errors:
  - `400 invalid_username` / `400 invalid_display_name`.
  - `409 username_taken`.

---

## Existing operations — new auth requirements

Every existing `huma.Register(...)` call moves to `registerOp(...)` with an explicit `AuthRequirement`:

| Resource group | Auth requirement | Notes |
|---|---|---|
| `providers`, `provider-endpoints`, `endpoints`, `models` (writes) | `admin` | Provider/endpoint/model configuration is operator-only. |
| `models` (reads), `endpoints` (reads) | `perm:view_models` | Non-admin users get read-only listings. |
| `api-keys` (all operations) | `perm:manage_own_api_keys` | Admin sees all rows; user sees rows where `account_id = me`. New rows are stamped with `account_id` of the caller (admin can override; user cannot). |
| `requests` list/detail | `perm:view_own_usage` (list) / `perm:view_own_traces` (detail / spans) | Scoped via `api_key.account_id`. |
| `traces` | `perm:view_own_traces` | Same scoping. |
| `overview` metrics | `perm:view_own_usage` | Same scoping. |
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

Huma's per-operation `Security` field carries this through `humachi`'s registration. The dashboard's `openapi-fetch` client picks it up; cookies are sent automatically by the browser thanks to same-origin defaults.

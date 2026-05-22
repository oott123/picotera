# Auth & Account System

Add WebAuthn-based authentication for the management API + dashboard and a small account system that lets non-admin users see scoped views (own usage, own API keys, available models).

## Why now

The README's CAUTION is the symptom: the management API has no authentication and must not be exposed publicly. This blocks any operator who wants to host picotera on a non-local network, share it with one or two trusted users, or simply add defense-in-depth on a home network. Closing the auth hole and giving non-admin users their own surface are two halves of the same feature — both need a `user`/`account` model and a session layer.

## Goals

- Passkey-only login for the dashboard. No passwords, no recovery questions.
- Bootstrap and recovery via a CLI subcommand (`picotera enroll-admin`) so total lockout has a documented exit path.
- Two roles: `admin` (everything, existing behavior) and `user` (limited, opt-in).
- Per-user permission toggles (v1: `view_own_usage`, `manage_own_api_keys`, `view_models`, `view_own_traces`).
- Admin issues invitations from the dashboard; recipients enroll a passkey via the same URL flow as bootstrap.
- API key ownership: existing keys remain admin-only legacy; new keys created by users are scoped to them.
- Operator-friendly defaults: plaintext tokens (consistent with `api_key.key`), live role/permission lookup (no JWT-style snapshot drift), and a single migration that ships all schema.

## Non-goals

- OIDC / external IdP / SSO.
- Passwords or any non-WebAuthn second factor.
- Multi-tenancy beyond the small-team / personal-deployment scale.
- Rate limiting (token entropy makes brute force impractical at this scale; deferrable).
- CSP / clickjacking defense (deferred; tracked).
- Cross-tab session sync (deferred; tabs reconcile on the next 401).
- Formal audit log table (logrus events at info level are sufficient).
- Backwards-compatible "auth=off" mode. The new behavior is the only behavior — operators must enroll an admin before the dashboard is usable.

## Phased delivery (single spec, single migration)

| Phase | What ships | What lights up |
|---|---|---|
| 1 | Full migration (account / webauthn_credential / enrollment tables + api_key.account_id). `pkg/auth/` package: session middleware, WebAuthn ceremonies, bootstrap CLI. `LoginView` + `EnrollView` + `MeView`. Auth enforcement on every existing operation. | The management API stops being public. Operators run `picotera enroll-admin`, log in, use the dashboard as today. Only `admin` accounts exist. |
| 2 | Invitation flow: `AccountsView` + `AccountForm`. Permission-toggle UI. | Admin can invite non-admin users; non-admin accounts exist but have no UI of their own yet (they see a "no permissions assigned" state). |
| 3 | Scoped views: every existing list/detail route honors `account_id` scoping in its repository query. `api_key.account_id` actively used. Sidebar adapts per permission. | Non-admin users get a working dashboard scoped to their own data. |

Migration 027 ships in Phase 1 with the full schema; Phases 2 and 3 are purely behavioral additions on top.

### Per-phase endpoint inventory

| Endpoint group | Phase | Notes |
|---|---|---|
| `/auth/*`, `/enrollments/:token/*`, `/me`, `/me/credentials/*` | 1 | Login, self-management, bootstrap-consume path |
| All existing operations migrated to `registerOp(...)` with their final `AuthRequirement` | 1 | Perm gates are wired but dormant (no non-admins yet) |
| `/accounts/*`, `/invitations` | 2 | Admin CRUD + invite flow + AccountsView + AccountForm |
| Scoped repository queries (`ListApiKeysByAccount`, `GetApiKeyOwnedBy`, `ListRequestsByAccount`) + dashboard variants | 3 | Non-admin dashboard becomes useful |

## Breaking change

Operators upgrading from a current build will see all their management-API calls return 401 the moment Phase 1 lands. This is intentional and called out in the design. There is no opt-out config flag.

After upgrading: run `picotera enroll-admin`, open the printed URL, register a passkey. Existing API keys keep working for gateway traffic (`/v1/messages` etc.) — their auth model is unchanged.

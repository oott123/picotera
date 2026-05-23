# Phase 5 — Auth System Fix-ups (Human-Review Round 2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development to implement this plan task-by-task.

**Goal:** Address human-review feedback after Phase 4. Five focused fixes: permission uniformity, admin-implies-all-perms, drop username/displayName suggestions at invite time, persistent invitation list with revoke, read-only model/endpoint views for non-admin. Plus a small api.md sync to document the permission→page→endpoint mapping.

**Architecture:** Surgical fixes. No new packages. One sqlc query addition (ListPendingInvitations). One new endpoint pair (list + revoke invitations). UI hidings and DB-write coercions.

**Branch:** `feat-user-system` (continuation; HEAD is `9d2b9b5`).

**Decisions made up-front (user-approved):**
- view_own_usage and view_own_traces stay distinct. Only `/requests/{id}/spans` moves under view_own_usage.
- Admin role forces all permissions=true at DB write time (not just at read time).
- No new audit events for invitation list/copy — rely on existing log shape.

---

## Task 1 (P5.01): Permission uniformity — spans under view_own_usage

**Goal:** Anyone who can read `/requests` (list, detail) can also read `/requests/{id}/spans`. The page is internally consistent.

**Files:**
- Modify: `pkg/server/server.go` — change `registerOp` for `OperationListRequestSpans` from `RequirePermission(PermViewOwnTraces)` to `RequirePermission(PermViewOwnUsage)`.
- Modify: `specs/2026-05-23-auth-system/api.md` — update the existing-operations table to reflect the move; add a permission→page→endpoint mapping section.

**Acceptance Criteria:**
- [ ] `OperationListRequestSpans` registered with `RequirePermission(PermViewOwnUsage)`.
- [ ] api.md "Existing operations" row for spans says `perm:view_own_usage`.
- [ ] api.md gains a short table: permission name → user-visible page(s) → backend endpoints. Used to audit drift.
- [ ] go build + go test clean.

**Verify:** `MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./... && MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge`

**Steps:**

- [ ] **Step 1:** In `pkg/server/server.go`, find `registerOp(mgmt, contract.OperationListRequestSpans, ...)`. Currently uses `RequirePermission(contract.PermViewOwnTraces)`. Change to `RequirePermission(contract.PermViewOwnUsage)`.

- [ ] **Step 2:** Update api.md. In §"Existing operations — new auth requirements", under the `requests` row, the current text mentions `view_own_usage (list)` / `view_own_traces (detail / spans)`. Change to: `view_own_usage (list + detail + spans)` / `view_own_traces (traces page)`.

- [ ] **Step 3:** Append a new section to api.md titled "Permission → page → endpoint mapping" with:

```
| Permission              | Visible pages                  | API endpoints                                                  |
|-------------------------|--------------------------------|----------------------------------------------------------------|
| (admin)                 | All                            | All                                                            |
| view_own_usage          | /requests, /requests/{id}     | GET /requests, GET /requests/{id}, GET /requests/{id}/spans   |
| view_own_traces         | /traces                        | GET /request-traces                                            |
| manage_own_api_keys     | /api-keys                      | All /api-keys/*                                                |
| view_models             | /models (read), /endpoints (r) | GET /models, GET /endpoints, GET /provider-endpoints           |
| (session — always)      | /me, /me/credentials           | All /me/* + /me/credentials/*                                  |
```

Note: keep it terse. The point is one canonical table that proves uniformity.

- [ ] **Step 4:** Verify + commit.

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
git add pkg/server/server.go specs/2026-05-23-auth-system/api.md
git commit -m "fix(auth): spans use view_own_usage; document permission→page mapping"
```

---

## Task 2 (P5.02): Admin role implies all permissions (server + UI)

**Goal:** When `role=admin`, the four `can_*` permission columns are always `true`. DB never holds `admin + can_view_models=false`. The UI locks the checkboxes when role=admin to reflect this.

**Files:**
- Modify: `pkg/server/handle_account.go::handleUpdateAccount` — when `in.Body.Role == "admin"`, force all four permissions in `UpdateAccountParams` to `true` (regardless of `in.Body.Permissions`).
- Modify: `pkg/server/handle_account.go::handleCreateInvitation` — when `in.Body.Role == "admin"`, set the EnrollmentTemplate's `Perms` to all-true.
- Modify: `dashboard/src/components/AccountForm.vue` — when `form.role === 'admin'`, the four permission checkboxes are disabled + visually all-checked. Add a "管理员拥有全部权限" hint.

**Acceptance Criteria:**
- [ ] Update with `{role: "admin", permissions: {view_own_usage: false, ...}}` results in an account row with all four `can_*` columns set to `true`.
- [ ] Create-invitation with `role=admin` issues an enrollment whose `template_can_*` are all `true`.
- [ ] AccountForm: when form.role === 'admin', permission checkboxes are all checked, disabled, and a hint is visible.
- [ ] When admin switches role from `user` → `admin` in the form, checkboxes immediately reflect all-checked.
- [ ] go build + go test + dashboard build/type-check/lint clean.

**Verify:** server: `curl -X PUT .../accounts/N -d '{"role":"admin","permissions":{"view_own_usage":false,"manage_own_api_keys":false,"view_models":false,"view_own_traces":false},"disabled":false,"displayName":"x"}'` (with admin cookie) → returned `AccountView.permissions` shows all-true.

**Steps:**

- [ ] **Step 1:** In `handleUpdateAccount`, when building `UpdateAccountParams`, coerce admin's perms to all-true:

```go
perms := in.Body.Permissions
if in.Body.Role == "admin" {
    perms = contract.Permissions{
        ViewOwnUsage:     true,
        ManageOwnAPIKeys: true,
        ViewModels:       true,
        ViewOwnTraces:    true,
    }
}
updated, err := q.UpdateAccount(ctx, db.UpdateAccountParams{
    ID:                  in.ID,
    DisplayName:         in.Body.DisplayName,
    Role:                in.Body.Role,
    CanViewOwnUsage:     perms.ViewOwnUsage,
    CanManageOwnApiKeys: perms.ManageOwnAPIKeys,
    CanViewModels:       perms.ViewModels,
    CanViewOwnTraces:    perms.ViewOwnTraces,
    Disabled:            in.Body.Disabled,
})
```

(Adapt to the existing variable names — check the current handler for exact shape.)

- [ ] **Step 2:** Same coercion in `handleCreateInvitation`:

```go
perms := in.Body.Permissions
if in.Body.Role == "admin" {
    perms = contract.Permissions{
        ViewOwnUsage:     true,
        ManageOwnAPIKeys: true,
        ViewModels:       true,
        ViewOwnTraces:    true,
    }
}
tpl := &auth.EnrollmentTemplate{
    Role:        in.Body.Role,
    Perms:       perms,
    Username:    "",  // (Task 3 drops these from the body entirely)
    DisplayName: "",
}
```

- [ ] **Step 3:** In `AccountForm.vue`, add a `computed` that reflects admin-mode:

```ts
const isAdmin = computed(() => form.value.role === 'admin')

// When role flips to admin, lock perms to all-true so the visible state
// matches what the server will persist.
watch(
  () => form.value.role,
  (role) => {
    if (role === 'admin') {
      form.value.permissions = {
        view_own_usage: true,
        manage_own_api_keys: true,
        view_models: true,
        view_own_traces: true,
      }
    }
  },
  { immediate: true },
)
```

(Note: `immediate: true` so the initial state for an existing admin account is also locked.)

- [ ] **Step 4:** Update the permission-checkbox template to disable + add a hint when admin:

```vue
<Field label="权限" as="div">
  <div class="flex flex-col gap-1.5">
    <label
      v-for="(label, perm) in permLabels"
      :key="perm"
      class="flex items-center gap-2 text-sm cursor-pointer"
      :class="isAdmin ? 'text-ink-faint' : 'text-ink-muted'"
    >
      <input
        v-model="form.permissions[perm as keyof Permissions]"
        :disabled="isAdmin"
        type="checkbox"
        class="accent-accent"
      />
      <span>{{ label }}</span>
    </label>
    <p v-if="isAdmin" class="text-2xs text-ink-faint mt-1">
      管理员拥有全部权限
    </p>
  </div>
</Field>
```

- [ ] **Step 5:** Verify + commit.

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
git add pkg/server/handle_account.go dashboard/src/components/AccountForm.vue
git commit -m "fix(auth): admin role forces all permissions=true at write; UI locks checkboxes"
```

---

## Task 3 (P5.03): Drop username/displayName suggestions at invite time

**Goal:** Admin invites by setting role + permissions only. Invitee picks their own username + displayName from scratch. No template hints.

**Files:**
- Modify: `pkg/contract/auth.go` — `createInvitation` request body no longer has `username` / `displayName` fields. Remove `TemplateUsername` from `InvitationResponse` (no longer meaningful).
- Modify: `pkg/server/handle_account.go::handleCreateInvitation` — drop the username precheck, displayName validation, and template-username/displayName population. Template carries only `role` + `perms`.
- Modify: `pkg/auth/enrollment.go::EnrollmentTemplate` — `Username` and `DisplayName` fields removed (unused now).
- Modify: `pkg/auth/enrollment.go::IssueEnrollment` — stops writing the two template_username/display_name columns. (Schema columns remain nullable in 028; just unused.)
- Modify: `pkg/server/handle_enrollment.go::handlePreviewEnrollment` — invite branch no longer reads template_username/display_name; `target` is absent for invite intent.
- Modify: `pkg/server/handle_enrollment.go::handleEnrollmentBeginHTTP` — invite branch logic unchanged (still validates body.Username + DisplayName from the invitee).
- Modify: `dashboard/src/components/AccountForm.vue` — invite mode no longer shows username + displayName fields. Form has only role + permissions.
- Modify: `dashboard/src/views/EnrollView.vue` — invite branch's prefill from `preview.target` becomes dead code; remove the `watchEffect` block and the `hasTemplate` computed. Form starts blank.
- Regenerate: `openapi.yaml` + `dashboard/src/openapi-types.d.ts`.

**Acceptance Criteria:**
- [ ] `POST /api/picotera/invitations` body schema is `{role, permissions}` only.
- [ ] `InvitationResponse` no longer has `templateUsername` field.
- [ ] AccountForm invite mode has zero text inputs — just role SegmentedControl + permissions checkboxes.
- [ ] EnrollView invite branch shows an empty editable form on initial render (no prefilled values).
- [ ] go build + go test + dashboard build/type-check/lint clean.

**Steps:**

- [ ] **Step 1:** In `pkg/contract/auth.go`, edit `createInvitation` body struct (define inline in handle_account.go if not in contract). Currently the body has `Username, DisplayName, Role, Permissions`. Drop Username + DisplayName.

Also remove `TemplateUsername` from `InvitationResponse`:

```go
type InvitationResponse struct {
    URL       string    `json:"url"`
    ExpiresAt time.Time `json:"expiresAt"`
}
```

- [ ] **Step 2:** In `handleCreateInvitation`:
  - Drop the `auth.ValidateUsername(in.Body.Username)` call.
  - Drop the `auth.ValidateDisplayName(in.Body.DisplayName)` call.
  - Drop the `GetAccountByUsername` precheck.
  - Build the template with `Role + Perms` only (no Username/DisplayName).
  - Drop `TemplateUsername` from the response.

- [ ] **Step 3:** In `pkg/auth/enrollment.go`:
  - Remove `Username` and `DisplayName` fields from `EnrollmentTemplate`.
  - In `IssueEnrollment`, drop the two `params.TemplateUsername` / `params.TemplateDisplayName` assignments (just leave them as zero-value `pgtype.Text{}` = NULL).

- [ ] **Step 4:** In `handlePreviewEnrollment::IntentInvite` branch:

```go
case auth.IntentInvite:
    // No target hint — invitee picks their own username/displayName.
```

(Just an empty case; `out.Target` stays nil.)

- [ ] **Step 5:** In `AccountForm.vue`:
  - Drop the `<Field label="...username...">` and `<Field label="...displayName...">` blocks from the invite mode form.
  - Keep them ONLY for edit mode (where they show the existing account's identity, with `:disabled="isEdit"` for username).
  - Use `v-if="isEdit"` to gate them, OR restructure the form.

```vue
<form v-else id="account-form" class="flex flex-col gap-4" @submit.prevent="submit">
  <!-- Identity fields: edit mode only -->
  <template v-if="isEdit">
    <Field :label="usernameLabel">
      <Input v-model="form.username" :disabled="isEdit" required placeholder="例如 alice" pattern="[a-z0-9_\-]{2,32}" />
    </Field>
    <Field :label="displayNameLabel">
      <Input v-model="form.displayName" required placeholder="例如 Alice Smith" />
    </Field>
  </template>
  <Field label="角色" as="div">
    <SegmentedControl v-model="form.role" :options="roleOptions" :columns="2" />
  </Field>
  <!-- ... permissions, status ... -->
</form>
```

Also drop the `usernameLabel` / `displayNameLabel` computeds if they're no longer referenced. (They were used by both modes in Phase 4; check.)

- [ ] **Step 6:** In `EnrollView.vue`:
  - Drop the `watchEffect` that prefills from `preview.target`.
  - Drop the `hasTemplate` computed.
  - Drop the "管理员建议了用户名和显示名…" hint paragraph.
  - The invite form continues to render the two editable inputs (username + displayName), prompting the invitee — that's unchanged.

- [ ] **Step 7:** Regenerate openapi + TS types:

```bash
MISE_DISABLE_TOOLS=pnpm mise run openapi
pnpm --dir dashboard generate-openapi
```

- [ ] **Step 8:** Verify + commit.

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
git add pkg/contract/auth.go pkg/server/handle_account.go pkg/server/handle_enrollment.go pkg/auth/enrollment.go pkg/auth/enrollment_test.go dashboard/src/components/AccountForm.vue dashboard/src/views/EnrollView.vue openapi.yaml dashboard/src/openapi-types.d.ts
git commit -m "feat(auth): admin invites by role+perms only; invitee picks own identity"
```

(Note: `enrollment_test.go` may also need updates if any tests construct an `EnrollmentTemplate` with Username/DisplayName.)

---

## Task 4 (P5.04): Pending invitations — list + revoke

**Goal:** Admin can see all outstanding invitations at any time, copy their URLs, and revoke individual ones. The "reveal-once" posture is dropped (consistent with the project's plaintext-secrets-trust-admin threat model).

**Files:**
- Modify: `db/queries/enrollment.sql` — add `ListPendingInvitations :many` query.
- Regenerate: `pkg/db/`.
- Modify: `pkg/contract/auth.go` — add `InvitationView` type and operation declarations.
- Modify: `pkg/server/handle_account.go` — add `handleListInvitations` and `handleRevokeInvitation`.
- Modify: `pkg/server/server.go` — register both operations as admin-only.
- Modify: `pkg/auth/errors.go` — add `ErrInvitationNotFound` (different from enrollment_consumed; distinguishes "doesn't exist" from "already used/expired").
- Modify: `dashboard/src/api/client.ts` — add `listInvitations`, `revokeInvitation` fetchers.
- Modify: `dashboard/src/api/queryKeys.ts` — add `invitations.all` key.
- Modify: `dashboard/src/views/AccountsView.vue` — add a "Pending invitations" section above the users table. Each row shows role, permissions, createdAt, expiresAt, copy URL button, revoke button.
- Regenerate: `openapi.yaml` + `dashboard/src/openapi-types.d.ts`.

**Acceptance Criteria:**
- [ ] `GET /api/picotera/invitations` returns `[]InvitationView` for outstanding (intent=invite, consumed_at IS NULL, expires_at > now()) enrollments. Sorted by created_at descending.
- [ ] `POST /api/picotera/invitations/revoke` body `{token}` sets `consumed_at = now()`. Returns 204. 404 if token doesn't exist; 409 if already consumed.
- [ ] Each `InvitationView` has: `token` (so the URL can be reconstructed), `role`, `permissions`, `createdAt`, `expiresAt`, `url` (server-built convenience).
- [ ] AccountsView shows a "待发送邀请" / "Pending invitations" section (use Chinese, matching the rest of the dashboard).
- [ ] Copy button copies the full URL to clipboard with timed feedback ("已复制" for 1.5s).
- [ ] Revoke button prompts confirmation via `useConfirm.require`, then revokes + refetches.
- [ ] After invite creation, the section refreshes to include the new entry; the existing reveal-once panel (P4.06) stays as a one-time "here's the URL right after creation" UX, but the section provides the persistent view.
- [ ] go build + go test + dashboard build/type-check/lint clean.

**Verify:** create invitation → appears in list. Copy URL → matches the one shown in the reveal panel. Revoke → removed from list, the original token returns `enrollment_consumed` on consume attempt.

**Steps:**

- [ ] **Step 1:** SQL. Append to `db/queries/enrollment.sql`:

```sql
-- name: ListPendingInvitations :many
SELECT * FROM enrollment
WHERE intent = 'invite'
  AND consumed_at IS NULL
  AND expires_at > now()
ORDER BY created_at DESC;

-- name: RevokeInvitation :one
-- Same DB effect as ConsumeEnrollment, but a distinct callsite so the audit
-- log can record revoke (admin action) vs consume (invitee ceremony).
UPDATE enrollment
SET consumed_at = now()
WHERE token = $1 AND intent = 'invite' AND consumed_at IS NULL
RETURNING *;
```

(The `RevokeInvitation` is intent-restricted to 'invite' so an admin can't accidentally use this to consume a bootstrap/reset token.)

Regenerate:

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- sqlc generate
```

- [ ] **Step 2:** Contract. Add to `pkg/contract/auth.go`:

```go
// InvitationView is a server-side projection of a pending enrollment row,
// including the URL so admin doesn't have to reconstruct it client-side.
type InvitationView struct {
    Token       string      `json:"token"`
    URL         string      `json:"url"`
    Role        string      `json:"role"`
    Permissions Permissions `json:"permissions"`
    CreatedAt   time.Time   `json:"createdAt"`
    ExpiresAt   time.Time   `json:"expiresAt"`
}

var OperationListInvitations = huma.Operation{
    OperationID: "listInvitations",
    Method:      http.MethodGet,
    Path:        "/invitations",
    Summary:     "List outstanding (unconsumed, unexpired) invitations.",
}

var OperationRevokeInvitation = huma.Operation{
    OperationID: "revokeInvitation",
    Method:      http.MethodPost,
    Path:        "/invitations/revoke",
    Summary:     "Revoke a pending invitation by token.",
}
```

- [ ] **Step 3:** New auth error in `pkg/auth/errors.go`:

```go
// ErrInvitationNotFound differentiates "token never existed" from
// "token consumed/expired" so the admin UI can show a clean message.
func ErrInvitationNotFound() *AuthError {
    return &AuthError{Code: "invitation_not_found", Status: 404,
        Message: "invitation not found"}
}
```

- [ ] **Step 4:** Handlers. Add to `pkg/server/handle_account.go`:

```go
type listInvitationsOut struct {
    Body []contract.InvitationView
}

func (s *Server) handleListInvitations(ctx context.Context, _ *struct{}) (*listInvitationsOut, error) {
    rows, err := s.queries.ListPendingInvitations(ctx)
    if err != nil {
        return nil, fmt.Errorf("handleListInvitations: %w", err)
    }
    views := make([]contract.InvitationView, 0, len(rows))
    for _, r := range rows {
        // Permissions snapshot from template_* columns. If template_* are NULL
        // (legacy invite rows from before P4.04), treat them as all-false.
        perms := contract.Permissions{
            ViewOwnUsage:     r.TemplateCanViewOwnUsage.Bool,
            ManageOwnAPIKeys: r.TemplateCanManageOwnApiKeys.Bool,
            ViewModels:       r.TemplateCanViewModels.Bool,
            ViewOwnTraces:    r.TemplateCanViewOwnTraces.Bool,
        }
        role := "user"
        if r.TemplateRole.Valid {
            role = r.TemplateRole.String
        }
        views = append(views, contract.InvitationView{
            Token:       r.Token,
            URL:         s.config.PublicOrigins[0] + "/enroll/" + r.Token,
            Role:        role,
            Permissions: perms,
            CreatedAt:   r.CreatedAt.Time,
            ExpiresAt:   r.ExpiresAt.Time,
        })
    }
    return &listInvitationsOut{Body: views}, nil
}

type revokeInvitationIn struct {
    Body struct {
        Token string `json:"token"`
    }
}

func (s *Server) handleRevokeInvitation(ctx context.Context, in *revokeInvitationIn) (*struct{}, error) {
    sess := auth.SessionFromContext(ctx)
    row, err := s.queries.RevokeInvitation(ctx, in.Body.Token)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            // Could be: token doesn't exist, or already consumed, or wrong
            // intent. Don't distinguish to avoid leaking token existence.
            return nil, authErrToHuma(auth.ErrInvitationNotFound())
        }
        return nil, fmt.Errorf("handleRevokeInvitation: %w", err)
    }
    logx.WithContext(ctx).WithFields(logrus.Fields{
        "event":    "auth.invitation_revoked",
        "actor_id": sess.Account.ID,
        "token4":   in.Body.Token[:4],
    }).Info("auth")
    _ = row // returned by RETURNING; unused here but the query needed :one
    return &struct{}{}, nil
}
```

- [ ] **Step 5:** Register in `pkg/server/server.go`:

```go
registerOp(mgmt, contract.OperationListInvitations,
    server.handleListInvitations, contract.AuthRequirement{Kind: contract.AuthAdmin})
registerOp(mgmt, contract.OperationRevokeInvitation,
    server.handleRevokeInvitation, contract.AuthRequirement{Kind: contract.AuthAdmin})
```

Place near the other account/invitation registrations.

- [ ] **Step 6:** Regenerate openapi + TS types:

```bash
MISE_DISABLE_TOOLS=pnpm mise run openapi
pnpm --dir dashboard generate-openapi
```

- [ ] **Step 7:** Frontend fetchers in `dashboard/src/api/client.ts`:

```ts
type InvitationView = components['schemas']['InvitationView']

export async function listInvitations(): Promise<InvitationView[]> {
    const { data, error } = await api.GET('/api/picotera/invitations')
    if (error) fail(error, '加载邀请失败')
    return data ?? []
}

export async function revokeInvitation(token: string): Promise<void> {
    const { error } = await api.POST('/api/picotera/invitations/revoke', { body: { token } })
    if (error) fail(error, '撤销邀请失败')
}

export function invalidateInvitations(client: QueryClient) {
    client.invalidateQueries({ queryKey: queryKeys.invitations.all })
}
```

In `dashboard/src/api/queryKeys.ts`:

```ts
invitations: {
    all: ['invitations'] as const,
},
```

- [ ] **Step 8:** AccountsView section. Add above the users table:

```vue
<!-- Pending invitations section -->
<DataCard v-if="invitations.length > 0">
  <div class="px-4 py-3 border-b border-line flex items-center justify-between">
    <h3 class="text-sm font-medium text-ink">待发送邀请 ({{ invitations.length }})</h3>
  </div>
  <DataTable>
    <thead>
      <tr>
        <Th>角色</Th>
        <Th>权限</Th>
        <Th>创建时间</Th>
        <Th>过期时间</Th>
        <Th actions />
      </tr>
    </thead>
    <tbody>
      <Tr v-for="inv in invitations" :key="inv.token">
        <Td>
          <Tag :variant="inv.role === 'admin' ? 'accent' : 'default'">
            {{ inv.role === 'admin' ? '管理员' : '标准用户' }}
          </Tag>
        </Td>
        <Td>
          <div class="flex flex-wrap gap-1">
            <Tag v-if="inv.role === 'admin'" variant="muted">全部</Tag>
            <template v-else>
              <Tag v-if="inv.permissions.view_own_usage" variant="muted">用量</Tag>
              <Tag v-if="inv.permissions.manage_own_api_keys" variant="muted">密钥</Tag>
              <Tag v-if="inv.permissions.view_models" variant="muted">模型</Tag>
              <Tag v-if="inv.permissions.view_own_traces" variant="muted">链路</Tag>
            </template>
          </div>
        </Td>
        <Td><span class="text-xs text-ink-muted">{{ fmtTime(inv.createdAt) }}</span></Td>
        <Td><span class="text-xs text-ink-muted">{{ fmtTime(inv.expiresAt) }}</span></Td>
        <Td actions>
          <div class="inline-flex gap-1">
            <IconButton :title="copiedToken === inv.token ? '已复制' : '复制链接'" @click="copyInviteUrl(inv)">
              <Icon :name="copiedToken === inv.token ? 'check' : 'copy'" :size="13" />
            </IconButton>
            <IconButton title="撤销邀请" @click="confirmRevokeInvitation(inv)">
              <Icon name="trash" :size="13" />
            </IconButton>
          </div>
        </Td>
      </Tr>
    </tbody>
  </DataTable>
</DataCard>
```

Script setup additions:

```ts
import { listInvitations, revokeInvitation, invalidateInvitations } from '@/api/client'
type InvitationView = components['schemas']['InvitationView']

const invitationsQuery = useQuery({
    queryKey: queryKeys.invitations.all,
    queryFn: listInvitations,
})
const invitations = computed(() => invitationsQuery.data.value ?? [])

const copiedToken = ref<string | null>(null)
let copyTimer: ReturnType<typeof setTimeout> | null = null

async function copyInviteUrl(inv: InvitationView) {
    try {
        await navigator.clipboard.writeText(inv.url)
        copiedToken.value = inv.token
        if (copyTimer) clearTimeout(copyTimer)
        copyTimer = setTimeout(() => { copiedToken.value = null }, 1500)
    } catch {}
}

const revokeMutation = useMutation({
    mutationFn: (token: string) => revokeInvitation(token),
    onSuccess: () => invalidateInvitations(queryClient),
})

function confirmRevokeInvitation(inv: InvitationView) {
    confirm.require({
        message: '确定要撤销这个邀请吗？该链接将立即失效。',
        accept: async () => { await revokeMutation.mutateAsync(inv.token) },
    })
}
```

Also: when a new invitation is created (from AccountForm's success), invalidate `queryKeys.invitations.all` so the section refreshes. Find the existing `inviteMutation`'s `onSuccess` in AccountForm and add `invalidateInvitations(queryClient)`:

```ts
const inviteMutation = useMutation({
    mutationFn: (body: {...}) => createInvitation(body),
    onSuccess: () => {
        invalidateAccounts(queryClient)
        invalidateInvitations(queryClient)
    },
})
```

- [ ] **Step 9:** Verify + commit.

```bash
MISE_DISABLE_TOOLS=pnpm mise exec -- go build ./...
MISE_DISABLE_TOOLS=pnpm mise exec -- go test ./pkg/auth ./pkg/server ./pkg/llmbridge
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
git add db/queries/enrollment.sql pkg/db/ pkg/contract/auth.go pkg/auth/errors.go pkg/server/handle_account.go pkg/server/server.go dashboard/src/api/client.ts dashboard/src/api/queryKeys.ts dashboard/src/views/AccountsView.vue dashboard/src/components/AccountForm.vue openapi.yaml dashboard/src/openapi-types.d.ts
git commit -m "feat(auth): list + revoke pending invitations"
```

---

## Task 5 (P5.05): Models/Endpoints pages read-only for non-admin

**Goal:** Non-admin users with `view_models` see model and endpoint data but cannot edit, delete, or create. UI hides all mutation affordances.

**Files:**
- Modify: `dashboard/src/views/ModelsView.vue` — hide "新增" button, edit/delete IconButtons, and any other CRUD actions when `!session.isAdmin.value`.
- Modify: `dashboard/src/views/EndpointsView.vue` — same treatment.
- Modify: `dashboard/src/views/ProvidersView.vue` (if reachable for non-admin) — N/A, this view requires admin. But check `ModelListEditor` and any panels — if they're embedded in non-admin reachable views, gate them. (They're not; ProvidersView is admin-only.)

**Acceptance Criteria:**
- [ ] ModelsView: no "新增" button, no edit/delete IconButtons visible when caller is not admin. The list table still shows all model data.
- [ ] EndpointsView: same treatment.
- [ ] Admin sees the existing full functionality unchanged.
- [ ] dashboard build/type-check/lint clean.

**Verify:** Sign in as a standard user with `view_models`. Visit `/models` and `/endpoints` — see read-only listings. Sign in as admin, see the full CRUD UI.

**Steps:**

- [ ] **Step 1:** In `ModelsView.vue`, add `useSession` import + call (if not already there):

```ts
import { useSession } from '@/composables/useSession'
const session = useSession()
```

Find the "新增" button (usually `<Button @click="openCreate">`) and gate it:

```vue
<Button v-if="session.isAdmin.value" @click="openCreate">
  <Icon name="plus" :size="14" :stroke-width="2.2" />
  <span>新增模型</span>
</Button>
```

Find the row actions (edit + delete IconButtons) and gate them:

```vue
<Td actions>
  <div v-if="session.isAdmin.value" class="inline-flex gap-1 ...">
    <IconButton ... edit ...>
    <IconButton ... delete ...>
  </div>
</Td>
```

(Or wrap each individually — wrap the whole `<div>` is cleaner.)

- [ ] **Step 2:** Same treatment in `EndpointsView.vue`.

- [ ] **Step 3:** Sanity check: are there other views with admin-write but `view_models`-read mismatch? Grep for `view_models` route definitions and inspect each.

```bash
grep -rn "view_models" dashboard/src
```

ModelsView and EndpointsView are the only two. ProvidersView is admin-only. ProviderEndpointsPanel is admin-only (opened from ProvidersView).

- [ ] **Step 4:** Verify + commit.

```bash
pnpm --dir dashboard type-check
pnpm --dir dashboard build
pnpm --dir dashboard lint
git add dashboard/src/views/ModelsView.vue dashboard/src/views/EndpointsView.vue
git commit -m "feat(dashboard): models + endpoints pages read-only for non-admin"
```

---

## Verify-and-Ship checkpoint

After all 5 tasks land, manual smoke:

1. **P5.01 spans uniformity**: as non-admin with view_own_usage only, /requests/{id}/spans returns 200 (not 403).
2. **P5.02 admin all-perms**: invite an admin with permissions all-false → resulting account has all four can_* true.
3. **P5.03 no suggestions**: admin invites with role=user, perms=manage_own_api_keys; reveal URL; open in private window; EnrollView shows blank editable form.
4. **P5.04 invitations list**: after invite creation, AccountsView "待发送邀请" section shows it; copy URL works; revoke removes it + invalidates the token.
5. **P5.05 read-only**: as non-admin with view_models, /models shows the listing but no edit/delete/add buttons.

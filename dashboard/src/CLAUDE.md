# Dashboard src CLAUDE.md

## Permission-aware views

Every view with **permission-driven UI differences** MUST declare a single `mode` computed at the top of `<script setup>`. All permission gates within the view — both `useQuery({ enabled })` and `v-if` template checks — consult `mode` (or a named capability derived from `session`). Views where admin and non-admin render identically (because the backend handles row-scoping transparently) don't need to declare a mode — the route's `meta.auth` already documents the entry gate, and a forced-but-unused `mode` declaration is just noise.

```ts
const session = useSession()
// admin sees full data; readonly can view only
const mode = computed<'admin' | 'readonly'>(() =>
  session.isAdmin.value ? 'admin' : 'readonly'
)
```

Mode names are page-specific. Common patterns:
- `'admin' | 'readonly'` — view shows the same data; non-admin can read but not edit (e.g. ModelsView, EndpointsView).
- `'admin' | 'scoped'` — non-admin sees only their own data; the API does row-level scoping (e.g. RequestsView, ApiKeysView, TracesView).

Where the page's primary mode doesn't capture all permission distinctions, declare named **secondary capabilities** alongside:

```ts
// ApiKeysView.vue — admin/scoped split governs which list endpoint to hit;
// a secondary capability gates a specific sub-action.
const canRevokeAny = computed(
  () => mode.value === 'admin' || session.can('manage_own_api_keys')
)
```

When a view has **no single admin-vs-not axis** — every UI difference is gated on a different permission — skip the mode declaration entirely and use pure named capabilities. `RequestsView.vue` is the canonical example: it has no mode, just `canFilterByModel`, `canFilterByProject`, `canFilterByProvider` (each backed by the permission whose API populates the filter's data). The backend transparently scopes rows per role on the same endpoint, so the view renders identically for admin and non-admin.

**Never reference `session.isAdmin.value` or `session.can(perm)` directly inside a view file.** All such checks belong in the mode/capability declarations at the top. This makes adding a new permission-aware element a single new check that matches the existing pattern, and reviewers can audit gating by looking at one block.

Composables (e.g. `useProvidersMap`, `useProjectsMap`) that fetch admin-only data gate internally on `session.isAdmin.value`. Views consuming those composables don't need to do anything special.

## See also

- `dashboard/DESIGN_SYSTEM.md` — design tokens and UI primitives.
- `dashboard/src/views/CLAUDE.md` — page-meta registration.

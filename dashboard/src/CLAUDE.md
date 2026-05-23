# Dashboard src CLAUDE.md

## Permission-aware views

Every view that's reachable by non-admin users (i.e. `meta.auth` is `'session'`, a permission, or has `mode`-driven UI differences for non-admin) MUST declare a single `mode` computed at the top of `<script setup>`. All permission gates within the view — both `useQuery({ enabled })` and `v-if` template checks — consult `mode` (or a named capability derived from `session`).

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
// RequestsView.vue — the filter dropdowns need view_models even in scoped mode.
const canFilterByModel = computed(
  () => mode.value === 'admin' || session.can('view_models')
)
```

**Never reference `session.isAdmin.value` or `session.can(perm)` directly inside a view file.** All such checks belong in the mode/capability declarations at the top. This makes adding a new permission-aware element a single new check that matches the existing pattern, and reviewers can audit gating by looking at one block.

Composables (e.g. `useProvidersMap`, `useProjectsMap`) that fetch admin-only data gate internally on `session.isAdmin.value`. Views consuming those composables don't need to do anything special.

## See also

- `dashboard/DESIGN_SYSTEM.md` — design tokens and UI primitives.
- `dashboard/src/views/CLAUDE.md` — page-meta registration.

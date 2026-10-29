# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with the PicoTera dashboard.

## Stack

Vue 3 (beta, pinned in `pnpm-workspace.yaml` overrides) + Tailwind CSS v4 + Pinia + Vue Router + TypeScript + `@tanstack/vue-query` for data fetching. Charts via `vue-echarts` (Apache ECharts). Virtualized lists via `@tanstack/vue-virtual`. Icons via `@tabler/icons-vue`; floating/popover positioning via `@floating-ui/vue`. Package manager is pnpm (workspace root at repo root).

**Design system reference**: before building or modifying UI, read `DESIGN_SYSTEM.md` for tokens, primitives (`src/ui/`), and conventions.

## Layout

- `src/main.ts`, `src/App.vue` — app bootstrap; `AppSidebar` + router-view shell.
- `src/router/` — Vue Router config.
- `src/stores/` — Pinia stores (`preferences.ts` holds theme, panel mode, font size, display currency).
- `src/views/` — route-level pages. One view per management resource, plus overview and request history.
- `src/components/` — feature-level components: forms, editors, side panels, chart wrappers, artifact viewers, chrome.
- `src/composables/` — reusable composition functions.
- `src/lib/` — plain TypeScript modules with no Vue dependency. `toolFormat.ts` renders tool calls / tool results as one-line Python-style literals (`Bash(command="ls")`, `Bash → "…"`, each value capped at 128 chars) and is meant to be reused wherever a tool call needs a summary; `testBody.ts` / `testStream.ts` back the gateway test view.
- `src/api/` — `openapi-fetch` client (`plugin.ts`), shared `QueryClient` (`queryClient.ts`), typed `queryKeys` registry (`queryKeys.ts`), async fetcher wrappers + invalidation helpers (`client.ts`), and re-exported schema types (`index.ts`). Generated types live at `src/openapi-types.d.ts` (output of `pnpm --dir dashboard generate-openapi`).
- `src/ui/` — **local UI primitive library. No third-party UI kit. No variant-authoring libs (cva/tv).** Style with Tailwind classes directly inside each component.

## Local UI Primitives (`src/ui/`)

Re-exported via `src/ui/index.ts`. Prefer these over ad-hoc markup.

- **Form**: `Button`, `IconButton`, `Input`, `Select`, `Textarea`, `Field` (label + error + help wrapper), `SegmentedControl`.
- **Data display**: `DataCard` (titled container), `DataTable` + `Th` / `Td` / `Tr` (table primitives), `AutoDataTable` (data-driven table from `columns + items`, with slot overrides `#header-<key>` / `#cell-<key>`), `Badge`, `Tag`, `TagList`, `StateText` (state-colored inline text), `MoneyDisplay`.
- **Filtering**: `ColumnFilter` (dropdown filter with optional search, positioned via floating-ui).
- **Overlays / navigation**: `Overlay` (backdrop layer), `SidePanel` (slide-over, driven by `useSidePanel`), `ConfirmDialog` (driven by `useConfirm`), `Tabs`.
- **Code**: `CodeEditor` (CodeMirror 6 wrapper).
- **Icons**: `Icon` component with `IconName` type, fed from `src/ui/icons/paths.ts`. Use `@tabler/icons-vue` when adding new icons.

When building new screens, compose these primitives — don't reach for a third-party UI library, and don't introduce a variant DSL. Tailwind v4 utility classes are the styling vocabulary.

## Composables

- `useApi` — typed `openapi-fetch` client instance.
- `useConfirm` — global confirm dialog (all destructive actions route through this).
- `useSidePanel` — global slide-over stack with stable `key` for row selection tracking.
- `useArtifact` — loads and decompresses request/response artifacts from MinIO.
- `useSSEParser` — parses SSE event streams, extracts content from multiple LLM providers (OpenAI Chat/Responses, Anthropic, Gemini), renders markdown via `@tanstack/markdown` (raw HTML is escaped and shown as literal text; output is no longer passed through DOMPurify). Supports timing injection per event.
- `useRequestDetailUiState` — manages request detail view tab state (overview/request/response/conversation/logs/usage), body view mode, header/thinking visibility toggles.
- `useCurrencyContext` — provides inject/provide pattern for currency conversion with exchange rate lookups.
- `useExchangeRates` — fetches exchange rate data.
- `useProjectsMap` / `useProvidersMap` — reactive lookup maps for projects and providers by ID.

## Data layer (vue-query)

`@tanstack/vue-query` is the *only* sanctioned way to read API data in views/components. The shared `QueryClient` (`src/api/queryClient.ts`) is registered in `src/main.ts` via `VueQueryPlugin`; it ships sensible defaults (`refetchOnWindowFocus: false`, `retry: 1`, mutation `retry: 0`) and exports two stale-time constants — `MANAGEMENT_STALE_TIME` (30s, the default) for config resources and `OPERATIONAL_STALE_TIME` (5s) for live data. Override per-`useQuery` only when needed.

- **Fetchers in `src/api/client.ts`** — plain async functions wrapping `api.GET`/`PUT`/`POST` (the openapi-fetch client). They throw `ApiRequestError` (with localized fallback messages) on non-2xx so vue-query's `error`/`isError` flows work uniformly. Don't call `api.GET` directly from views — add a fetcher here.
- **Keys in `src/api/queryKeys.ts`** — single hierarchical `queryKeys` object (`all` / `list(filters)` / `detail(id)` shape per resource). Always derive keys from this object; never inline `['providers', id]` literals. Filtered list keys take a `Readonly<>` filter object so the spread is deterministic. Filter / cursor types (`RequestsFilters`, `CursorFilters`) are exported here for views that pass reactive filters.
- **Reactive queries** — when filters or cursors are reactive, pass a `computed(() => queryKeys.x.list(...))` as `queryKey` and reference the same reactive values inside `queryFn` so vue-query refires on change. `RequestsView.vue` and `TracesView.vue` are the canonical examples.
- **Mutations** — call the corresponding `client.ts` writer (`upsertProvider`, `deleteScript`, etc.) inside `useMutation`, then on success invoke the matching `invalidate*` helper from `client.ts` (e.g. `invalidateProviderEndpoints` already fans out to providers + models). Prefer these helpers over ad-hoc `client.invalidateQueries` so cross-resource fanout stays in one place.
- **Error rendering** — surface `ApiRequestError.message` (already localized) in the UI; `code` / `details` are available on the error for finer handling.

## Views & router page metadata

When you add a new route, register the route name in `src/App.vue`'s `pageMeta` map (`title` + `hint`). The shell reads it to render header chrome — without an entry the page renders untitled. The map key must match the route's `name` exactly (defined in `src/router/index.ts`). See `src/views/CLAUDE.md`.

Routes: `/overview` (default), `/providers`, `/models`, `/endpoints`, `/requests`, `/requests/:requestId`, `/projects`, `/scripts`, `/kv`, `/api-keys`, `/rates`, `/traces`.

## Charts

Chart components live in `src/components/charts/` and use `vue-echarts` (Apache ECharts v6 with modular imports via `echarts.ts`).

In `OverviewLineChart`, `OverviewAreaStack` and `OverviewBoxplotSeries`, a (group, bucket) the backend returned no point for is empty data and is filled with ECharts' `'-'`, so lines, areas and median lines break there; a returned point is drawn even when it is 0. Ratio metrics (cache hit rate, success rates, empty-reply rate, finish-reason share) omit the point when the denominator is 0, so they break on idle buckets; count metrics (tokens, cost, requests, traces) return a point for every bucket and never break. All three draw their own `<ul>` legend; hovering an item highlights its series via `dispatchAction`, the same as hovering the series itself (re-applied after a legend click / right-click changes which series exist).

- `OverviewAreaStack` — stacked area chart for request volume over time.
- `OverviewDonut` — donut chart for distribution breakdowns.
- `OverviewBoxplotSeries` — per-bucket distribution chart for the speed section (prefill/decode speed, TTFT), with the same per-series toggle/isolate legend as `OverviewLineChart`. Its mode follows the number of *visible* groups: with exactly one it draws that group's boxplot (colors match `OverviewSpeedTimeline`) plus a straight line through the medians; with several it drops the boxes and draws only smoothed median lines with point symbols, like `OverviewLineChart`. The median lines break on buckets without samples, and highlight on hover through `emphasis.focus: 'series'`; legend hover does nothing in box mode, where there is a single group to begin with.
- `OverviewLineChart` — multi-series line chart with per-series toggle/isolate, used for per-bucket time series such as cache hit rate. Draws a symbol on every point (`showAllSymbol: true`, since the category axis' default `'auto'` would thin out an isolated point between two gaps).
- `OverviewSankey` — Sankey diagram for model → provider routing flow.
- `OverviewSpeedTimeline` — horizontal boxplot chart for min-max speed ranges; the border is a darker (light themes) / lighter (dark themes) shade of the fill, which is what makes the median line stand out.

Shared color palette in `charts/colors.ts` (reads `--color-chart-0` through `--color-chart-9` CSS variables). ECharts module registration in `charts/echarts.ts`.

## Design Context

### Users
ML/AI engineers configuring model providers, testing endpoints, and optimizing inference costs. They're technically fluent, time-constrained, and working in high-stakes environments where misconfiguration means downtime or overspend. They need to quickly understand routing state, diagnose failures, and iterate on provider setups without hand-holding.

### Brand Personality
**Modern, Smart, Confident** — PicoTera feels like infrastructure that knows what it's doing. It projects competence without arrogance. The interface should feel like talking to a sharp colleague: direct, precise, never wasteful. Not flashy or playful — purposefully understated because the tool speaks through its capability.

### Aesthetic Direction
Light mode primary with clean, spacious surfaces. Information-dense but not cluttered — think Grafana/Datadog: dashboard-first, data-rich, scannable at a glance. Professional polish over decorative flair. A blue primary accent against neutral light backgrounds. Typography should be crisp and hierarchical. Avoid minimal-for-minimal's-sake; density is good when it serves scanning.

**References**: Grafana, Datadog — functional density, clear data hierarchy, status-driven UI
**Anti-references**: Overly minimal landing-page aesthetics, playful micro-interactions that slow down power users, dark-mode-only developer tools

### Design Principles

1. **Signal over decoration** — Every pixel should earn its place. Status colors, data density, and clear hierarchy matter more than visual flourish. If it doesn't help an engineer make a decision, it doesn't belong.
2. **Scan, don't read** — Optimize for the 3-second glance. Tables, status badges, and metrics should be immediately parseable. Reserve detailed views for drill-down, not the default.
3. **Confidence through clarity** — The UI should never leave the user guessing about state. Active vs. inactive, healthy vs. failing, configured vs. missing — binary visual signals with no ambiguity.
4. **Fast over fancy** — ML engineers are iterating quickly. Interactions should be direct and predictable. Prefer instant feedback to animated transitions. CRUD operations should feel like editing a spreadsheet, not navigating a wizard.
5. **Progressive density** — Start with a clean overview, then reveal detail on demand. Summary cards → expandable rows → detail panels. Never hide critical info behind clicks, but don't overwhelm with everything at once.

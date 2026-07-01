# PicoTera Dashboard — Design System

Reference for the dashboard's current visual language, tokens, and UI primitives. This documents what **exists** today in `dashboard/src/ui/` and `dashboard/src/index.css`; for the *why* and brand direction, see `.impeccable.md` and the `## Design Context` block in `CLAUDE.md`.

When building new screens: compose primitives from `src/ui/` and style additions with Tailwind v4 utilities. No third-party UI kit, no variant-authoring library (cva / tv). Read tokens through the semantic names below — never hard-code colors.

---

## 1. Foundations

### Typography

- **Sans**: `Geist` (self-hosted via jsDelivr `@font-face`, weights 400/500/600/700) → `--font-sans`.
- **Mono**: `Geist Mono` (weight 400) → `--font-mono`. Used for IDs, code-like values, Tags, Badges, and anything the user might copy.
- **Root size**: `html { font-size: 14px }`. All `rem` values scale off this — `1rem = 14px`.
- **Feature settings**: `body` has `font-feature-settings: "ss01", "cv11"`. The helper classes `.mono` and `.tabular` enable `"tnum", "zero"` for aligned numbers and slashed zero — use them on metric values and IDs in tables.

Type scale (Tailwind utilities, all driven by `--text-*` in `@theme`):

| Utility | Size (rem / px @14px root) | Typical use |
| --- | --- | --- |
| `text-2xs` | `9/14rem` (9px), lh `1` | Labels, kickers, uppercase meta |
| `text-xs` | `10/14rem` (10px) | Tags, Badges, Tab labels |
| `text-sm` | `11/14rem` (11px) | Body of tables, inputs, buttons |
| `text-base` | `1rem` (14px) | Default paragraph |
| `text-xl` | `17/14rem` (17px) | Section titles |

Weights in use: `font-normal` (400), `font-medium` (500), `font-semibold` (600). Don't introduce `font-bold` (700) in UI chrome — reserve it for emphasis inside content.

Uppercase labels use `text-2xs font-medium uppercase tracking-[0.03em]` (or `0.04em` on panel kickers, `0.06em` on sidebar section headers). Pattern visible in `Field.vue`, `SidePanel.vue`, `AppSidebar.vue`.

### Spacing

- Base unit is unusual: `--spacing: calc(3 / 14 * 1rem)` ≈ **3px**. Tailwind's numeric spacing utilities (`p-2`, `gap-4`, etc.) are multiples of this 3px unit, not the default 4px. `p-2` = 6px, `p-4` = 12px, `gap-1` = 3px.
- Use the numeric utilities; don't reach for arbitrary `px-[12px]` when a scale step fits. When you truly need an off-scale value (typically for optical alignment), use arbitrary values sparingly — `py-[1.125rem]` and `w-[1.375rem]` exist in the codebase for this reason.

### Radius

- `rounded-sm` (Tailwind default 2px) — segmented-control inner pills.
- `rounded-xs` = `0.1875rem` — custom token, small inset elements.
- `rounded-md` — inputs, buttons, side panels in lists.
- `rounded-[5px]` — Tags and Badges (distinct from inputs, on purpose).
- `rounded-xl` = `0.625rem` — cards, modals, side panels.

### Shadows

Three custom tokens; stick to these:

- `shadow-xs` — resting state of filled buttons and subtle chrome.
- `shadow-sm` — cards (`DataCard`, `SidePanel`).
- `shadow-lg` — floating surfaces (`ConfirmDialog`).

Row "selected" indicator uses an inset shadow as an accent stripe, not a border: `shadow-[inset_2px_0_0_var(--color-accent)]` (`Tr.vue`).

### Color

All colors are OKLCH custom properties declared in `@theme` (light default) and overridden per-theme in `:root[data-theme="…"]` blocks. Themes shipped: **light** (default), **dark**, **solarized-light**, **solarized-dark**. Switching is data-attribute-driven, handled by `stores/preferences.ts`.

Semantic token groups — always refer to these, never pick a raw OKLCH value inline:

| Group | Tokens | Purpose |
| --- | --- | --- |
| Surface | `surface-0`, `50`, `100`, `200`, `300` | Page/card backgrounds, ascending elevation tint |
| Ink | `ink`, `ink-muted`, `ink-faint` | Primary / secondary / tertiary text |
| Accent | `accent`, `accent-strong`, `accent-faint`, `accent-ink` | Brand blue; `-faint` is a tinted surface, `-ink` is a readable text shade over `-faint` |
| State: ok | `ok`, `ok-ink`, `ok-faint` | Healthy / success |
| State: warn | `warn`, `warn-ink`, `warn-faint` | Degraded / caution |
| State: err | `err`, `err-ink`, `err-faint` | Failure / destructive |
| Line | `line`, `line-soft` | Borders; `-soft` for table row separators |
| Sidebar | `sidebar-bg`, `sidebar-text`, `sidebar-text-active`, `sidebar-hover`, `sidebar-active-bg`, `sidebar-active-text` | Sidebar-specific roles |
| Overlay | `overlay-bg` | Modal/panel scrim (already includes alpha) |

Convention: the pair `{state}-faint` (tinted surface) + `{state}-ink` (text) is what you use together — e.g. `bg-ok-faint text-ok-ink`. `{state}` on its own is the solid/strong shade used for Button fills and row accents.

Neutrals are **tinted toward the accent hue** (255–262° blue). Don't substitute with untinted grays; it breaks cohesion across themes.

### Motion

Transitions throughout the codebase are `transition-colors` only. Keep it that way: no motion on layout, no bounce easing. State changes are instant-feeling. The one universal interactive affordance is a 0.5px nudge on primary Button press (`active:translate-y-[0.5px]`) — enough to feel tactile, not enough to look animated.

---

## 2. Primitives (`src/ui/`)

Imported from the barrel `src/ui/index.ts`. Keep adding to this barrel when introducing new primitives.

### Form

- **`Button`** — variants: `primary` (default, accent fill), `ghost` (surface-0 w/ border), `danger` (err fill). Sizes: `sm`, `md`. Slot-based content; icons go inside, `gap-2` already applied.
- **`IconButton`** — square (`1.375rem` sm / `1.625rem` md), transparent by default, hover tints background. `active` prop gives it the selected-row treatment (`accent-faint` + `accent-ink`). `variant: 'danger'` gives it an err-faint hover. Use for table row actions, panel close buttons, header icon controls.
- **`Input`** — 1-line text; `mono` prop flips to `font-mono`. Supports `.number` / `.trim` / `.lazy` modelModifiers via the `modelModifiers` prop. Focus ring is `ring-[3px] ring-accent/20` plus a hard `border-accent`.
- **`Select`** — plain native `<select>`, same border/ring pattern as Input (no ring on focus, just border).
- **`Textarea`** — min-height 24 units (72px), `resize-y`, same focus treatment as Input.
- **`Field`** — label + control + error wrapper. Renders as a `<label>` by default (set `as="div"` when the slot contains non-labelable content). Label styling is the uppercase `text-2xs` treatment; error is `text-sm text-err`.
- **`SegmentedControl`** — mutually-exclusive choice, equal-width grid. Pass `options: { value, label }[]`; optional `columns` overrides the grid count. Selected cell is a `surface-0` pill with a faint accent-ink text and a hairline ring.

### Data display

- **`DataCard`** — generic titled container: `surface-0` background, `border-line`, `rounded-xl`, `shadow-sm`, `overflow-hidden`. Compose the header yourself inside the slot.
- **`DataTable` + `Tr` + `Th` + `Td`** — full-width `<table>` with `border-separate border-spacing-0`. `Th actions` / `Td actions` variant pins the column to the right with `w-[1%]`. `Tr selected` draws the 2px inset accent stripe; `Tr hoverable` (default true) gives row hover state.
- **`AutoDataTable`** — thin wrapper around `DataTable`/`Tr`/`Th`/`Td` that renders rows from `columns + items`. Props: `columns: AutoDataTableColumn<Row>[]`, `items: Row[]`, `rowKey(row, i) => string | number`, optional `selected(row) => boolean`, `hoverable`, `onRowClick(row, event)`. Column has `{ key, header?, field?, actions?, align?, headerClass?, cellClass? }`; `field` supports dotted paths. Slots: `#header-<key>` and `#cell-<key>="{ row, value, index }"` override the default header / cell rendering. Use this when the table is pure data-in / row-out; fall back to composing `DataTable` directly when rows need irregular structure.
- **`Badge`** — small monospace counter pill, intended for numeric values (row counts, sizes). `min-w-6`, tabular-nums.
- **`Tag`** — labeled chip, variants: `default`, `accent`, `ok`, `muted`, `more`. Monospace, 2xs. Use for identifiers, enum values, small stateful flags.
- **`TagList`** — `flex flex-wrap gap-1` container. Always wrap Tags in this.
- **`StateText`** — status-colored inline text for empty-state and loading messages. `dashed` (default true) gives a dashed border, `compact` switches to tight vertical padding. Use this instead of inventing bespoke "No data yet" blocks.

### Filtering
- **`ColumnFilter`** — single-select header filter dropdown (positioned via floating-ui). Trigger styling is the shared uppercase `text-2xs` filter button; active value renders as an accent-faint monospace chip with an inline clear button.
- **`MultiColumnFilter`** — multi-select variant of `ColumnFilter`.
- **`TimeRangeFilter`** — always-inline `[startAt, endAt]` time window (RFC3339 UTC strings). Renders two `Field`-wrapped native `datetime-local` inputs side by side (label on top, input below, seconds precision via `step="1"`) — no trigger, no floating panel. `v-model` is a `TimeRangeValue` (`{ startAt, endAt }`, both `''` when unset); inputs convert to/from local time on the fly and emit on every change (clearing a field emits `''`). An inline error shows when start > end or an external value is unparseable. Use for list-page created-at / last-request-at windows.

### Overlays / navigation

- **`Overlay`** — fullscreen `position: fixed` scrim with `bg-overlay-bg`, optional `backdrop-blur-[4px]`. Teleported to `<body>`; click-outside emits `click` on the backdrop only.
- **`SidePanel`** — the slide-over shell used by `useSidePanel`. Takes `title`, optional `kicker` and `subtitle`. Named slots: default (body), `footer` (action row), `error` (bottom alert strip with `err-faint` bg). Don't instantiate directly for routine flows — drive it through `useSidePanel` and register the content component.
- **`ConfirmDialog`** — driven by the `useConfirm` composable. Not instantiated ad-hoc; mount `<ConfirmDialog />` once (in `SidePanelHost` / `App.vue`) and call `confirm.require({ message, accept })`.
- **`Tabs`** — segmented-style tabs with optional per-tab icon. Compact (`text-xs`, tight padding). Use when the sub-navigation is small and mutually exclusive.

### Icons

- **`Icon`** — single component, resolves a name from `src/ui/icons/paths.ts` to a `@tabler/icons-vue` component. Defaults: `size=14`, `strokeWidth=1.8`. `aria-hidden`.
- To add an icon: import the Tabler component in `paths.ts`, extend the `IconName` union, add it to the `iconComponents` record. Never import Tabler icons directly in feature code — go through this registry so sizing and stroke stay consistent.

---

## 3. Conventions

- **Label uppercase kickers**: `text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]`. Applied via `Field`'s label; mirror manually in custom headers.
- **Table row density**: Th `py-2.5 px-4`, Td `py-3 px-4`, first/last cell extra `pl-[1.125rem]` / `pr-[1.125rem]` for optical edge padding.
- **Empty state**: use `StateText` inside the card, never a raw `<p>` with custom muted color.
- **Confirmation**: all destructive actions route through `useConfirm` → `ConfirmDialog`. Never `window.confirm`, never ad-hoc modals.
- **Side panels**: open via `useSidePanel` with a stable `key`. The key lets the sidebar/table mark the row `selected` via `panel.isActive(key)`. Follow the pattern in `ProvidersView.vue`.
- **i18n copy**: dashboard text is Simplified Chinese. When adding UI, match the existing tone — direct, no honorifics, verbs in imperative for actions (`删除`, `保存`).
- **Tabular / mono**: wherever a value is an identifier, a count, a byte size, or a timestamp, use `font-mono` or the `.tabular` helper so columns align.
- **Focus rings**: `focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-2` on buttons; `focus:ring-[3px] focus:ring-accent/20 focus:border-accent` on inputs. Don't invent a third pattern.
- **Never**: import a third-party Vue UI kit; introduce a variant-authoring DSL (cva/tv); hard-code an OKLCH value in a component; use pure `#000` / `#fff`; reach for `transition-all` or non-color transitions on UI chrome.

---

## 4. Extending the system

1. **Needed once**: style with Tailwind utilities directly in the view/component. No primitive yet.
2. **Needed twice**: still inline, but start noticing the shape.
3. **Needed three+ times, or touches tokens**: extract a primitive into `src/ui/`, export from `src/ui/index.ts`, and document its props here.

When adding a token:
- Add it to `@theme` in `src/index.css` (light-theme value).
- Add the same key with theme-appropriate values to **every** `[data-theme="…"]` block. If you skip one, that theme falls back to the light value silently.
- Prefer deriving from existing tokens (`var(--color-accent-faint)`) over inventing a new raw OKLCH.

When touching colors, reference `reference/color-and-contrast.md` from the Impeccable skill for contrast checks — interactive text should stay ≥ 4.5:1 against its background across all four themes.

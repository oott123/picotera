# Dashboard Theme Expansion + Dark-Mode Prose Fix — Design

Date: 2026-06-06
Scope: `dashboard/` only (frontend). No backend / API / OpenAPI changes.

## 1. Problem

Two related problems in the PicoTera dashboard:

1. **Dark-mode text is unreadable in places.** In dark themes, parts of the
   rendered Markdown (the conversation, thinking blocks, and response reply in
   the request-detail views) stay dark-on-dark. The user reports "部分文字渲染
   还是深色,看不清" — specifically the conversation / response rendering.

2. **Not enough themes.** Only 4 themes ship today (Pico Light, Solarized
   Light, Solarized Dark, Tera Dark). The user wants Visual Studio Dark+/Light+
   plus a curated set of famous themes.

## 2. Root cause of the dark-mode bug

The conversation/response renderers wrap `renderMarkdown()` output in Tailwind's
`prose` class (`@tailwindcss/typography`):

- `dashboard/src/components/ConversationView.vue:165` — text part
- `dashboard/src/components/ConversationView.vue:185` — thinking part
- `dashboard/src/components/ResponseArtifactView.vue:281` — thinking
- `dashboard/src/components/ResponseArtifactView.vue:285` — reply

The typography plugin colors content through its own `--tw-prose-*` CSS custom
properties, which default to a **fixed light-mode gray scale** and are never
overridden. The container's `text-ink` only sets the top-level body color; child
elements — headings, `<strong>`, inline `<code>`, links, blockquotes, list
markers, `<hr>`, table borders — are colored by `prose :where(...)` rules using
`--tw-prose-headings` / `--tw-prose-bold` / `--tw-prose-code` / etc., which stay
dark gray in every theme. On a dark surface these become invisible. That is
exactly the "part of the text is still dark" symptom.

### Secondary fragility

`dashboard/src/ui/CodeEditor.vue:46-49` detects dark mode by substring-matching
`document.documentElement.getAttribute('data-theme').includes('dark')`. This is
brittle: new dark theme slugs like `dracula`, `nord`, `monokai`, `tokyo-night`,
`catppuccin-mocha`, `gruvbox-dark` do **not** contain the substring "dark", so
the editor would apply the *light* syntax-highlight style on a dark background.
Meanwhile `dashboard/src/stores/preferences.ts:72` already maintains a correct
`data-dark` attribute on `<html>` that nothing reads.

## 3. Chosen approach

Convert "add 13 themes" from "edit 4 places per theme" into "add 1 registry
entry + 1 CSS block per theme", and fix the dark-mode bug at its source.

Three pieces:

### 3a. Fix prose once, for all themes

Add a single rule in `dashboard/src/index.css` mapping the typography plugin's
variables to existing semantic tokens. Because the tokens themselves swap per
theme (via `:root[data-theme='X']`), this one block makes **every** prose render
site follow **every** theme automatically:

```css
/* Prose (markdown) — map typography plugin vars to semantic tokens so rendered
   Markdown follows the active theme instead of the plugin's fixed gray scale. */
.prose {
  --tw-prose-body: var(--color-ink);
  --tw-prose-headings: var(--color-ink);
  --tw-prose-lead: var(--color-ink-muted);
  --tw-prose-links: var(--color-accent-ink);
  --tw-prose-bold: var(--color-ink);
  --tw-prose-counters: var(--color-ink-muted);
  --tw-prose-bullets: var(--color-ink-faint);
  --tw-prose-hr: var(--color-line);
  --tw-prose-quotes: var(--color-ink-muted);
  --tw-prose-quote-borders: var(--color-line);
  --tw-prose-captions: var(--color-ink-faint);
  --tw-prose-code: var(--color-ink);
  --tw-prose-pre-code: var(--color-ink);
  --tw-prose-pre-bg: var(--color-surface-100);
  --tw-prose-th-borders: var(--color-line);
  --tw-prose-td-borders: var(--color-line-soft);
}
```

`--tw-prose-kbd-shadows` is left at the plugin default (it expects an `R G B`
triple, not an OKLCH var; `<kbd>` essentially never appears in LLM output). The
existing `text-ink` on the prose containers stays (harmless, now redundant); the
two `ResponseArtifactView` prose blocks that lack `text-ink` are fixed by this
mapping regardless.

### 3b. Drive dark detection from `data-dark`

`CodeEditor.vue` reads `document.documentElement.dataset.dark === 'true'` and its
`MutationObserver` watches `data-dark` instead of `data-theme`. No substring
matching. (CodeMirror syntax colors stay `oneDark` for dark / `defaultHighlight`
for light — per-theme syntax palettes are out of scope; the editor is only used
in `ScriptForm` for JS, never in conversation rendering.)

### 3c. Single source of truth for themes

New module `dashboard/src/stores/themes.ts`:

```ts
export type ThemeKind = 'light' | 'dark'
export interface ThemeDef {
  value: string   // data-theme slug
  label: string   // display name in the menu
  kind: ThemeKind // drives data-dark
  surface: string // swatch left half (oklch literal)
  accent: string  // swatch right half (oklch literal)
}
export const THEMES = [ /* …17 entries… */ ] as const satisfies readonly ThemeDef[]
export type Theme = (typeof THEMES)[number]['value']        // strict union of slugs
export const THEME_VALUES = THEMES.map((t) => t.value) as Theme[]
export function isDarkTheme(value: Theme): boolean {
  return THEMES.find((t) => t.value === value)?.kind === 'dark'
}
```

- `preferences.ts` imports `Theme`, `THEME_VALUES`, `isDarkTheme` from this
  module (drops its local `Theme` type + `THEME_VALUES` literal). `apply()` sets
  `root.dataset.dark = String(isDarkTheme(theme.value))`. Everything else in the
  store (panel mode, font size, currency, persistence) is unchanged.
- `PreferencesMenu.vue` imports `THEMES`, drops its local `themes` array, and
  renders the list grouped into light / dark sub-sections.

Rationale over alternatives: appending to 3 separate places (type, values,
`data-dark` conditional, menu array) is what DESIGN_SYSTEM explicitly warns about
("If you skip one, that theme falls back to the light value silently"); adding 13
themes amplifies that risk. `prose-invert` per component only handles a binary
dark/light split, uses the plugin's own gray scale rather than our ink tokens,
and would leave light-but-non-default themes (Gruvbox Light, Catppuccin Latte)
un-themed.

## 4. Theme list (17 total)

Existing (4): `light` (Pico Light), `solarized-light`, `solarized-dark`,
`dark` (Tera Dark).

New (13):

| slug | label | kind | signature surface | signature accent |
| --- | --- | --- | --- | --- |
| `vs-light` | Visual Studio Light | light | `#ffffff` / sidebar `#f3f3f3` | `#005FB8` / `#0078D4` |
| `vs-dark` | Visual Studio Dark | dark | `#1e1e1e` / sidebar `#252526` | `#0078D4` / `#569CD6` |
| `github-light` | GitHub Light | light | `#ffffff` / subtle `#f6f8fa` | `#0969da` |
| `github-dark` | GitHub Dark | dark | `#0d1117` / sidebar `#161b22` | `#2f81f7` / `#58a6ff` |
| `dracula` | Dracula | dark | `#282a36` / line `#44475a` | `#bd93f9` |
| `nord` | Nord | dark | `#2e3440` / `#3b4252` | `#88c0d0` / `#81a1c1` |
| `tokyo-night` | Tokyo Night | dark | `#1a1b26` / `#16161e` | `#7aa2f7` |
| `one-dark` | One Dark | dark | `#282c34` | `#61afef` |
| `monokai` | Monokai | dark | `#272822` | `#66d9ef` (blue), ok `#a6e22e`, err `#f92672`, warn `#fd971f` |
| `catppuccin-latte` | Catppuccin Latte | light | `#eff1f5` / mantle `#e6e9ef` | `#8839ef` (mauve) |
| `catppuccin-mocha` | Catppuccin Mocha | dark | `#1e1e2e` / mantle `#181825` | `#cba6f7` (mauve) |
| `gruvbox-light` | Gruvbox Light | light | `#fbf1c7` | `#076678` (blue), warn `#d79921`, err `#9d0006`, ok `#79740e` |
| `gruvbox-dark` | Gruvbox Dark | dark | `#282828` / `#1d2021` | `#fe8019` (orange), ok `#b8bb26`, err `#fb4934`, warn `#fabd2f` |

Order in the registry (and menu groups): lights first
(`light`, `solarized-light`, `vs-light`, `github-light`, `catppuccin-latte`,
`gruvbox-light`), then darks
(`dark`, `solarized-dark`, `vs-dark`, `github-dark`, `dracula`, `nord`,
`tokyo-night`, `one-dark`, `monokai`, `catppuccin-mocha`, `gruvbox-dark`).

### Per-theme token construction

Each new theme fills the full token set already used by the shipping themes:
`surface-0/50/100/200/300`, `ink/ink-muted/ink-faint`,
`accent/accent-strong/accent-faint/accent-ink`, `ok/ok-ink/ok-faint`,
`warn/warn-ink/warn-faint`, `err/err-ink/err-faint`, `line/line-soft`,
`sidebar-*` (bg/text/text-active/hover/active-bg/active-text), `overlay-bg`,
`chart-0..9`. All values authored in OKLCH (no raw hex in CSS), matching the
existing blocks.

**Contrast guarantee by construction:** author each new theme by taking the
lightness/structure skeleton of the closest shipping same-kind theme (dark →
mirror `dark`'s L/C relationships; light → mirror `light` / `solarized-light`)
and re-tuning hue/chroma toward the target palette's signature. This keeps
`ink` vs `surface-0` and `accent-ink` vs `accent-faint` contrast on par with
already-shipped themes (target ≥ 4.5:1 for interactive text, per DESIGN_SYSTEM).
Signature hexes above are converted to OKLCH and used for the key surface/accent
roles; supporting roles are derived to preserve contrast.

## 5. PreferencesMenu layout

17 themes is too tall for a flat list in the `w-60` popover. Keep the existing
row design (split-circle swatch + label + selected dot) unchanged, but:

- Split into two labeled sub-sections, "浅色" and "深色", using the existing
  `text-2xs font-medium uppercase tracking-[0.06em] text-ink-faint` kicker style
  already used by the menu's section headers.
- Wrap the theme list in a `max-h-[…] overflow-y-auto` scroll container so the
  popover never exceeds the viewport. Other menu sections (panel position, font
  size, currency) are unchanged.

Swatch `surface`/`accent` come from each registry entry (`ThemeDef.surface` /
`.accent`).

## 6. File changes

| File | Change |
| --- | --- |
| `dashboard/src/stores/themes.ts` | **New.** `THEMES` registry + derived `Theme`, `THEME_VALUES`, `isDarkTheme`. |
| `dashboard/src/stores/preferences.ts` | Import theme types/values/`isDarkTheme` from the registry; `data-dark` via `isDarkTheme()`. Remove local `Theme` type + `THEME_VALUES`. |
| `dashboard/src/index.css` | Add `.prose` → `--tw-prose-*` token mapping; add 13 `:root[data-theme='…']` blocks. |
| `dashboard/src/components/PreferencesMenu.vue` | Remove local `themes` array; read `THEMES`; render light/dark groups in a scroll container. |
| `dashboard/src/ui/CodeEditor.vue` | Dark detection reads `data-dark`; observer watches `data-dark`. |

No other components reference the theme list or hardcode prose colors (verified by
sweep: `text-ink`/token usage is otherwise disciplined; only the 4 prose sites and
the CodeEditor dark check are affected).

## 7. Verification plan (live browser)

Per project memory (`local-run-verify-recipe`, `browser-verification-visible`):

1. **Infra:** `sudo docker compose up -d` (TimescaleDB :34052, KeyDB :34051,
   MinIO :34050) from the main repo.
2. **Backend from the MAIN repo** (not the worktree — `third_party/axonhub`
   submodule is absent in worktrees): build the llmbridge plugin, then
   `PICOTERA_DATABASE_URL=… PICOTERA_LLMBRIDGE_PLUGIN_PATH=… go run ./cmd/picotera/main.go`
   (serves :9898).
3. **Dashboard dev server from the worktree** (to exercise the changed code):
   `pnpm install` (workspace root) then `pnpm --dir dashboard dev` (:5173,
   proxies `/api` + `/v1` to :9898).
4. **Seed conversation/response data** by inserting rows directly into the
   `request` hypertable (`type=0, status=2`, supply `id` + `created_at`) so the
   request-detail views render conversation / thinking / response Markdown for
   eyeballing. Clean up with `DELETE FROM request WHERE id LIKE 'demo-%'`.
5. **Visible (non-headless) browser** on WSLg so the user can watch live; cycle
   through all 17 themes and confirm readability — focus on dark themes'
   conversation, thinking, response, inline code, and code blocks. Capture
   screenshots for the dark themes.

Build/type checks before commit: `pnpm --dir dashboard type-check` and
`pnpm --dir dashboard lint`.

## 8. Commit conventions

Per `picotera-commit-conventions`: single-line signed commits
`<type>: <desc>`, no `Co-Authored-By`. GPG signing is required and cannot be done
non-interactively by the agent — if a commit fails with a passphrase/tty error,
ask the user to unlock their gpg-agent or run the commit via the `!` prefix.
Work happens on the `worktree-dashboard-themes` branch (master stays clean).

## 9. Out of scope

- Per-theme CodeMirror syntax palettes (editor keeps oneDark/default).
- `prefers-color-scheme` auto theme; system-follow mode.
- Any backend, API, OpenAPI, or non-dashboard change.
- Restyling `JsonArtifactViewer` beyond verifying its token-mapped colors read
  correctly on dark themes (it already maps all `--jse-*` vars to tokens; only
  add a `[data-dark]`-scoped `--jse-theme: dark` if a visual artifact appears).
</content>
</invoke>

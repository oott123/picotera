# Dashboard Theme Expansion + Dark-Mode Prose Fix — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the dark-mode "part of the text stays dark" bug in rendered Markdown and ship 13 new famous themes, all on the existing token system.

**Architecture:** The dashboard themes are semantic OKLCH custom properties (`--color-*`) declared once in `@theme` (light default) and re-assigned per `:root[data-theme='X']` block in `dashboard/src/index.css`; `stores/preferences.ts` toggles `data-theme` / `data-dark` on `<html>`. This plan (1) maps the Tailwind typography plugin's `--tw-prose-*` variables onto those tokens so Markdown follows the active theme, (2) centralizes the theme list into one registry module that drives the type, the `data-dark` flag, and the menu, and (3) adds 13 `data-theme` blocks + registry entries.

**Tech Stack:** Vue 3 + Tailwind CSS v4 (`@tailwindcss/typography`) + Pinia + TypeScript; CodeMirror 6 (`CodeEditor`); no frontend unit-test runner (verification = `type-check` + `lint` + live browser).

**Spec:** `docs/superpowers/specs/2026-06-06-dashboard-themes-design.md`

**Verification note:** This is a perceptual/CSS feature with no frontend test runner. Per-task "Verify" runs `pnpm --dir dashboard type-check` and `pnpm --dir dashboard lint` (both must pass clean) to catch code/TS errors; visual correctness is confirmed in the live browser in Task 8 (the user watches). Run all `pnpm` commands from the worktree root.

---

### Task 1: Prose follows theme tokens (the dark-mode bug fix)

**Goal:** Markdown rendered inside `.prose` (conversation text, thinking, response reply, tool output) uses the active theme's ink/accent/line tokens instead of the typography plugin's fixed gray scale, so nothing stays dark-on-dark.

**Files:**
- Modify: `dashboard/src/index.css` (add a `.prose` token-mapping block after the `:root[data-theme='dark']` block, before the `html {` rule near line 230)

**Acceptance Criteria:**
- [ ] A `.prose { … }` rule sets every listed `--tw-prose-*` variable to a `var(--color-*)` token.
- [ ] No component markup changes are needed (the four prose sites in `ConversationView.vue` / `ResponseArtifactView.vue` are untouched).
- [ ] `type-check` and `lint` pass.

**Verify:** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → both exit 0. Visual: in Task 8, Markdown headings/bold/inline-code/links/quotes/list-markers are readable in every dark theme.

**Steps:**

- [ ] **Step 1: Add the prose token mapping to `index.css`**

Insert this block immediately after the closing `}` of `:root[data-theme='dark']` (current line 228) and before `html {`:

```css
/* ---------- Prose (markdown) ----------
   The @tailwindcss/typography plugin colors content through its own
   --tw-prose-* variables, which default to a fixed light gray scale and do not
   follow our themes. Re-map them onto the semantic tokens so every .prose block
   (conversation text, thinking, response reply, tool output) tracks the active
   theme — fixing dark-on-dark text in dark themes. Tokens already swap per
   [data-theme], so this single block covers all themes. */
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

- [ ] **Step 2: Verify build**

Run: `pnpm --dir dashboard type-check && pnpm --dir dashboard lint`
Expected: both exit 0, no errors.

- [ ] **Step 3: Commit**

```bash
git add dashboard/src/index.css
git commit -m "fix: prose markdown follows theme tokens in dark mode"
```

---

### Task 2: Centralize the theme registry and wire all consumers

**Goal:** A single `themes.ts` registry is the only place a theme is declared; `preferences.ts`, `PreferencesMenu.vue`, and `CodeEditor.vue` consume it. No new themes yet — pure refactor of the existing 4, plus the `data-dark`-based dark detection and the light/dark-grouped menu. This unblocks adding themes by one registry entry each.

**Files:**
- Create: `dashboard/src/stores/themes.ts`
- Modify: `dashboard/src/stores/preferences.ts` (drop local `Theme` type + `THEME_VALUES`; import from registry; `data-dark` via `isDarkTheme`)
- Modify: `dashboard/src/components/PreferencesMenu.vue` (drop local `themes` array; read registry; render light/dark groups in a scroll container)
- Modify: `dashboard/src/ui/CodeEditor.vue` (dark detection reads `data-dark`; observer watches `data-dark`)

**Acceptance Criteria:**
- [ ] `themes.ts` exports `THEMES` (the 4 existing themes), and derived `Theme` (a strict union of slugs), `THEME_VALUES`, `isDarkTheme`.
- [ ] `preferences.ts` no longer declares its own `Theme` union or `THEME_VALUES`; re-exports `Theme` from the registry so existing `import { Theme } from '@/stores/preferences'` keeps working; `apply()` sets `data-dark` via `isDarkTheme(theme.value)`.
- [ ] `PreferencesMenu.vue` renders two labeled groups ("浅色" / "深色") from the registry inside a `max-h`/`overflow-y-auto` container; swatches come from `ThemeDef.surface` / `.accent`.
- [ ] `CodeEditor.vue` `isDark()` returns `document.documentElement.dataset.dark === 'true'`; the `MutationObserver` filters `['data-dark']`.
- [ ] All 4 existing themes still apply correctly; `type-check` and `lint` pass.

**Verify:** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → both exit 0.

**Steps:**

- [ ] **Step 1: Create `dashboard/src/stores/themes.ts`**

```ts
export type ThemeKind = 'light' | 'dark'

export interface ThemeDef {
  /** data-theme slug applied to <html> */
  value: string
  /** display name in the preferences menu */
  label: string
  /** drives the data-dark flag and the menu grouping */
  kind: ThemeKind
  /** left half of the menu swatch (oklch literal) */
  surface: string
  /** right half of the menu swatch (oklch literal) */
  accent: string
}

// Single source of truth. Lights first, then darks; menu groups filter by kind
// and preserve this order. Adding a theme = one entry here + one
// :root[data-theme='…'] block in index.css.
export const THEMES = [
  {
    value: 'light',
    label: 'Pico Light',
    kind: 'light',
    surface: 'oklch(0.986 0.003 250)',
    accent: 'oklch(0.54 0.19 262)',
  },
  {
    value: 'solarized-light',
    label: 'Solarized Light',
    kind: 'light',
    surface: 'oklch(0.965 0.036 92)',
    accent: 'oklch(0.72 0.15 85)',
  },
  {
    value: 'dark',
    label: 'Tera Dark',
    kind: 'dark',
    surface: 'oklch(0.22 0.02 255)',
    accent: 'oklch(0.70 0.18 262)',
  },
  {
    value: 'solarized-dark',
    label: 'Solarized Dark',
    kind: 'dark',
    surface: 'oklch(0.30 0.035 210)',
    accent: 'oklch(0.68 0.14 235)',
  },
] as const satisfies readonly ThemeDef[]

export type Theme = (typeof THEMES)[number]['value']

export const THEME_VALUES = THEMES.map((t) => t.value) as Theme[]

export function isDarkTheme(value: Theme): boolean {
  return THEMES.find((t) => t.value === value)?.kind === 'dark'
}
```

- [ ] **Step 2: Rewire `preferences.ts`**

Replace the local `Theme` type (line 4) and the `THEME_VALUES` constant (line 19) with imports, and re-export `Theme` for existing consumers. Concretely:

Change the top of the file from:
```ts
import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

export type Theme = 'light' | 'solarized-light' | 'solarized-dark' | 'dark'
export type PanelMode = 'auto' | 'right' | 'modal'
```
to:
```ts
import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { THEME_VALUES, isDarkTheme } from './themes'
import type { Theme } from './themes'

export type { Theme }
export type PanelMode = 'auto' | 'right' | 'modal'
```

Delete the now-duplicate declaration line:
```ts
const THEME_VALUES: Theme[] = ['light', 'solarized-light', 'solarized-dark', 'dark']
```

In `apply()`, change the `data-dark` line from:
```ts
    root.dataset.dark = String(theme.value === 'dark' || theme.value === 'solarized-dark')
```
to:
```ts
    root.dataset.dark = String(isDarkTheme(theme.value))
```

(The `DEFAULTS.theme`, `load()` validation via `THEME_VALUES.includes(...)`, and everything else stay as-is.)

- [ ] **Step 3: Rewire `PreferencesMenu.vue` script**

Remove the local `ThemeOption` type and `themes` array (lines 68–94) and import the registry instead. In `<script setup>`, replace:
```ts
import type { Theme, PanelMode, FontSize } from '@/stores/preferences'
```
with:
```ts
import type { PanelMode, FontSize } from '@/stores/preferences'
import { THEMES } from '@/stores/themes'
import type { Theme } from '@/stores/themes'
```
Delete the `type ThemeOption = …` line and the whole `const themes: ThemeOption[] = [ … ]` array. Add derived groups after the imports:
```ts
const lightThemes = THEMES.filter((t) => t.kind === 'light')
const darkThemes = THEMES.filter((t) => t.kind === 'dark')
```
(`setTheme(t: Theme)` stays; `t.value` is now the union type.)

- [ ] **Step 4: Rewire `PreferencesMenu.vue` template (外观 section)**

Replace the single-list 外观 `<section>` (the `<section class="px-1 pt-1.5 pb-2">` containing the `<h3>外观</h3>` and its `<ul>`) with a grouped, scrollable version. Extract the row button into a reusable `<template>`-loop over each group so the markup stays DRY:

```html
      <section class="px-1 pt-1.5 pb-2">
        <div class="max-h-72 overflow-y-auto pr-0.5">
          <template v-for="group in [
            { key: 'light', label: '浅色', items: lightThemes },
            { key: 'dark', label: '深色', items: darkThemes },
          ]" :key="group.key">
            <h3
              class="m-0 mb-2 px-1.5 text-2xs font-medium tracking-[0.06em] uppercase text-ink-faint"
              :class="group.key === 'dark' ? 'mt-2' : ''"
            >
              {{ group.label }}
            </h3>
            <ul
              class="list-none p-0 m-0 mb-1 flex flex-col gap-px"
              role="radiogroup"
              :aria-label="`外观主题 - ${group.label}`"
            >
              <li v-for="t in group.items" :key="t.value">
                <button
                  type="button"
                  role="radio"
                  :aria-checked="prefs.theme === t.value"
                  class="grid grid-cols-[auto_1fr_auto] items-center gap-2.5 w-full px-2 py-1.5 bg-transparent border border-transparent rounded-md text-sm text-left cursor-pointer transition-colors hover:bg-surface-50"
                  :class="
                    prefs.theme === t.value
                      ? 'bg-accent-faint border-accent/25 text-accent-ink font-medium'
                      : ''
                  "
                  @click="setTheme(t.value)"
                >
                  <span
                    class="relative inline-block w-5 h-5 rounded-full flex-none"
                    :style="{
                      background: `linear-gradient(90deg, ${t.surface} 0 50%, ${t.accent} 50% 100%)`,
                      boxShadow:
                        prefs.theme === t.value
                          ? 'inset 0 0 0 1px color-mix(in oklch, var(--color-ink) 22%, transparent), 0 0 0 2px var(--color-surface-0), 0 0 0 3px var(--color-accent)'
                          : 'inset 0 0 0 1px color-mix(in oklch, var(--color-ink) 18%, transparent)',
                    }"
                    aria-hidden="true"
                  />
                  <span class="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">{{
                    t.label
                  }}</span>
                  <span
                    v-if="prefs.theme === t.value"
                    class="inline-block w-1.5 h-1.5 rounded-full bg-accent"
                    aria-hidden="true"
                  />
                </button>
              </li>
            </ul>
          </template>
        </div>
      </section>
```

(The 面板位置 / 字体大小 / 主要货币 sections below are unchanged.)

- [ ] **Step 5: Rewire `CodeEditor.vue` dark detection**

Replace `isDark()` (lines 46–49):
```ts
function isDark() {
  return document.documentElement.dataset.dark === 'true'
}
```
And change the observer's `attributeFilter` (line 139) from `['data-theme']` to `['data-dark']`:
```ts
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['data-dark'],
  })
```

- [ ] **Step 6: Verify build**

Run: `pnpm --dir dashboard type-check && pnpm --dir dashboard lint`
Expected: both exit 0.

- [ ] **Step 7: Commit**

```bash
git add dashboard/src/stores/themes.ts dashboard/src/stores/preferences.ts dashboard/src/components/PreferencesMenu.vue dashboard/src/ui/CodeEditor.vue
git commit -m "refactor: centralize theme registry as single source of truth"
```

---

## Tasks 3–7: add the 13 new themes

Each batch task does the same two mechanical edits and verifies the build:
1. Append the `:root[data-theme='…']` block(s) to `dashboard/src/index.css` (after the existing theme blocks, before `.prose`).
2. Insert the registry entry/entries into `THEMES` in `dashboard/src/stores/themes.ts` — **new lights after `solarized-light`, new darks after the existing dark run** so the file stays grouped lights-then-darks.

Every block fills the full token set used by the shipping themes. The OKLCH values below were authored by mirroring the lightness structure of the closest shipping same-kind theme (so interactive-text contrast stays on par, target ≥ 4.5:1); Task 8 tunes any that read off in the browser.

---

### Task 3: Visual Studio themes (Light + Dark)

**Goal:** Add `vs-light` and `vs-dark` (VS Code Light/Dark Modern) — the explicitly requested pair.

**Files:**
- Modify: `dashboard/src/index.css` (append two blocks)
- Modify: `dashboard/src/stores/themes.ts` (two entries)

**Acceptance Criteria:**
- [ ] Selecting "Visual Studio Light" / "Visual Studio Dark" applies neutral VS surfaces with the VS blue accent; conversation/response text readable.
- [ ] `type-check` + `lint` pass.

**Verify:** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.

**Steps:**

- [ ] **Step 1: Append CSS blocks to `index.css`**

```css
:root[data-theme='vs-light'] {
  --color-surface-0: oklch(1 0 0);
  --color-surface-50: oklch(0.985 0.001 250);
  --color-surface-100: oklch(0.965 0.002 250);
  --color-surface-200: oklch(0.94 0.003 250);
  --color-surface-300: oklch(0.9 0.004 250);

  --color-ink: oklch(0.27 0.005 260);
  --color-ink-muted: oklch(0.5 0.006 258);
  --color-ink-faint: oklch(0.62 0.007 258);

  --color-accent: oklch(0.5 0.16 250);
  --color-accent-strong: oklch(0.44 0.18 252);
  --color-accent-faint: oklch(0.94 0.03 245);
  --color-accent-ink: oklch(0.42 0.16 252);

  --color-ok: oklch(0.55 0.13 150);
  --color-ok-ink: oklch(0.44 0.12 150);
  --color-ok-faint: oklch(0.95 0.045 150);
  --color-warn: oklch(0.6 0.12 70);
  --color-warn-ink: oklch(0.47 0.11 65);
  --color-warn-faint: oklch(0.96 0.05 85);
  --color-err: oklch(0.55 0.2 25);
  --color-err-ink: oklch(0.47 0.19 25);
  --color-err-faint: oklch(0.96 0.035 25);

  --color-chart-0: oklch(0.5 0.16 250);
  --color-chart-1: oklch(0.52 0.13 150);
  --color-chart-2: oklch(0.62 0.12 70);
  --color-chart-3: oklch(0.52 0.18 25);
  --color-chart-4: oklch(0.55 0.1 195);
  --color-chart-5: oklch(0.5 0.18 320);
  --color-chart-6: oklch(0.55 0.13 350);
  --color-chart-7: oklch(0.52 0.12 130);
  --color-chart-8: oklch(0.48 0.15 235);
  --color-chart-9: oklch(0.55 0.13 55);

  --color-line: oklch(0.9 0.003 255);
  --color-line-soft: oklch(0.945 0.002 255);

  --color-sidebar-bg: oklch(0.965 0.002 250);
  --color-sidebar-text: oklch(0.42 0.006 258);
  --color-sidebar-text-active: oklch(0.27 0.005 260);
  --color-sidebar-hover: oklch(0.94 0.003 250);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.28 0.01 255 / 0.4);
}

:root[data-theme='vs-dark'] {
  --color-surface-0: oklch(0.26 0.004 250);
  --color-surface-50: oklch(0.23 0.004 250);
  --color-surface-100: oklch(0.3 0.005 250);
  --color-surface-200: oklch(0.35 0.006 250);
  --color-surface-300: oklch(0.44 0.007 250);

  --color-ink: oklch(0.93 0.004 250);
  --color-ink-muted: oklch(0.74 0.006 250);
  --color-ink-faint: oklch(0.58 0.008 250);

  --color-accent: oklch(0.62 0.15 245);
  --color-accent-strong: oklch(0.7 0.14 245);
  --color-accent-faint: oklch(0.34 0.08 245);
  --color-accent-ink: oklch(0.82 0.12 245);

  --color-ok: oklch(0.78 0.15 150);
  --color-ok-ink: oklch(0.88 0.13 150);
  --color-ok-faint: oklch(0.34 0.09 150);
  --color-warn: oklch(0.8 0.13 85);
  --color-warn-ink: oklch(0.9 0.11 85);
  --color-warn-faint: oklch(0.36 0.08 80);
  --color-err: oklch(0.66 0.18 28);
  --color-err-ink: oklch(0.85 0.13 28);
  --color-err-faint: oklch(0.34 0.1 28);

  --color-chart-0: oklch(0.7 0.14 245);
  --color-chart-1: oklch(0.7 0.12 150);
  --color-chart-2: oklch(0.78 0.1 75);
  --color-chart-3: oklch(0.7 0.14 35);
  --color-chart-4: oklch(0.75 0.11 180);
  --color-chart-5: oklch(0.68 0.14 330);
  --color-chart-6: oklch(0.82 0.1 230);
  --color-chart-7: oklch(0.66 0.18 28);
  --color-chart-8: oklch(0.72 0.12 270);
  --color-chart-9: oklch(0.74 0.12 110);

  --color-line: oklch(0.34 0.005 250);
  --color-line-soft: oklch(0.3 0.005 250);

  --color-sidebar-bg: oklch(0.24 0.004 250);
  --color-sidebar-text: oklch(0.74 0.006 250);
  --color-sidebar-text-active: oklch(0.95 0.004 250);
  --color-sidebar-hover: oklch(0.31 0.005 250);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.08 0.005 250 / 0.6);
}
```

- [ ] **Step 2: Add registry entries** to `THEMES` in `themes.ts` — `vs-light` after the `solarized-light` entry, `vs-dark` after the `solarized-dark` entry:

```ts
  // after solarized-light:
  {
    value: 'vs-light',
    label: 'Visual Studio Light',
    kind: 'light',
    surface: 'oklch(0.985 0.001 250)',
    accent: 'oklch(0.5 0.16 250)',
  },
```
```ts
  // after solarized-dark:
  {
    value: 'vs-dark',
    label: 'Visual Studio Dark',
    kind: 'dark',
    surface: 'oklch(0.26 0.004 250)',
    accent: 'oklch(0.62 0.15 245)',
  },
```

- [ ] **Step 3: Verify** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.
- [ ] **Step 4: Commit** `git add dashboard/src/index.css dashboard/src/stores/themes.ts && git commit -m "feat: add visual studio light and dark themes"`

---

### Task 4: GitHub themes (Light + Dark)

**Goal:** Add `github-light` and `github-dark`.

**Files:** Modify `dashboard/src/index.css` (two blocks), `dashboard/src/stores/themes.ts` (two entries).

**Acceptance Criteria:**
- [ ] GitHub canvas/border surfaces with GitHub blue accent; conversation/response readable.
- [ ] `type-check` + `lint` pass.

**Verify:** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.

**Steps:**

- [ ] **Step 1: Append CSS blocks**

```css
:root[data-theme='github-light'] {
  --color-surface-0: oklch(1 0 0);
  --color-surface-50: oklch(0.985 0.002 250);
  --color-surface-100: oklch(0.968 0.004 250);
  --color-surface-200: oklch(0.94 0.006 250);
  --color-surface-300: oklch(0.9 0.008 250);

  --color-ink: oklch(0.25 0.02 260);
  --color-ink-muted: oklch(0.5 0.02 258);
  --color-ink-faint: oklch(0.62 0.018 258);

  --color-accent: oklch(0.55 0.18 255);
  --color-accent-strong: oklch(0.48 0.2 256);
  --color-accent-faint: oklch(0.95 0.025 255);
  --color-accent-ink: oklch(0.45 0.17 256);

  --color-ok: oklch(0.58 0.14 150);
  --color-ok-ink: oklch(0.45 0.13 150);
  --color-ok-faint: oklch(0.95 0.045 150);
  --color-warn: oklch(0.62 0.13 75);
  --color-warn-ink: oklch(0.48 0.11 70);
  --color-warn-faint: oklch(0.96 0.05 85);
  --color-err: oklch(0.55 0.2 22);
  --color-err-ink: oklch(0.46 0.19 22);
  --color-err-faint: oklch(0.96 0.035 22);

  --color-chart-0: oklch(0.55 0.18 255);
  --color-chart-1: oklch(0.58 0.14 150);
  --color-chart-2: oklch(0.62 0.13 75);
  --color-chart-3: oklch(0.55 0.2 22);
  --color-chart-4: oklch(0.58 0.11 200);
  --color-chart-5: oklch(0.52 0.18 300);
  --color-chart-6: oklch(0.58 0.16 350);
  --color-chart-7: oklch(0.56 0.13 130);
  --color-chart-8: oklch(0.5 0.16 235);
  --color-chart-9: oklch(0.58 0.14 55);

  --color-line: oklch(0.89 0.006 255);
  --color-line-soft: oklch(0.93 0.005 255);

  --color-sidebar-bg: oklch(0.985 0.003 250);
  --color-sidebar-text: oklch(0.45 0.02 258);
  --color-sidebar-text-active: oklch(0.25 0.02 260);
  --color-sidebar-hover: oklch(0.955 0.005 250);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.28 0.02 255 / 0.42);
}

:root[data-theme='github-dark'] {
  --color-surface-0: oklch(0.2 0.015 260);
  --color-surface-50: oklch(0.17 0.015 265);
  --color-surface-100: oklch(0.24 0.016 260);
  --color-surface-200: oklch(0.3 0.018 258);
  --color-surface-300: oklch(0.4 0.02 256);

  --color-ink: oklch(0.93 0.012 250);
  --color-ink-muted: oklch(0.7 0.02 255);
  --color-ink-faint: oklch(0.56 0.022 258);

  --color-accent: oklch(0.65 0.17 255);
  --color-accent-strong: oklch(0.72 0.16 255);
  --color-accent-faint: oklch(0.32 0.1 258);
  --color-accent-ink: oklch(0.8 0.13 255);

  --color-ok: oklch(0.72 0.17 150);
  --color-ok-ink: oklch(0.85 0.14 150);
  --color-ok-faint: oklch(0.32 0.1 150);
  --color-warn: oklch(0.78 0.14 85);
  --color-warn-ink: oklch(0.88 0.12 85);
  --color-warn-faint: oklch(0.34 0.08 80);
  --color-err: oklch(0.66 0.2 25);
  --color-err-ink: oklch(0.84 0.14 25);
  --color-err-faint: oklch(0.33 0.1 25);

  --color-chart-0: oklch(0.65 0.17 255);
  --color-chart-1: oklch(0.72 0.17 150);
  --color-chart-2: oklch(0.78 0.14 85);
  --color-chart-3: oklch(0.66 0.2 25);
  --color-chart-4: oklch(0.72 0.13 200);
  --color-chart-5: oklch(0.68 0.16 300);
  --color-chart-6: oklch(0.72 0.15 350);
  --color-chart-7: oklch(0.7 0.13 130);
  --color-chart-8: oklch(0.66 0.15 235);
  --color-chart-9: oklch(0.74 0.14 60);

  --color-line: oklch(0.34 0.016 260);
  --color-line-soft: oklch(0.28 0.016 262);

  --color-sidebar-bg: oklch(0.18 0.015 265);
  --color-sidebar-text: oklch(0.7 0.02 255);
  --color-sidebar-text-active: oklch(0.94 0.012 250);
  --color-sidebar-hover: oklch(0.24 0.016 260);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.05 0.015 265 / 0.65);
}
```

- [ ] **Step 2: Add registry entries** — `github-light` after `vs-light`, `github-dark` after `vs-dark`:

```ts
  {
    value: 'github-light',
    label: 'GitHub Light',
    kind: 'light',
    surface: 'oklch(0.985 0.002 250)',
    accent: 'oklch(0.55 0.18 255)',
  },
```
```ts
  {
    value: 'github-dark',
    label: 'GitHub Dark',
    kind: 'dark',
    surface: 'oklch(0.2 0.015 260)',
    accent: 'oklch(0.65 0.17 255)',
  },
```

- [ ] **Step 3: Verify** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.
- [ ] **Step 4: Commit** `git add dashboard/src/index.css dashboard/src/stores/themes.ts && git commit -m "feat: add github light and dark themes"`

---

### Task 5: Dracula + Nord

**Goal:** Add the two iconic standalone dark themes `dracula` and `nord`.

**Files:** Modify `dashboard/src/index.css` (two blocks), `dashboard/src/stores/themes.ts` (two entries).

**Acceptance Criteria:**
- [ ] Dracula (purple accent on `#282a36`) and Nord (frost cyan on `#2e3440`) apply; conversation/response readable.
- [ ] `type-check` + `lint` pass.

**Verify:** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.

**Steps:**

- [ ] **Step 1: Append CSS blocks**

```css
:root[data-theme='dracula'] {
  --color-surface-0: oklch(0.31 0.028 285);
  --color-surface-50: oklch(0.28 0.028 285);
  --color-surface-100: oklch(0.35 0.03 283);
  --color-surface-200: oklch(0.41 0.032 280);
  --color-surface-300: oklch(0.49 0.035 278);

  --color-ink: oklch(0.95 0.012 95);
  --color-ink-muted: oklch(0.76 0.035 280);
  --color-ink-faint: oklch(0.6 0.05 278);

  --color-accent: oklch(0.74 0.16 300);
  --color-accent-strong: oklch(0.81 0.15 300);
  --color-accent-faint: oklch(0.37 0.09 300);
  --color-accent-ink: oklch(0.85 0.13 300);

  --color-ok: oklch(0.82 0.19 150);
  --color-ok-ink: oklch(0.88 0.16 150);
  --color-ok-faint: oklch(0.36 0.1 150);
  --color-warn: oklch(0.8 0.13 70);
  --color-warn-ink: oklch(0.88 0.11 72);
  --color-warn-faint: oklch(0.37 0.07 65);
  --color-err: oklch(0.68 0.2 22);
  --color-err-ink: oklch(0.85 0.14 22);
  --color-err-faint: oklch(0.36 0.1 22);

  --color-chart-0: oklch(0.74 0.16 300);
  --color-chart-1: oklch(0.82 0.19 150);
  --color-chart-2: oklch(0.83 0.13 200);
  --color-chart-3: oklch(0.74 0.17 350);
  --color-chart-4: oklch(0.8 0.13 65);
  --color-chart-5: oklch(0.92 0.16 110);
  --color-chart-6: oklch(0.68 0.2 22);
  --color-chart-7: oklch(0.7 0.1 275);
  --color-chart-8: oklch(0.78 0.15 320);
  --color-chart-9: oklch(0.8 0.16 170);

  --color-line: oklch(0.39 0.03 285);
  --color-line-soft: oklch(0.34 0.03 285);

  --color-sidebar-bg: oklch(0.28 0.028 285);
  --color-sidebar-text: oklch(0.74 0.035 280);
  --color-sidebar-text-active: oklch(0.95 0.012 95);
  --color-sidebar-hover: oklch(0.35 0.03 285);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.12 0.03 285 / 0.6);
}

:root[data-theme='nord'] {
  --color-surface-0: oklch(0.33 0.022 250);
  --color-surface-50: oklch(0.3 0.022 250);
  --color-surface-100: oklch(0.37 0.024 248);
  --color-surface-200: oklch(0.43 0.026 246);
  --color-surface-300: oklch(0.5 0.028 244);

  --color-ink: oklch(0.93 0.012 230);
  --color-ink-muted: oklch(0.8 0.018 235);
  --color-ink-faint: oklch(0.66 0.025 240);

  --color-accent: oklch(0.78 0.08 210);
  --color-accent-strong: oklch(0.72 0.09 235);
  --color-accent-faint: oklch(0.4 0.05 225);
  --color-accent-ink: oklch(0.84 0.07 210);

  --color-ok: oklch(0.78 0.1 140);
  --color-ok-ink: oklch(0.86 0.09 140);
  --color-ok-faint: oklch(0.4 0.06 140);
  --color-warn: oklch(0.84 0.1 80);
  --color-warn-ink: oklch(0.9 0.08 80);
  --color-warn-faint: oklch(0.42 0.06 75);
  --color-err: oklch(0.66 0.13 20);
  --color-err-ink: oklch(0.8 0.11 20);
  --color-err-faint: oklch(0.4 0.08 20);

  --color-chart-0: oklch(0.78 0.08 210);
  --color-chart-1: oklch(0.78 0.1 140);
  --color-chart-2: oklch(0.84 0.1 80);
  --color-chart-3: oklch(0.66 0.13 20);
  --color-chart-4: oklch(0.72 0.09 235);
  --color-chart-5: oklch(0.7 0.08 330);
  --color-chart-6: oklch(0.74 0.1 45);
  --color-chart-7: oklch(0.76 0.07 190);
  --color-chart-8: oklch(0.62 0.08 250);
  --color-chart-9: oklch(0.8 0.09 110);

  --color-line: oklch(0.4 0.022 248);
  --color-line-soft: oklch(0.36 0.022 248);

  --color-sidebar-bg: oklch(0.3 0.022 250);
  --color-sidebar-text: oklch(0.78 0.018 235);
  --color-sidebar-text-active: oklch(0.94 0.012 230);
  --color-sidebar-hover: oklch(0.37 0.024 248);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.16 0.02 250 / 0.6);
}
```

- [ ] **Step 2: Add registry entries** (both dark, after `github-dark`):

```ts
  {
    value: 'dracula',
    label: 'Dracula',
    kind: 'dark',
    surface: 'oklch(0.31 0.028 285)',
    accent: 'oklch(0.74 0.16 300)',
  },
  {
    value: 'nord',
    label: 'Nord',
    kind: 'dark',
    surface: 'oklch(0.33 0.022 250)',
    accent: 'oklch(0.78 0.08 210)',
  },
```

- [ ] **Step 3: Verify** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.
- [ ] **Step 4: Commit** `git add dashboard/src/index.css dashboard/src/stores/themes.ts && git commit -m "feat: add dracula and nord themes"`

---

### Task 6: Tokyo Night + One Dark + Monokai

**Goal:** Add three more popular dark themes: `tokyo-night`, `one-dark`, `monokai`.

**Files:** Modify `dashboard/src/index.css` (three blocks), `dashboard/src/stores/themes.ts` (three entries).

**Acceptance Criteria:**
- [ ] All three apply with their signature surfaces/accents; conversation/response readable.
- [ ] `type-check` + `lint` pass.

**Verify:** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.

**Steps:**

- [ ] **Step 1: Append CSS blocks**

```css
:root[data-theme='tokyo-night'] {
  --color-surface-0: oklch(0.24 0.025 270);
  --color-surface-50: oklch(0.21 0.025 272);
  --color-surface-100: oklch(0.28 0.028 268);
  --color-surface-200: oklch(0.34 0.03 266);
  --color-surface-300: oklch(0.44 0.032 264);

  --color-ink: oklch(0.86 0.04 270);
  --color-ink-muted: oklch(0.7 0.05 268);
  --color-ink-faint: oklch(0.56 0.06 266);

  --color-accent: oklch(0.7 0.15 265);
  --color-accent-strong: oklch(0.78 0.14 265);
  --color-accent-faint: oklch(0.34 0.09 265);
  --color-accent-ink: oklch(0.82 0.12 265);

  --color-ok: oklch(0.8 0.16 140);
  --color-ok-ink: oklch(0.88 0.13 140);
  --color-ok-faint: oklch(0.34 0.09 140);
  --color-warn: oklch(0.8 0.12 75);
  --color-warn-ink: oklch(0.88 0.1 75);
  --color-warn-faint: oklch(0.36 0.07 70);
  --color-err: oklch(0.7 0.16 0);
  --color-err-ink: oklch(0.84 0.12 0);
  --color-err-faint: oklch(0.36 0.1 0);

  --color-chart-0: oklch(0.7 0.15 265);
  --color-chart-1: oklch(0.8 0.16 140);
  --color-chart-2: oklch(0.8 0.12 75);
  --color-chart-3: oklch(0.7 0.16 0);
  --color-chart-4: oklch(0.82 0.12 220);
  --color-chart-5: oklch(0.74 0.14 300);
  --color-chart-6: oklch(0.74 0.13 340);
  --color-chart-7: oklch(0.78 0.12 175);
  --color-chart-8: oklch(0.66 0.14 250);
  --color-chart-9: oklch(0.8 0.13 50);

  --color-line: oklch(0.34 0.028 268);
  --color-line-soft: oklch(0.29 0.028 270);

  --color-sidebar-bg: oklch(0.21 0.025 272);
  --color-sidebar-text: oklch(0.7 0.05 268);
  --color-sidebar-text-active: oklch(0.9 0.04 270);
  --color-sidebar-hover: oklch(0.28 0.028 268);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.1 0.02 272 / 0.62);
}

:root[data-theme='one-dark'] {
  --color-surface-0: oklch(0.31 0.012 265);
  --color-surface-50: oklch(0.28 0.012 265);
  --color-surface-100: oklch(0.36 0.013 263);
  --color-surface-200: oklch(0.42 0.014 261);
  --color-surface-300: oklch(0.5 0.015 259);

  --color-ink: oklch(0.86 0.012 265);
  --color-ink-muted: oklch(0.68 0.015 263);
  --color-ink-faint: oklch(0.55 0.018 262);

  --color-accent: oklch(0.72 0.14 245);
  --color-accent-strong: oklch(0.79 0.13 245);
  --color-accent-faint: oklch(0.37 0.08 248);
  --color-accent-ink: oklch(0.83 0.11 245);

  --color-ok: oklch(0.78 0.13 140);
  --color-ok-ink: oklch(0.87 0.11 140);
  --color-ok-faint: oklch(0.37 0.08 140);
  --color-warn: oklch(0.8 0.11 75);
  --color-warn-ink: oklch(0.88 0.1 75);
  --color-warn-faint: oklch(0.38 0.07 70);
  --color-err: oklch(0.7 0.15 20);
  --color-err-ink: oklch(0.84 0.12 20);
  --color-err-faint: oklch(0.37 0.09 20);

  --color-chart-0: oklch(0.72 0.14 245);
  --color-chart-1: oklch(0.78 0.13 140);
  --color-chart-2: oklch(0.8 0.11 75);
  --color-chart-3: oklch(0.7 0.15 20);
  --color-chart-4: oklch(0.74 0.11 195);
  --color-chart-5: oklch(0.7 0.15 320);
  --color-chart-6: oklch(0.72 0.13 45);
  --color-chart-7: oklch(0.76 0.12 165);
  --color-chart-8: oklch(0.66 0.13 255);
  --color-chart-9: oklch(0.8 0.12 110);

  --color-line: oklch(0.4 0.013 263);
  --color-line-soft: oklch(0.35 0.013 264);

  --color-sidebar-bg: oklch(0.28 0.012 265);
  --color-sidebar-text: oklch(0.68 0.015 263);
  --color-sidebar-text-active: oklch(0.9 0.012 265);
  --color-sidebar-hover: oklch(0.36 0.013 263);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.12 0.01 265 / 0.6);
}

:root[data-theme='monokai'] {
  --color-surface-0: oklch(0.27 0.012 110);
  --color-surface-50: oklch(0.24 0.012 110);
  --color-surface-100: oklch(0.33 0.013 108);
  --color-surface-200: oklch(0.39 0.014 106);
  --color-surface-300: oklch(0.48 0.015 104);

  --color-ink: oklch(0.96 0.008 100);
  --color-ink-muted: oklch(0.74 0.02 100);
  --color-ink-faint: oklch(0.58 0.025 100);

  --color-accent: oklch(0.78 0.13 195);
  --color-accent-strong: oklch(0.84 0.12 195);
  --color-accent-faint: oklch(0.36 0.07 200);
  --color-accent-ink: oklch(0.86 0.1 195);

  --color-ok: oklch(0.84 0.2 130);
  --color-ok-ink: oklch(0.9 0.16 130);
  --color-ok-faint: oklch(0.38 0.11 130);
  --color-warn: oklch(0.78 0.16 60);
  --color-warn-ink: oklch(0.87 0.13 65);
  --color-warn-faint: oklch(0.37 0.09 55);
  --color-err: oklch(0.66 0.24 5);
  --color-err-ink: oklch(0.82 0.16 358);
  --color-err-faint: oklch(0.36 0.12 5);

  --color-chart-0: oklch(0.78 0.13 195);
  --color-chart-1: oklch(0.84 0.2 130);
  --color-chart-2: oklch(0.78 0.16 60);
  --color-chart-3: oklch(0.66 0.24 5);
  --color-chart-4: oklch(0.88 0.13 110);
  --color-chart-5: oklch(0.7 0.17 300);
  --color-chart-6: oklch(0.8 0.12 175);
  --color-chart-7: oklch(0.76 0.15 40);
  --color-chart-8: oklch(0.72 0.16 330);
  --color-chart-9: oklch(0.82 0.16 150);

  --color-line: oklch(0.39 0.013 106);
  --color-line-soft: oklch(0.34 0.013 108);

  --color-sidebar-bg: oklch(0.24 0.012 110);
  --color-sidebar-text: oklch(0.74 0.02 100);
  --color-sidebar-text-active: oklch(0.96 0.008 100);
  --color-sidebar-hover: oklch(0.33 0.013 108);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.1 0.01 110 / 0.6);
}
```

- [ ] **Step 2: Add registry entries** (all dark, appended after `nord`):

```ts
  {
    value: 'tokyo-night',
    label: 'Tokyo Night',
    kind: 'dark',
    surface: 'oklch(0.24 0.025 270)',
    accent: 'oklch(0.7 0.15 265)',
  },
  {
    value: 'one-dark',
    label: 'One Dark',
    kind: 'dark',
    surface: 'oklch(0.31 0.012 265)',
    accent: 'oklch(0.72 0.14 245)',
  },
  {
    value: 'monokai',
    label: 'Monokai',
    kind: 'dark',
    surface: 'oklch(0.27 0.012 110)',
    accent: 'oklch(0.78 0.13 195)',
  },
```

- [ ] **Step 3: Verify** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.
- [ ] **Step 4: Commit** `git add dashboard/src/index.css dashboard/src/stores/themes.ts && git commit -m "feat: add tokyo night, one dark, and monokai themes"`

---

### Task 7: Catppuccin (Latte + Mocha) + Gruvbox (Light + Dark)

**Goal:** Add two light/dark pairs: `catppuccin-latte`, `catppuccin-mocha`, `gruvbox-light`, `gruvbox-dark`.

**Files:** Modify `dashboard/src/index.css` (four blocks), `dashboard/src/stores/themes.ts` (four entries).

**Acceptance Criteria:**
- [ ] All four apply with signature palettes (Catppuccin mauve, Gruvbox warm); conversation/response readable.
- [ ] `type-check` + `lint` pass.

**Verify:** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.

**Steps:**

- [ ] **Step 1: Append CSS blocks**

```css
:root[data-theme='catppuccin-latte'] {
  --color-surface-0: oklch(0.97 0.005 280);
  --color-surface-50: oklch(0.955 0.007 280);
  --color-surface-100: oklch(0.93 0.008 280);
  --color-surface-200: oklch(0.9 0.01 280);
  --color-surface-300: oklch(0.86 0.011 280);

  --color-ink: oklch(0.42 0.04 285);
  --color-ink-muted: oklch(0.54 0.04 285);
  --color-ink-faint: oklch(0.64 0.035 283);

  --color-accent: oklch(0.52 0.22 300);
  --color-accent-strong: oklch(0.46 0.24 300);
  --color-accent-faint: oklch(0.93 0.04 300);
  --color-accent-ink: oklch(0.46 0.22 300);

  --color-ok: oklch(0.58 0.16 145);
  --color-ok-ink: oklch(0.46 0.14 145);
  --color-ok-faint: oklch(0.93 0.06 145);
  --color-warn: oklch(0.68 0.13 70);
  --color-warn-ink: oklch(0.5 0.11 65);
  --color-warn-faint: oklch(0.95 0.06 80);
  --color-err: oklch(0.55 0.21 15);
  --color-err-ink: oklch(0.48 0.2 15);
  --color-err-faint: oklch(0.94 0.05 15);

  --color-chart-0: oklch(0.52 0.22 300);
  --color-chart-1: oklch(0.58 0.16 145);
  --color-chart-2: oklch(0.68 0.13 70);
  --color-chart-3: oklch(0.55 0.21 15);
  --color-chart-4: oklch(0.55 0.2 260);
  --color-chart-5: oklch(0.6 0.11 195);
  --color-chart-6: oklch(0.62 0.19 45);
  --color-chart-7: oklch(0.62 0.15 230);
  --color-chart-8: oklch(0.55 0.17 330);
  --color-chart-9: oklch(0.58 0.16 120);

  --color-line: oklch(0.9 0.008 280);
  --color-line-soft: oklch(0.93 0.007 280);

  --color-sidebar-bg: oklch(0.955 0.007 280);
  --color-sidebar-text: oklch(0.5 0.04 285);
  --color-sidebar-text-active: oklch(0.42 0.04 285);
  --color-sidebar-hover: oklch(0.93 0.008 280);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.42 0.03 285 / 0.4);
}

:root[data-theme='catppuccin-mocha'] {
  --color-surface-0: oklch(0.26 0.03 285);
  --color-surface-50: oklch(0.23 0.03 285);
  --color-surface-100: oklch(0.33 0.03 283);
  --color-surface-200: oklch(0.4 0.032 281);
  --color-surface-300: oklch(0.48 0.032 280);

  --color-ink: oklch(0.9 0.03 285);
  --color-ink-muted: oklch(0.76 0.035 283);
  --color-ink-faint: oklch(0.62 0.04 282);

  --color-accent: oklch(0.78 0.13 300);
  --color-accent-strong: oklch(0.83 0.12 300);
  --color-accent-faint: oklch(0.38 0.08 300);
  --color-accent-ink: oklch(0.86 0.1 300);

  --color-ok: oklch(0.84 0.13 145);
  --color-ok-ink: oklch(0.89 0.11 145);
  --color-ok-faint: oklch(0.38 0.08 145);
  --color-warn: oklch(0.85 0.1 80);
  --color-warn-ink: oklch(0.9 0.09 80);
  --color-warn-faint: oklch(0.4 0.07 70);
  --color-err: oklch(0.74 0.13 10);
  --color-err-ink: oklch(0.85 0.11 10);
  --color-err-faint: oklch(0.4 0.09 10);

  --color-chart-0: oklch(0.78 0.13 300);
  --color-chart-1: oklch(0.84 0.13 145);
  --color-chart-2: oklch(0.85 0.1 80);
  --color-chart-3: oklch(0.74 0.13 10);
  --color-chart-4: oklch(0.78 0.13 250);
  --color-chart-5: oklch(0.82 0.11 190);
  --color-chart-6: oklch(0.8 0.12 45);
  --color-chart-7: oklch(0.82 0.12 220);
  --color-chart-8: oklch(0.8 0.12 320);
  --color-chart-9: oklch(0.82 0.13 130);

  --color-line: oklch(0.4 0.03 283);
  --color-line-soft: oklch(0.34 0.03 284);

  --color-sidebar-bg: oklch(0.23 0.03 285);
  --color-sidebar-text: oklch(0.76 0.035 283);
  --color-sidebar-text-active: oklch(0.92 0.03 285);
  --color-sidebar-hover: oklch(0.33 0.03 283);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.1 0.025 285 / 0.62);
}

:root[data-theme='gruvbox-light'] {
  --color-surface-0: oklch(0.95 0.04 95);
  --color-surface-50: oklch(0.93 0.045 92);
  --color-surface-100: oklch(0.89 0.05 90);
  --color-surface-200: oklch(0.83 0.05 88);
  --color-surface-300: oklch(0.77 0.05 86);

  --color-ink: oklch(0.35 0.02 80);
  --color-ink-muted: oklch(0.5 0.03 75);
  --color-ink-faint: oklch(0.6 0.03 72);

  --color-accent: oklch(0.48 0.09 220);
  --color-accent-strong: oklch(0.42 0.1 222);
  --color-accent-faint: oklch(0.9 0.04 200);
  --color-accent-ink: oklch(0.42 0.1 222);

  --color-ok: oklch(0.55 0.13 130);
  --color-ok-ink: oklch(0.45 0.12 130);
  --color-ok-faint: oklch(0.92 0.06 125);
  --color-warn: oklch(0.62 0.13 75);
  --color-warn-ink: oklch(0.48 0.11 70);
  --color-warn-faint: oklch(0.93 0.07 80);
  --color-err: oklch(0.52 0.2 28);
  --color-err-ink: oklch(0.45 0.19 28);
  --color-err-faint: oklch(0.92 0.06 28);

  --color-chart-0: oklch(0.48 0.09 220);
  --color-chart-1: oklch(0.55 0.13 130);
  --color-chart-2: oklch(0.62 0.13 75);
  --color-chart-3: oklch(0.52 0.2 28);
  --color-chart-4: oklch(0.52 0.09 190);
  --color-chart-5: oklch(0.5 0.12 350);
  --color-chart-6: oklch(0.58 0.17 45);
  --color-chart-7: oklch(0.55 0.11 160);
  --color-chart-8: oklch(0.48 0.13 250);
  --color-chart-9: oklch(0.56 0.13 110);

  --color-line: oklch(0.83 0.05 88);
  --color-line-soft: oklch(0.89 0.045 90);

  --color-sidebar-bg: oklch(0.93 0.045 92);
  --color-sidebar-text: oklch(0.45 0.03 78);
  --color-sidebar-text-active: oklch(0.35 0.02 80);
  --color-sidebar-hover: oklch(0.89 0.05 90);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.35 0.03 80 / 0.4);
}

:root[data-theme='gruvbox-dark'] {
  --color-surface-0: oklch(0.28 0.008 90);
  --color-surface-50: oklch(0.25 0.008 90);
  --color-surface-100: oklch(0.34 0.009 88);
  --color-surface-200: oklch(0.41 0.01 86);
  --color-surface-300: oklch(0.49 0.012 84);

  --color-ink: oklch(0.89 0.035 95);
  --color-ink-muted: oklch(0.72 0.03 95);
  --color-ink-faint: oklch(0.62 0.03 92);

  --color-accent: oklch(0.74 0.13 60);
  --color-accent-strong: oklch(0.8 0.12 65);
  --color-accent-faint: oklch(0.38 0.07 55);
  --color-accent-ink: oklch(0.82 0.11 62);

  --color-ok: oklch(0.8 0.15 125);
  --color-ok-ink: oklch(0.87 0.13 125);
  --color-ok-faint: oklch(0.37 0.09 125);
  --color-warn: oklch(0.82 0.14 85);
  --color-warn-ink: oklch(0.88 0.12 85);
  --color-warn-faint: oklch(0.38 0.08 80);
  --color-err: oklch(0.64 0.2 28);
  --color-err-ink: oklch(0.8 0.15 28);
  --color-err-faint: oklch(0.36 0.11 28);

  --color-chart-0: oklch(0.74 0.13 60);
  --color-chart-1: oklch(0.8 0.15 125);
  --color-chart-2: oklch(0.82 0.14 85);
  --color-chart-3: oklch(0.64 0.2 28);
  --color-chart-4: oklch(0.74 0.08 195);
  --color-chart-5: oklch(0.7 0.1 350);
  --color-chart-6: oklch(0.78 0.11 145);
  --color-chart-7: oklch(0.72 0.09 220);
  --color-chart-8: oklch(0.7 0.13 40);
  --color-chart-9: oklch(0.82 0.13 110);

  --color-line: oklch(0.4 0.01 88);
  --color-line-soft: oklch(0.34 0.009 90);

  --color-sidebar-bg: oklch(0.25 0.008 90);
  --color-sidebar-text: oklch(0.72 0.03 95);
  --color-sidebar-text-active: oklch(0.9 0.035 95);
  --color-sidebar-hover: oklch(0.34 0.009 88);
  --color-sidebar-active-bg: var(--color-accent-faint);
  --color-sidebar-active-text: var(--color-accent-ink);

  --color-overlay-bg: oklch(0.12 0.01 90 / 0.6);
}
```

- [ ] **Step 2: Add registry entries** — lights (`catppuccin-latte`, `gruvbox-light`) after `github-light`; darks (`catppuccin-mocha`, `gruvbox-dark`) after `monokai`:

```ts
  // lights, after github-light:
  {
    value: 'catppuccin-latte',
    label: 'Catppuccin Latte',
    kind: 'light',
    surface: 'oklch(0.97 0.005 280)',
    accent: 'oklch(0.52 0.22 300)',
  },
  {
    value: 'gruvbox-light',
    label: 'Gruvbox Light',
    kind: 'light',
    surface: 'oklch(0.95 0.04 95)',
    accent: 'oklch(0.48 0.09 220)',
  },
```
```ts
  // darks, after monokai:
  {
    value: 'catppuccin-mocha',
    label: 'Catppuccin Mocha',
    kind: 'dark',
    surface: 'oklch(0.26 0.03 285)',
    accent: 'oklch(0.78 0.13 300)',
  },
  {
    value: 'gruvbox-dark',
    label: 'Gruvbox Dark',
    kind: 'dark',
    surface: 'oklch(0.28 0.008 90)',
    accent: 'oklch(0.74 0.13 60)',
  },
```

- [ ] **Step 3: Verify** `pnpm --dir dashboard type-check && pnpm --dir dashboard lint` → exit 0.
- [ ] **Step 4: Commit** `git add dashboard/src/index.css dashboard/src/stores/themes.ts && git commit -m "feat: add catppuccin and gruvbox themes"`

---

### Task 8: Live browser verification across all 17 themes

**Goal:** With the full stack running, visually confirm in a visible (non-headless, user-watched) browser that every theme — especially the dark ones — renders the conversation, thinking, response reply, inline code, code blocks, and tool output readably, and tune any OKLCH values that read off.

**Files:**
- Possibly Modify: `dashboard/src/index.css` (tune specific token values if a theme reads off)
- Possibly Modify: `dashboard/src/components/JsonArtifactViewer.vue` (only if the JSON tree reads wrong on dark themes — add a `[data-dark='true'] .json-artifact-viewer { --jse-theme: dark }` rule)

**Acceptance Criteria:**
- [ ] Full stack up: infra (`sudo docker compose up -d`), backend from the MAIN repo on :9898, dashboard dev server from the worktree on :5173.
- [ ] At least one `request` row seeded (`type=0, status=2`) whose request/response artifacts contain Markdown (headings, bold, a link, a list, inline code, a fenced code block) and a tool call, so the request-detail conversation/response tabs render rich content.
- [ ] A visible browser is driven through the request-detail view; for EACH of the 17 themes, the conversation text, thinking block, response reply, inline code, fenced code block, and tool output are all legible (no dark-on-dark, no invisible headings/bold/links).
- [ ] Screenshots captured for the dark themes (`dark`, `solarized-dark`, `vs-dark`, `github-dark`, `dracula`, `nord`, `tokyo-night`, `one-dark`, `monokai`, `catppuccin-mocha`, `gruvbox-dark`).
- [ ] `pnpm --dir dashboard build` succeeds (full production build, not just type-check).

**Verify:** Visual confirmation across all 17 themes (screenshots) + `pnpm --dir dashboard build` exits 0.

**Steps:**

- [ ] **Step 1: Bring up infra** (from the main repo `/home/tioplato/projects/picotera`)

Run: `sudo docker compose up -d` then wait for `sudo docker exec picotera-postgres-1 pg_isready -U picotera -d picotera`.

- [ ] **Step 2: Build plugin + run backend from the MAIN repo** (worktrees lack the `third_party/axonhub` submodule)

```bash
cd /home/tioplato/projects/picotera
go build -o dist/picotera-llmbridge-plugin ./cmd/picotera-llmbridge-plugin
PICOTERA_DATABASE_URL=postgres://picotera:picotera@localhost:34052/picotera?sslmode=disable \
  PICOTERA_LLMBRIDGE_PLUGIN_PATH=/home/tioplato/projects/picotera/dist/picotera-llmbridge-plugin \
  go run ./cmd/picotera/main.go
```
(run in background; serves :9898, runs migrations on startup)

- [ ] **Step 3: Run the dashboard dev server from the worktree**

```bash
pnpm install   # workspace root, first run only
pnpm --dir dashboard dev   # :5173, proxies /api + /v1 to :9898
```

- [ ] **Step 4: Seed a rich request row** so the conversation/response renders Markdown + tool calls. Insert directly into the `request` hypertable (`type=0, status=2`, supply `id` + `created_at`) with request/response bodies containing Markdown (heading, **bold**, `inline code`, a fenced ```code``` block, a list, a link) and an assistant tool call. Confirm it shows under `/requests` (default filters `type=0`) and open `/requests/:id`.

- [ ] **Step 5: Launch a visible browser on WSLg and drive it** (per memory `browser-verification-visible`): visible chromium with `DISPLAY=:0`, `--remote-debugging-port=9222`; drive via the browser MCP / CDP. Navigate to the seeded request detail, open the conversation + response tabs.

- [ ] **Step 6: Cycle every theme and check legibility.** For each of the 17 `THEMES` values, set it (via the preferences menu, or `document.documentElement.dataset.theme = '<slug>'` + `dataset.dark`), and confirm in the conversation/response: body text, headings, bold, links, inline code, fenced code block background+text, blockquotes, list markers, and tool-call JSON are all readable. Screenshot each dark theme. Tune any off OKLCH values in `index.css` and reload.

- [ ] **Step 7: Production build**

Run: `pnpm --dir dashboard build`
Expected: exit 0 (vue-tsc + vite build succeed).

- [ ] **Step 8: Commit any tuning** (only if Step 6 changed files)

```bash
git add dashboard/src/index.css dashboard/src/components/JsonArtifactViewer.vue
git commit -m "fix: tune theme contrast from browser review"
```

---

## Self-Review

**Spec coverage:**
- Spec §2/§3a (prose bug) → Task 1. ✔
- Spec §2 secondary (CodeEditor `data-dark`) → Task 2, Step 5. ✔
- Spec §3c (registry single source) → Task 2. ✔
- Spec §4 (13 themes) → Tasks 3–7 (vs ×2, github ×2, dracula, nord, tokyo-night, one-dark, monokai, catppuccin ×2, gruvbox ×2 = 13). ✔
- Spec §5 (menu light/dark groups + scroll) → Task 2, Step 4. ✔
- Spec §6 (file changes) → all five files + the new module covered. ✔
- Spec §7 (verification) → Task 8. ✔

**Placeholder scan:** No "TBD/TODO". Every CSS block and TS snippet is concrete. Task 8 Step 4/6 describe data-seeding and per-theme checks with explicit, observable criteria (not "handle edge cases").

**Type consistency:** `ThemeDef` fields (`value/label/kind/surface/accent`) are consistent across `themes.ts` and every registry entry in Tasks 3–7. `Theme`, `THEME_VALUES`, `isDarkTheme` are defined in Task 2 and used by `preferences.ts` (re-exported) and `PreferencesMenu.vue`. `isDark()` in `CodeEditor.vue` reads the `data-dark` attribute `preferences.ts` sets. Slugs in `index.css` blocks exactly match the registry `value`s.

**Ordering / dependencies:** Task 1 independent. Task 2 must precede 3–7 (registry must exist). Tasks 3–7 each append to the same two files (sequential to avoid conflicts). Task 8 last (needs all themes).
</content>

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
    value: 'vs-light',
    label: 'Visual Studio Light',
    kind: 'light',
    surface: 'oklch(0.985 0.001 250)',
    accent: 'oklch(0.5 0.16 250)',
  },
  {
    value: 'github-light',
    label: 'GitHub Light',
    kind: 'light',
    surface: 'oklch(0.985 0.002 250)',
    accent: 'oklch(0.55 0.18 255)',
  },
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
  {
    value: 'amber-minimal-light',
    label: 'Amber Minimal Light',
    kind: 'light',
    surface: 'oklch(1 0 0)',
    accent: 'oklch(0.72 0.165 70.08)',
  },
  {
    value: 'claude-light',
    label: 'Claude Light',
    kind: 'light',
    surface: 'oklch(0.982 0.005 95.099)',
    accent: 'oklch(0.617 0.138 39.043)',
  },
  {
    value: 'perpetuity-light',
    label: 'Perpetuity Light',
    kind: 'light',
    surface: 'oklch(0.972 0.005 197.069)',
    accent: 'oklch(0.562 0.095 203.275)',
  },
  {
    value: 'nature-light',
    label: 'Nature Light',
    kind: 'light',
    surface: 'oklch(0.971 0.007 80.721)',
    accent: 'oklch(0.523 0.135 144.167)',
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
  {
    value: 'vs-dark',
    label: 'Visual Studio Dark',
    kind: 'dark',
    surface: 'oklch(0.26 0.004 250)',
    accent: 'oklch(0.62 0.15 245)',
  },
  {
    value: 'github-dark',
    label: 'GitHub Dark',
    kind: 'dark',
    surface: 'oklch(0.2 0.015 260)',
    accent: 'oklch(0.65 0.17 255)',
  },
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
  {
    value: 'amber-minimal-dark',
    label: 'Amber Minimal Dark',
    kind: 'dark',
    surface: 'oklch(0.269 0 0)',
    accent: 'oklch(0.769 0.165 70.08)',
  },
  {
    value: 'claude-dark',
    label: 'Claude Dark',
    kind: 'dark',
    surface: 'oklch(0.308 0.004 106.604)',
    accent: 'oklch(0.672 0.131 38.756)',
  },
  {
    value: 'perpetuity-dark',
    label: 'Perpetuity Dark',
    kind: 'dark',
    surface: 'oklch(0.229 0.028 216.067)',
    accent: 'oklch(0.78 0.127 195.035)',
  },
  {
    value: 'nature-dark',
    label: 'Nature Dark',
    kind: 'dark',
    surface: 'oklch(0.333 0.027 146.987)',
    accent: 'oklch(0.673 0.162 144.208)',
  },
] as const satisfies readonly ThemeDef[]

export type Theme = (typeof THEMES)[number]['value']

export const THEME_VALUES = THEMES.map((t) => t.value) as Theme[]

export function isDarkTheme(value: Theme): boolean {
  return THEMES.find((t) => t.value === value)?.kind === 'dark'
}

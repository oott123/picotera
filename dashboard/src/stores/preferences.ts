import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

export type Theme = 'light' | 'solarized-light' | 'solarized-dark' | 'dark'
export type PanelMode = 'auto' | 'right' | 'modal'
export type FontSize = 'tall' | 'grande' | 'venti' | 'trenta'
export type OverviewCurrencyOverride = 'original' | string | null

const STORAGE_KEY = 'picotera.preferences'

const DEFAULTS = {
  theme: 'light' as Theme,
  panelMode: 'auto' as PanelMode,
  fontSize: 'tall' as FontSize,
  displayCurrency: null as string | null,
  overviewCurrencyOverride: null as OverviewCurrencyOverride,
}

const THEME_VALUES: Theme[] = ['light', 'solarized-light', 'solarized-dark', 'dark']
const PANEL_MODE_VALUES: PanelMode[] = ['auto', 'right', 'modal']
const FONT_SIZE_VALUES: FontSize[] = ['tall', 'grande', 'venti', 'trenta']

export const FONT_SIZE_PX: Record<FontSize, number> = {
  tall: 14,
  grande: 16,
  venti: 18,
  trenta: 24,
}

function load() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return { ...DEFAULTS }
    const parsed = JSON.parse(raw) as Partial<typeof DEFAULTS>
    return {
      theme: THEME_VALUES.includes(parsed.theme as Theme)
        ? (parsed.theme as Theme)
        : DEFAULTS.theme,
      panelMode: PANEL_MODE_VALUES.includes(parsed.panelMode as PanelMode)
        ? (parsed.panelMode as PanelMode)
        : DEFAULTS.panelMode,
      fontSize: FONT_SIZE_VALUES.includes(parsed.fontSize as FontSize)
        ? (parsed.fontSize as FontSize)
        : DEFAULTS.fontSize,
      displayCurrency:
        typeof parsed.displayCurrency === 'string' && parsed.displayCurrency.length > 0
          ? parsed.displayCurrency
          : DEFAULTS.displayCurrency,
      overviewCurrencyOverride:
        typeof parsed.overviewCurrencyOverride === 'string' &&
        parsed.overviewCurrencyOverride.length > 0
          ? parsed.overviewCurrencyOverride
          : DEFAULTS.overviewCurrencyOverride,
    }
  } catch {
    return { ...DEFAULTS }
  }
}

export const usePreferencesStore = defineStore('preferences', () => {
  const initial = load()
  const theme = ref<Theme>(initial.theme)
  const panelMode = ref<PanelMode>(initial.panelMode)
  const fontSize = ref<FontSize>(initial.fontSize)
  const displayCurrency = ref<string | null>(initial.displayCurrency)
  const overviewCurrencyOverride = ref<OverviewCurrencyOverride>(initial.overviewCurrencyOverride)

  function apply() {
    const root = document.documentElement
    root.dataset.theme = theme.value
    root.dataset.panelMode = panelMode.value
    root.dataset.dark = String(theme.value === 'dark' || theme.value === 'solarized-dark')
    root.style.fontSize = `${FONT_SIZE_PX[fontSize.value]}px`
  }

  function persist() {
    try {
      localStorage.setItem(
        STORAGE_KEY,
        JSON.stringify({
          theme: theme.value,
          panelMode: panelMode.value,
          fontSize: fontSize.value,
          displayCurrency: displayCurrency.value,
          overviewCurrencyOverride: overviewCurrencyOverride.value,
        }),
      )
    } catch {
      // ignore quota / privacy-mode errors
    }
  }

  watch([theme, panelMode, fontSize, displayCurrency, overviewCurrencyOverride], () => {
    apply()
    persist()
  })

  function init() {
    apply()
  }

  return { theme, panelMode, fontSize, displayCurrency, overviewCurrencyOverride, init }
})

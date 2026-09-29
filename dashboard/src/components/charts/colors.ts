import Color from 'colorjs.io'

const CHART_COLOR_COUNT = 10

const FALLBACK_COLORS = [
  'oklch(0.54 0.19 262)',
  'oklch(0.62 0.15 155)',
  'oklch(0.65 0.15 80)',
  'oklch(0.58 0.19 25)',
  'oklch(0.60 0.14 195)',
  'oklch(0.55 0.18 300)',
  'oklch(0.62 0.16 350)',
  'oklch(0.60 0.14 120)',
  'oklch(0.52 0.17 235)',
  'oklch(0.60 0.15 55)',
]

function toHex(color: string): string {
  return new Color(color).to('srgb').toString({ format: 'hex' })
}

function readCSSVar(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

export function groupColor(index: number): string {
  if (index < CHART_COLOR_COUNT) {
    return toHex(readCSSVar(`--color-chart-${index}`) || FALLBACK_COLORS[index]!)
  }
  const base =
    readCSSVar(`--color-chart-${index % CHART_COLOR_COUNT}`) ||
    FALLBACK_COLORS[index % CHART_COLOR_COUNT]!
  const hueMatch = base.match(/oklch\(([^ ]+) ([^ ]+) ([^ )]+)\)/)
  if (!hueMatch) return toHex(base)
  const [, l, c, h] = hueMatch
  const rotation = 23 * Math.floor(index / CHART_COLOR_COUNT)
  const hue = (parseFloat(h!) + rotation) % 360
  return toHex(`oklch(${l} ${c} ${hue})`)
}

// Border shade for filled marks (e.g. boxplot): darker than the fill on light themes,
// lighter on dark themes, so lines drawn with the border color (boxplot median) stand out.
export function groupBorderColor(index: number): string {
  const dark = document.documentElement.dataset.dark === 'true'
  const c = new Color(groupColor(index)).to('oklch')
  c.coords[0] = Math.min(1, Math.max(0, c.coords[0]! + (dark ? 0.15 : -0.15)))
  return toHex(c.toString())
}

export function getChartColors(): string[] {
  const colors: string[] = []
  for (let i = 0; i < CHART_COLOR_COUNT; i++) {
    colors.push(toHex(readCSSVar(`--color-chart-${i}`) || FALLBACK_COLORS[i]!))
  }
  return colors
}

export function getThemeAxisStyle() {
  return {
    axisLine: toHex(readCSSVar('--color-line') || '#e5e5e5'),
    axisTick: toHex(readCSSVar('--color-line') || '#e5e5e5'),
    axisLabel: toHex(readCSSVar('--color-ink-muted') || '#737373'),
    splitLine: toHex(readCSSVar('--color-line-soft') || '#f0f0f0'),
    tooltipBg: toHex(readCSSVar('--color-surface-0') || '#ffffff'),
    tooltipBorder: toHex(readCSSVar('--color-line') || '#e5e5e5'),
    tooltipText: toHex(readCSSVar('--color-ink') || '#1a1a1a'),
  }
}

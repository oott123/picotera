import { compile } from 'tailwindcss'
import tailwindTheme from 'tailwindcss/theme.css?raw'
import tailwindUtilities from 'tailwindcss/utilities.css?raw'
import dashboardTheme from '@/theme.css?raw'

// index.css without preflight and base styles, which the dashboard's own
// stylesheet already carries; utilities join its cascade layers.
const INPUT = `@layer theme, base, components, utilities;
@import 'tailwindcss/theme.css' layer(theme);
@import 'tailwindcss/utilities.css' layer(utilities);
@import 'dashboard/theme.css';`

const STYLESHEETS: Record<string, string> = {
  'tailwindcss/theme.css': tailwindTheme,
  'tailwindcss/utilities.css': tailwindUtilities,
  'dashboard/theme.css': dashboardTheme,
}

async function loadStylesheet(id: string) {
  const content = STYLESHEETS[id]
  if (content === undefined) throw new Error(`unknown stylesheet ${id}`)
  return { path: id, base: '/', content }
}

let compiler: ReturnType<typeof compile> | null = null
let style: HTMLStyleElement | null = null

// Like Tailwind's own source scanning, every token of the source is a
// candidate; build() drops the ones that aren't utilities.
function extractCandidates(source: string): string[] {
  return [...new Set(source.split(/[\s'"`\\]+/).filter(Boolean))]
}

// addExtensionUtilities compiles the Tailwind utilities a frontend module's
// source mentions. The compiler accumulates candidates across calls, so the
// shared <style> always holds every extension's utilities loaded so far.
export async function addExtensionUtilities(source: string) {
  compiler ??= compile(INPUT, { loadStylesheet })
  const css = (await compiler).build(extractCandidates(source))
  if (!style) {
    style = document.createElement('style')
    style.dataset.picoteraExtensions = ''
    document.head.append(style)
  }
  style.textContent = css
}

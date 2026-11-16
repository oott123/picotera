import { iconComponents, type IconName } from '@/ui/icons/paths'
import type {
  ExtensionButton,
  ExtensionDescriptor,
  ExtensionPage,
  ExtensionSlot,
} from '@/extensions/types'

const PAGE_ID_PATTERN = /^[a-zA-Z0-9_-]+$/
const SLOTS: readonly ExtensionSlot[] = ['providers.toolbar', 'providers.row', 'models.toolbar']

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function checkKeys(value: Record<string, unknown>, allowed: readonly string[], where: string) {
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) throw new Error(`${where}: unknown key "${key}"`)
  }
}

function checkNonEmptyString(value: unknown, where: string): string {
  if (typeof value !== 'string' || value === '') {
    throw new Error(`${where} must be a non-empty string`)
  }
  return value
}

function checkIcon(value: unknown, where: string): IconName {
  if (typeof value !== 'string' || !Object.hasOwn(iconComponents, value)) {
    throw new Error(`${where} must be one of the dashboard icon names`)
  }
  return value as IconName
}

function validatePage(value: unknown, where: string, seen: Set<string>): ExtensionPage {
  if (!isPlainObject(value)) throw new Error(`${where} must be an object`)
  checkKeys(value, ['id', 'title', 'icon', 'component'], where)
  const id = value.id
  if (typeof id !== 'string' || id.length < 1 || id.length > 64 || !PAGE_ID_PATTERN.test(id)) {
    throw new Error(`${where}.id must match ^[a-zA-Z0-9_-]+$ and be 1-64 characters`)
  }
  if (seen.has(id)) throw new Error(`${where}.id "${id}" is duplicated`)
  seen.add(id)
  const title = checkNonEmptyString(value.title, `${where}.title`)
  const component = value.component
  if ((typeof component !== 'object' || component === null) && typeof component !== 'function') {
    throw new Error(`${where}.component must be a Vue component`)
  }
  const page: ExtensionPage = { id, title, component: component as ExtensionPage['component'] }
  if (value.icon !== undefined) page.icon = checkIcon(value.icon, `${where}.icon`)
  return page
}

function validateButton(value: unknown, where: string): ExtensionButton {
  if (!isPlainObject(value)) throw new Error(`${where} must be an object`)
  checkKeys(value, ['slot', 'label', 'icon', 'onClick'], where)
  const slot = value.slot
  if (typeof slot !== 'string' || !SLOTS.includes(slot as ExtensionSlot)) {
    throw new Error(`${where}.slot must be one of ${SLOTS.join(', ')}`)
  }
  const label = checkNonEmptyString(value.label, `${where}.label`)
  let icon: IconName | undefined
  if (value.icon !== undefined) icon = checkIcon(value.icon, `${where}.icon`)
  else if (slot === 'providers.row') throw new Error(`${where}.icon is required on providers.row`)
  if (typeof value.onClick !== 'function') throw new Error(`${where}.onClick must be a function`)
  const button = { slot, label, onClick: value.onClick } as ExtensionButton
  if (icon !== undefined) button.icon = icon
  return button
}

// validateDescriptor checks what a frontend module's default export returned.
// It is strict on purpose: an unknown key or a wrong type throws, naming the
// offending field, so a typo never silently drops a page or a button.
export function validateDescriptor(value: unknown): ExtensionDescriptor {
  if (!isPlainObject(value)) throw new Error('descriptor must be an object')
  checkKeys(value, ['pages', 'buttons'], 'descriptor')
  const out: ExtensionDescriptor = {}
  if (value.pages !== undefined) {
    if (!Array.isArray(value.pages)) throw new Error('pages must be an array')
    const seen = new Set<string>()
    out.pages = value.pages.map((p, i) => validatePage(p, `pages[${i}]`, seen))
  }
  if (value.buttons !== undefined) {
    if (!Array.isArray(value.buttons)) throw new Error('buttons must be an array')
    out.buttons = value.buttons.map((b, i) => validateButton(b, `buttons[${i}]`))
  }
  return out
}

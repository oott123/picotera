import { defineStore } from 'pinia'
import { computed, markRaw, ref } from 'vue'
import type { MeView, ScriptFrontendView } from '@/api'
import { createExtensionHost } from '@/extensions/host'
import { validateDescriptor } from '@/extensions/validate'
import type {
  ExtensionButton,
  ExtensionDescriptor,
  ExtensionPage,
  ExtensionSlot,
} from '@/extensions/types'

export type ExtensionEntry = {
  scriptId: string
  scriptName: string
  frontend: string // the source this load is for, to tell whether it changed
  generation: number // bumped on every (re)load, to drop stale results
  status: 'loading' | 'ready' | 'error'
  error: string // set when status === 'error'
  descriptor: ExtensionDescriptor | null
}

export type ExtensionPageItem = ExtensionPage & { scriptId: string; scriptName: string }
export type ExtensionButtonItem = ExtensionButton & { scriptId: string }
export type ExtensionButtonError = { scriptId: string; label: string; message: string }

// Shared across entries rather than per entry, so a script that is removed and
// re-added can't reuse the generation of a load still in flight.
let nextGeneration = 0

function errorMessage(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

// importDescriptor compiles the Tailwind utilities a frontend module uses,
// evaluates it from its source through a blob URL and runs its default export.
// A module can't be unloaded: a reload imports a fresh copy and lets the old one
// go unreferenced.
async function importDescriptor(frontend: string, me: MeView): Promise<ExtensionDescriptor> {
  // Loaded on demand: the compiler only ships to dashboards that have extensions.
  const { addExtensionUtilities } = await import('@/extensions/tailwind')
  await addExtensionUtilities(frontend)
  const url = URL.createObjectURL(new Blob([frontend], { type: 'text/javascript' }))
  let mod: { default?: unknown }
  try {
    mod = await import(/* @vite-ignore */ url)
  } finally {
    URL.revokeObjectURL(url)
  }
  if (typeof mod.default !== 'function') throw new Error('default export must be a function')
  return validateDescriptor(await mod.default(createExtensionHost(me)))
}

export const useExtensionsStore = defineStore('extensions', () => {
  const entries = ref<Record<string, ExtensionEntry>>({})
  const me = ref<MeView | null>(null)
  const buttonError = ref<ExtensionButtonError | null>(null)

  const readyEntries = computed(() =>
    Object.values(entries.value)
      .filter((e) => e.status === 'ready')
      .sort((a, b) => (a.scriptId < b.scriptId ? -1 : a.scriptId > b.scriptId ? 1 : 0)),
  )

  const pages = computed<ExtensionPageItem[]>(() =>
    readyEntries.value.flatMap((e) =>
      (e.descriptor?.pages ?? []).map((p) => ({
        ...p,
        scriptId: e.scriptId,
        scriptName: e.scriptName,
      })),
    ),
  )

  function buttonsFor(slot: ExtensionSlot): ExtensionButtonItem[] {
    return readyEntries.value.flatMap((e) =>
      (e.descriptor?.buttons ?? [])
        .filter((b) => b.slot === slot)
        .map((b) => ({ ...b, scriptId: e.scriptId })),
    )
  }

  function findPage(scriptId: string, pageId: string): ExtensionPageItem | undefined {
    return pages.value.find((p) => p.scriptId === scriptId && p.id === pageId)
  }

  async function load(scriptId: string, frontend: string, generation: number, forMe: MeView) {
    let descriptor: ExtensionDescriptor | null = null
    let error = ''
    try {
      descriptor = await importDescriptor(frontend, forMe)
    } catch (e) {
      error = errorMessage(e)
    }
    const entry = entries.value[scriptId]
    if (!entry || entry.generation !== generation) return
    if (descriptor) {
      // markRaw: the descriptor carries Vue components, which must not be
      // wrapped in reactive proxies.
      entry.descriptor = markRaw(descriptor)
      entry.status = 'ready'
    } else {
      entry.error = error
      entry.status = 'error'
    }
  }

  // sync reconciles the loaded modules with the latest script-frontends list,
  // reloading only those whose source changed — or all of them when the
  // current user changed, since `host.me` did.
  function sync(frontends: ScriptFrontendView[], nextMe: MeView) {
    const reloadAll =
      me.value === null || me.value.id !== nextMe.id || me.value.isAdmin !== nextMe.isAdmin
    me.value = nextMe
    const ids = new Set(frontends.map((f) => f.id))
    for (const id of Object.keys(entries.value)) {
      if (!ids.has(id)) delete entries.value[id]
    }
    for (const f of frontends) {
      const current = entries.value[f.id]
      if (current && !reloadAll && current.frontend === f.frontend) {
        current.scriptName = f.name
        continue
      }
      const generation = ++nextGeneration
      entries.value[f.id] = {
        scriptId: f.id,
        scriptName: f.name,
        frontend: f.frontend,
        generation,
        status: 'loading',
        error: '',
        descriptor: null,
      }
      void load(f.id, f.frontend, generation, nextMe)
    }
  }

  return { entries, me, buttonError, pages, buttonsFor, findPage, sync }
})

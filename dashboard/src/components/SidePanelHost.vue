<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useSidePanel } from '@/composables/useSidePanel'
import { usePreferencesStore } from '@/stores/preferences'

const { state, close } = useSidePanel()
const prefs = usePreferencesStore()
const cssWidth = computed(() => state.value?.width ?? '420px')

const narrow = ref(false)
let mql: MediaQueryList | null = null
function onChange(e: MediaQueryListEvent) {
  narrow.value = e.matches
}
onMounted(() => {
  mql = window.matchMedia('(max-width: 960px)')
  narrow.value = mql.matches
  mql.addEventListener('change', onChange)
})
onUnmounted(() => {
  mql?.removeEventListener('change', onChange)
})

const mode = computed<'right' | 'modal'>(() => {
  if (prefs.panelMode === 'right') return 'right'
  if (prefs.panelMode === 'modal') return 'modal'
  return narrow.value ? 'modal' : 'right'
})
</script>

<template>
  <!-- The panel is pinned to the viewport, so the reserved column is a spacer of its own. -->
  <div
    v-if="state && mode === 'right'"
    class="flex-none"
    :style="{ flexBasis: cssWidth, width: cssWidth }"
  />
  <aside
    v-if="state"
    class="flex min-h-0"
    :class="
      mode === 'modal'
        ? 'fixed inset-0 z-[900] w-auto items-center justify-center p-4'
        : 'fixed top-3 right-0 pr-8 pb-8 h-[calc(100dvh-0.75rem)] max-h-[calc(100dvh-0.75rem)]'
    "
    :style="mode === 'modal' ? undefined : { width: cssWidth }"
  >
    <div
      v-if="mode === 'modal'"
      class="absolute inset-0 bg-overlay-bg backdrop-blur-[4px]"
      @click="close"
    />
    <component
      :is="state.component"
      :key="state.key"
      v-bind="state.props"
      class="w-full max-h-full min-h-0"
      :class="
        mode === 'modal'
          ? 'relative max-h-[calc(100vh-2rem)] shadow-[0_25px_50px_-12px_oklch(0.1_0.02_250/0.25)]'
          : ''
      "
      :style="mode === 'modal' ? { width: `min(${cssWidth}, 100%)` } : undefined"
      @close="close"
    />
  </aside>
</template>

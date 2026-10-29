import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

export type ImpersonationTarget = { userId: number; displayName: string }

// In-memory only (not persisted): a page refresh returns to the real identity,
// a safe default. The openapi-fetch middleware reads `target` to inject the
// impersonation header on every management request.
export const useImpersonationStore = defineStore('impersonation', () => {
  const target = ref<ImpersonationTarget | null>(null)
  const isImpersonating = computed(() => target.value !== null)

  function start(user: { id: number; displayName: string }) {
    target.value = { userId: user.id, displayName: user.displayName }
  }

  function stop() {
    target.value = null
  }

  return { target, isImpersonating, start, stop }
})

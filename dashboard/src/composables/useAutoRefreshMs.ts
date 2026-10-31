import { ref, watch, type Ref } from 'vue'

export const AUTO_REFRESH_OPTIONS: { value: number; label: string }[] = [
  { value: 0, label: '手动' },
  { value: 200, label: '0.2 秒' },
  { value: 5000, label: '5 秒' },
  { value: 10000, label: '10 秒' },
  { value: 30000, label: '30 秒' },
  { value: 60000, label: '1 分钟' },
]

// Each caller passes its own storage key so one view's auto-refresh setting
// never affects another view.
export function useAutoRefreshMs(storageKey: string): Ref<number> {
  const load = (): number => {
    try {
      const raw = localStorage.getItem(storageKey)
      if (raw == null) return 0
      const n = Number(raw)
      return AUTO_REFRESH_OPTIONS.some((o) => o.value === n) ? n : 0
    } catch {
      return 0
    }
  }
  const ms = ref(load())
  watch(ms, (v) => {
    try {
      localStorage.setItem(storageKey, String(v))
    } catch {
      // ignore quota / privacy-mode errors
    }
  })
  return ms
}

import { computed, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { getConfig } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'

const DEFAULT_TITLE = 'PicoTera'

export function useAppTitle() {
  const query = useQuery({
    queryKey: queryKeys.config,
    queryFn: getConfig,
    retry: false,
    throwOnError: false,
  })

  const appTitle = computed(() => {
    const title = query.data.value?.title
    if (typeof title === 'string') {
      return title.trim() || DEFAULT_TITLE
    }
    return DEFAULT_TITLE
  })

  // Only the oidc mode has an interactive login, so it is the only one where
  // logging out means anything.
  const authMode = computed(() => query.data.value?.authMode ?? '')
  const canLogout = computed(() => authMode.value === 'oidc')

  // Keep document.title in sync.
  watch(
    appTitle,
    (title) => {
      document.title = title
    },
    { immediate: true },
  )

  return {
    appTitle,
    authMode,
    canLogout,
    query,
  }
}

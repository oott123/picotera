import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { listProviders } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import type { ProviderView } from '@/api'
import { useSession } from '@/composables/useSession'

export function useProvidersMap() {
  const session = useSession()
  // admin-only data; non-admin sees empty map and ID fallback labels
  const query = useQuery({
    queryKey: queryKeys.providers.all,
    queryFn: listProviders,
    enabled: session.isAdmin,
  })
  const providers = computed(() => query.data.value ?? [])
  const providersMap = computed(() => {
    const m = new Map<number, ProviderView>()
    for (const p of providers.value) m.set(p.id, p)
    return m
  })

  function providerLabel(id: number): string {
    const p = providersMap.value.get(id)
    return p ? p.name : `#${id}`
  }

  return { providers, providersMap, providerLabel, fetchProviders: query.refetch, query }
}

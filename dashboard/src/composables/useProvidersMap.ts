import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { listProviderLabels } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import type { ProviderLabel } from '@/api'

export function useProvidersMap() {
  const query = useQuery({
    queryKey: queryKeys.labels.providers,
    queryFn: listProviderLabels,
  })
  const providers = computed(() => query.data.value ?? [])
  const providersMap = computed(() => {
    const m = new Map<number, ProviderLabel>()
    for (const p of providers.value) m.set(p.id, p)
    return m
  })

  function providerLabel(id: number): string {
    const p = providersMap.value.get(id)
    return p ? p.name : `#${id}`
  }

  return { providers, providersMap, providerLabel, fetchProviders: query.refetch, query }
}

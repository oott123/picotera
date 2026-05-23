import { computed } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { ExchangeRateView } from '@/api'
import {
  deleteExchangeRate,
  invalidateExchangeRates,
  listExchangeRates,
  upsertExchangeRate,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { useSession } from '@/composables/useSession'

export function useExchangeRates() {
  const client = useQueryClient()
  const session = useSession()
  // admin-only data; non-admin sees empty rates without a 403
  const query = useQuery({
    queryKey: queryKeys.exchangeRates.all,
    queryFn: listExchangeRates,
    enabled: session.isAdmin,
  })

  const rates = computed(() => query.data.value ?? [])
  const loaded = computed(() => query.isSuccess.value)
  const loading = computed(() => query.isLoading.value)
  const byCode = computed(() => {
    const map = new Map<string, ExchangeRateView>()
    for (const rate of rates.value) map.set(rate.code, rate)
    return map
  })

  const upsertMutation = useMutation({
    mutationFn: upsertExchangeRate,
    onSuccess: () => invalidateExchangeRates(client),
  })

  const removeMutation = useMutation({
    mutationFn: deleteExchangeRate,
    onSuccess: () => invalidateExchangeRates(client),
  })

  return {
    rates,
    loaded,
    loading,
    byCode,
    fetch: query.refetch,
    upsert: upsertMutation.mutateAsync,
    remove: removeMutation.mutateAsync,
    query,
    upsertMutation,
    removeMutation,
  }
}

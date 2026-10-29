import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { fetchMe } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'

// useMe exposes the current user and a derived isAdmin flag. isAdmin gates the
// admin navigation group, the router guard, and the short-circuit test mode in
// TestView. The backend remains the sole authority — admin operations return 403
// regardless of this flag.
export function useMe() {
  const query = useQuery({
    queryKey: queryKeys.me,
    queryFn: fetchMe,
  })
  const me = computed(() => query.data.value)
  const isAdmin = computed(() => me.value?.isAdmin ?? false)
  return { me, isAdmin, query }
}

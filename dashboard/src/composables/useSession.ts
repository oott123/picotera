import { computed } from 'vue'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { fetchMe, logout as apiLogout } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import type { components } from '@/openapi-types'

type Session = components['schemas']['SessionView']
export type Permission = keyof components['schemas']['Permissions']

/**
 * useSession exposes the currently authenticated session (or null) plus
 * convenience helpers for permission checks. Backed by a single vue-query
 * subscription — the query cache is shared across the app, so calling
 * useSession() from multiple components reuses one /me fetch.
 */
export function useSession() {
  const q = useQuery<Session | null>({
    queryKey: queryKeys.session.current,
    queryFn: fetchMe,
    retry: false,
    staleTime: 30_000,
  })

  return {
    user: computed(() => q.data.value ?? null),
    isPending: q.isPending,
    isError: q.isError,
    isAdmin: computed(() => q.data.value?.role === 'admin'),
    can(perm: Permission): boolean {
      const u = q.data.value
      if (!u) return false
      return u.role === 'admin' || !!u.permissions[perm]
    },
  }
}

/**
 * useSignOut returns a function that calls /auth/logout, clears the session
 * query cache, and (caller decides) navigates to /login. The composable
 * keeps the orchestration in one place so multiple views (sidebar avatar
 * menu, MeView's sign-out button) don't drift apart.
 */
export function useSignOut() {
  const qc = useQueryClient()
  return async () => {
    try {
      await apiLogout()
    } catch {
      // Idempotent on the server; ignore errors so sign-out always completes.
    }
    qc.removeQueries({ queryKey: queryKeys.session.all })
    qc.removeQueries({ queryKey: queryKeys.accounts.all })
    qc.removeQueries({ queryKey: queryKeys.credentials.all })
  }
}

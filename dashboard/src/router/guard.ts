import type { RouteLocationNormalized, NavigationGuardReturn } from 'vue-router'
import { queryClient } from '@/api/queryClient'
import { queryKeys } from '@/api/queryKeys'
import { fetchMe } from '@/api/client'
import type { components } from '@/openapi-types'
import { fallbackFor } from './fallback'

type SessionView = components['schemas']['SessionView']

export async function authGuard(
  to: RouteLocationNormalized,
): Promise<NavigationGuardReturn> {
  const auth = to.meta.auth
  if (auth.kind === 'public') return true

  // Populate cache (swallow 401 — getQueryData below will be undefined).
  await queryClient
    .ensureQueryData({
      queryKey: queryKeys.session.current,
      queryFn: fetchMe,
      retry: false,
    })
    .catch(() => null)
  const me = queryClient.getQueryData<SessionView>(queryKeys.session.current)
  if (!me) {
    return { path: '/login', query: { next: to.fullPath } }
  }
  if (auth.kind === 'session') return true
  if (auth.kind === 'admin') {
    if (me.role === 'admin') return true
    return fallbackFor(me)
  }
  // permission
  const ok = me.role === 'admin' || !!me.permissions[auth.perm]
  return ok ? true : fallbackFor(me)
}

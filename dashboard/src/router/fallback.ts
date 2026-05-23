import type { components } from '@/openapi-types'

type SessionView = components['schemas']['SessionView']

/**
 * Pick the best landing page for the given session. Admins go to /overview.
 * Otherwise we route to the most useful page their permissions allow.
 * Used by the route guard (denied admin/permission routes redirect here)
 * and by post-login / post-enroll flows so freshly authenticated non-admins
 * don't land on a page they'd be 403'd from.
 */
export function fallbackFor(me: SessionView): string {
  if (me.role === 'admin') return '/overview'
  if (me.permissions.view_own_usage) return '/requests'
  if (me.permissions.manage_own_api_keys) return '/api-keys'
  return '/me'
}

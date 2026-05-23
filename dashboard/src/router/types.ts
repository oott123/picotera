import 'vue-router'
import type { components } from '@/openapi-types'

type Permission = keyof components['schemas']['Permissions']

export type RouteAuth =
  | { kind: 'public' }
  | { kind: 'session' }
  | { kind: 'admin' }
  | { kind: 'permission'; perm: Permission }

declare module 'vue-router' {
  interface RouteMeta {
    auth: RouteAuth
    layout: 'app' | 'minimal'
  }
}

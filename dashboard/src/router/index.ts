import { createRouter, createWebHistory } from 'vue-router'
import { queryClient } from '@/api/queryClient'
import { queryKeys } from '@/api/queryKeys'
import { fetchMe } from '@/api/client'

// Admin-only route names. Mirrors AppSidebar's adminNav. The guard below
// redirects non-admins to /overview; the backend stays the sole authority, so
// any admin API a non-admin reaches anyway returns 403.
const ADMIN_ROUTES = new Set([
  'adminOverview',
  'providers',
  'models',
  'endpoints',
  'scripts',
  'kv',
  'rates',
  'users',
])

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', redirect: '/overview' },
    { path: '/overview', name: 'overview', component: () => import('@/views/OverviewView.vue') },
    {
      path: '/admin/overview',
      name: 'adminOverview',
      component: () => import('@/views/AdminOverviewView.vue'),
    },
    { path: '/providers', name: 'providers', component: () => import('@/views/ProvidersView.vue') },
    { path: '/models', name: 'models', component: () => import('@/views/ModelsView.vue') },
    { path: '/endpoints', name: 'endpoints', component: () => import('@/views/EndpointsView.vue') },
    { path: '/requests', name: 'requests', component: () => import('@/views/RequestsView.vue') },
    {
      path: '/requests/:requestId',
      name: 'requestDetail',
      component: () => import('@/views/RequestDetailView.vue'),
    },
    { path: '/traces', name: 'traces', component: () => import('@/views/TracesView.vue') },
    { path: '/api-keys', name: 'apiKeys', component: () => import('@/views/ApiKeysView.vue') },
    { path: '/users', name: 'users', component: () => import('@/views/UsersView.vue') },
    { path: '/projects', name: 'projects', component: () => import('@/views/ProjectsView.vue') },
    { path: '/scripts', name: 'scripts', component: () => import('@/views/ScriptsView.vue') },
    { path: '/test', name: 'test', component: () => import('@/views/TestView.vue') },
    { path: '/kv', name: 'kv', component: () => import('@/views/KvView.vue') },
    { path: '/rates', name: 'rates', component: () => import('@/views/RatesView.vue') },
    { path: '/settings', name: 'settings', component: () => import('@/views/SettingsView.vue') },
  ],
})

router.beforeEach(async (to) => {
  if (typeof to.name !== 'string' || !ADMIN_ROUTES.has(to.name)) return true
  // ensureQueryData resolves the cached /me (or fetches it) so the guard works
  // even before AppSidebar's useMe has populated the cache. /me errors (e.g.
  // 401) propagate as today — not handled here.
  const me = await queryClient.ensureQueryData({ queryKey: queryKeys.me, queryFn: fetchMe })
  return me.isAdmin ? true : { name: 'overview' }
})

export default router

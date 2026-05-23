import { createRouter, createWebHistory } from 'vue-router'
import './types'
import { authGuard } from './guard'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    { path: '/', redirect: '/overview' },
    {
      path: '/overview',
      name: 'overview',
      component: () => import('@/views/OverviewView.vue'),
      meta: { auth: { kind: 'permission', perm: 'view_own_usage' }, layout: 'app' },
    },
    {
      path: '/providers',
      name: 'providers',
      component: () => import('@/views/ProvidersView.vue'),
      meta: { auth: { kind: 'admin' }, layout: 'app' },
    },
    {
      path: '/models',
      name: 'models',
      component: () => import('@/views/ModelsView.vue'),
      meta: { auth: { kind: 'permission', perm: 'view_models' }, layout: 'app' },
    },
    {
      path: '/endpoints',
      name: 'endpoints',
      component: () => import('@/views/EndpointsView.vue'),
      meta: { auth: { kind: 'permission', perm: 'view_models' }, layout: 'app' },
    },
    {
      path: '/requests',
      name: 'requests',
      component: () => import('@/views/RequestsView.vue'),
      meta: { auth: { kind: 'permission', perm: 'view_own_usage' }, layout: 'app' },
    },
    {
      path: '/requests/:requestId',
      name: 'requestDetail',
      component: () => import('@/views/RequestDetailView.vue'),
      meta: { auth: { kind: 'permission', perm: 'view_own_usage' }, layout: 'app' },
    },
    {
      path: '/traces',
      name: 'traces',
      component: () => import('@/views/TracesView.vue'),
      meta: { auth: { kind: 'permission', perm: 'view_own_traces' }, layout: 'app' },
    },
    {
      path: '/api-keys',
      name: 'apiKeys',
      component: () => import('@/views/ApiKeysView.vue'),
      meta: { auth: { kind: 'permission', perm: 'manage_own_api_keys' }, layout: 'app' },
    },
    {
      path: '/projects',
      name: 'projects',
      component: () => import('@/views/ProjectsView.vue'),
      meta: { auth: { kind: 'admin' }, layout: 'app' },
    },
    {
      path: '/scripts',
      name: 'scripts',
      component: () => import('@/views/ScriptsView.vue'),
      meta: { auth: { kind: 'admin' }, layout: 'app' },
    },
    {
      path: '/simulate',
      name: 'simulate',
      component: () => import('@/views/SimulateView.vue'),
      meta: { auth: { kind: 'admin' }, layout: 'app' },
    },
    {
      path: '/kv',
      name: 'kv',
      component: () => import('@/views/KvView.vue'),
      meta: { auth: { kind: 'admin' }, layout: 'app' },
    },
    {
      path: '/rates',
      name: 'rates',
      component: () => import('@/views/RatesView.vue'),
      meta: { auth: { kind: 'admin' }, layout: 'app' },
    },
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/LoginView.vue'),
      meta: { auth: { kind: 'public' }, layout: 'minimal' },
    },
    {
      path: '/enroll/:token',
      name: 'enroll',
      component: () => import('@/views/EnrollView.vue'),
      meta: { auth: { kind: 'public' }, layout: 'minimal' },
    },
  ],
})

router.beforeEach(authGuard)

export default router

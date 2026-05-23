<script setup lang="ts">
import { computed, ref, useTemplateRef, watch, onBeforeUnmount } from 'vue'
import { useRoute, useRouter, RouterLink } from 'vue-router'
import { useFloating, offset, flip, shift, autoUpdate } from '@floating-ui/vue'
import PreferencesMenu from '@/components/PreferencesMenu.vue'
import Icon from '@/ui/icons/Icon.vue'
import type { IconName } from '@/ui/icons/paths'
import { useSession, useSignOut, type Permission } from '@/composables/useSession'

const route = useRoute()
const router = useRouter()
const session = useSession()
const signOut = useSignOut()

const activeRouteName = computed(() => {
  if (route.name === 'requestDetail') return 'requests'
  return route.name
})

type NavItem =
  | { name: string; label: string; icon: IconName; requires: 'admin' }
  | { name: string; label: string; icon: IconName; requires: { perm: Permission } }

const nav: NavItem[] = [
  { name: 'overview',   label: '概览', icon: 'chart-pie',       requires: { perm: 'view_own_usage' } },
  { name: 'providers',  label: '渠道', icon: 'cloud-fog',        requires: 'admin' },
  { name: 'models',     label: '模型', icon: 'cpu',              requires: { perm: 'view_models' } },
  { name: 'endpoints',  label: '端点', icon: 'plug',             requires: { perm: 'view_models' } },
  { name: 'requests',   label: '请求', icon: 'activity',         requires: { perm: 'view_own_usage' } },
  { name: 'traces',     label: '追踪', icon: 'route',            requires: { perm: 'view_own_traces' } },
  { name: 'apiKeys',    label: '密钥', icon: 'key',              requires: { perm: 'manage_own_api_keys' } },
  { name: 'projects',   label: '项目', icon: 'folder',           requires: 'admin' },
  { name: 'scripts',    label: '脚本', icon: 'braces',           requires: 'admin' },
  { name: 'simulate',   label: '模拟', icon: 'geometry',         requires: 'admin' },
  { name: 'kv',         label: '缓存', icon: 'db',               requires: 'admin' },
  { name: 'rates',      label: '汇率', icon: 'currency-dollar',  requires: 'admin' },
]

function isVisible(item: NavItem): boolean {
  // Admins see everything; non-admins are filtered to their granted permissions.
  if (session.isAdmin.value) return true
  if (item.requires === 'admin') return false
  return session.can(item.requires.perm)
}

const visibleNav = computed(() => nav.filter(isVisible))

// --- Profile menu (mirrors PreferencesMenu floating-ui pattern) ---

const profileOpen = ref(false)
const profileTriggerRef = useTemplateRef<HTMLElement>('profileTriggerRef')
const profileFloatingRef = useTemplateRef<HTMLElement>('profileFloatingRef')

const { floatingStyles: profileFloatingStyles } = useFloating(profileTriggerRef, profileFloatingRef, {
  placement: 'top-start',
  strategy: 'fixed',
  whileElementsMounted: autoUpdate,
  middleware: [offset(8), flip({ padding: 8 }), shift({ padding: 8 })],
})

function toggleProfile() {
  profileOpen.value = !profileOpen.value
}

function closeProfile() {
  profileOpen.value = false
}

function onProfileDocMouseDown(e: MouseEvent) {
  const t = e.target as Node
  if (profileFloatingRef.value?.contains(t)) return
  if (profileTriggerRef.value?.contains(t)) return
  closeProfile()
}

function onProfileKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') closeProfile()
}

watch(profileOpen, (v) => {
  if (v) {
    document.addEventListener('mousedown', onProfileDocMouseDown, true)
    document.addEventListener('keydown', onProfileKeydown)
  } else {
    document.removeEventListener('mousedown', onProfileDocMouseDown, true)
    document.removeEventListener('keydown', onProfileKeydown)
  }
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onProfileDocMouseDown, true)
  document.removeEventListener('keydown', onProfileKeydown)
})

const roleLabel = computed(() => {
  const role = session.user.value?.role
  if (role === 'admin') return '管理员'
  if (role === 'user') return '普通用户'
  return role ?? ''
})

async function onSignOut() {
  closeProfile()
  await signOut()
  router.replace('/login')
}
</script>

<template>
  <aside
    class="w-72 min-w-72 bg-sidebar-bg border-r border-line flex flex-col h-[100dvh] sticky top-0"
  >
    <div class="px-4 pt-[1.125rem] pb-4 flex items-center gap-2.5">
      <span
        class="inline-flex items-center justify-center w-[1.875rem] h-[1.875rem] bg-accent text-white rounded-md shadow-[inset_0_0_0_1px_oklch(1_0_0/0.12),0_1px_2px_oklch(0.3_0.1_262/0.25)]"
        aria-hidden="true"
      >
        <svg
          viewBox="0 0 24 24"
          width="18"
          height="18"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M4 7h10a4 4 0 0 1 0 8H8" />
          <path d="M8 4v16" />
        </svg>
      </span>
      <div class="flex flex-col leading-[1.15]">
        <span class="font-semibold text-[0.9375rem] tracking-[-0.01em] text-ink">PicoTera</span>
        <span class="font-mono text-2xs text-ink-faint">LLM gateway</span>
      </div>
    </div>

    <nav class="flex-1 px-2 py-1.5 pb-4 flex flex-col gap-px" aria-label="主导航">
      <div
        class="px-2.5 pt-3 pb-1.5 text-2xs font-medium text-ink-faint uppercase tracking-[0.06em]"
      >
        配置
      </div>
      <RouterLink
        v-for="item in visibleNav"
        :key="item.name"
        :to="{ name: item.name }"
        class="group relative flex items-center gap-2.5 px-2.5 py-2 rounded-md text-sm font-normal text-sidebar-text no-underline transition-colors hover:bg-sidebar-hover hover:text-sidebar-text-active"
        :class="
          activeRouteName === item.name
            ? 'bg-sidebar-active-bg text-sidebar-active-text font-medium'
            : ''
        "
      >
        <span
          class="inline-flex w-[1.125rem] h-[1.125rem] items-center justify-center transition-colors"
          :class="
            activeRouteName === item.name
              ? 'text-accent'
              : 'text-ink-faint group-hover:text-ink-muted'
          "
          aria-hidden="true"
        >
          <Icon :name="item.icon" :size="15" :stroke-width="1.6" />
        </span>
        <span>{{ item.label }}</span>
      </RouterLink>
    </nav>

    <div class="px-3.5 pt-2.5 pb-3 border-t border-line flex items-center justify-between gap-2">
      <PreferencesMenu />

      <!-- Account / profile trigger -->
      <button
        ref="profileTriggerRef"
        type="button"
        aria-label="账户"
        title="账户"
        :aria-expanded="profileOpen"
        aria-haspopup="menu"
        class="inline-flex items-center justify-center w-7 h-7 p-0 bg-transparent text-ink-muted border border-transparent rounded-md cursor-pointer transition-colors hover:bg-sidebar-hover hover:text-ink aria-expanded:bg-sidebar-active-bg aria-expanded:text-sidebar-active-text aria-expanded:border-line"
        @click="toggleProfile"
      >
        <Icon name="user" :size="14" />
      </button>
    </div>
  </aside>

  <!-- Profile popover — teleported to body so it escapes aside stacking context -->
  <Teleport to="body">
    <div
      v-if="profileOpen"
      ref="profileFloatingRef"
      class="w-56 p-1.5 bg-surface-0 border border-line rounded-xl shadow-lg z-[1000] text-ink"
      role="menu"
      :style="profileFloatingStyles"
    >
      <section class="px-2 pt-1.5 pb-2">
        <div class="text-sm font-medium text-ink truncate">
          {{ session.user.value?.username }}
        </div>
        <div class="text-2xs text-ink-faint">{{ roleLabel }}</div>
      </section>

      <hr class="m-0 h-px border-0 bg-line-soft" />

      <RouterLink
        to="/me"
        role="menuitem"
        class="flex items-center px-2 py-1.5 mt-1 rounded-md hover:bg-surface-50 text-sm text-ink no-underline w-full"
        @click="closeProfile"
      >
        我的账户
      </RouterLink>

      <button
        type="button"
        role="menuitem"
        class="flex items-center px-2 py-1.5 rounded-md hover:bg-surface-50 text-sm text-left w-full bg-transparent border-0 cursor-pointer text-ink"
        @click="onSignOut"
      >
        退出登录
      </button>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, RouterLink } from 'vue-router'
import { useQueryClient } from '@tanstack/vue-query'
import PreferencesMenu from '@/components/PreferencesMenu.vue'
import { useAppTitle } from '@/composables/useAppTitle'
import { useMe } from '@/composables/useMe'
import { useImpersonationStore } from '@/stores/impersonation'
import Icon from '@/ui/icons/Icon.vue'
import { IconButton, Tag } from '@/ui'
import type { IconName } from '@/ui/icons/paths'

const route = useRoute()
const queryClient = useQueryClient()
const { appTitle } = useAppTitle()

const { me, isAdmin } = useMe()
const impersonation = useImpersonationStore()

async function stopImpersonating() {
  impersonation.stop()
  queryClient.clear()
}
const refreshing = ref(false)
const activeRouteName = computed(() => {
  if (route.name === 'requestDetail') return 'requests'
  return route.name
})

type NavItem = { name: string; label: string; icon: IconName }

// User features: available to every authenticated user.
const userNav: NavItem[] = [
  { name: 'overview', label: '概览', icon: 'chart-pie' },
  { name: 'apiKeys', label: '密钥', icon: 'key' },
  { name: 'requests', label: '请求', icon: 'activity' },
  { name: 'traces', label: '追踪', icon: 'route' },
  { name: 'projects', label: '项目', icon: 'folder' },
  { name: 'test', label: '测试', icon: 'flask' },
  { name: 'settings', label: '设置', icon: 'settings' },
]

// Admin features: hidden entirely for non-admins (also guarded by the router).
const adminNav: NavItem[] = [
  { name: 'adminOverview', label: '全览', icon: 'chart-pie' },
  { name: 'providers', label: '渠道', icon: 'cloud-fog' },
  { name: 'models', label: '模型', icon: 'cpu' },
  { name: 'endpoints', label: '端点', icon: 'plug' },
  { name: 'scripts', label: '脚本', icon: 'braces' },
  { name: 'kv', label: '缓存', icon: 'db' },
  { name: 'rates', label: '汇率', icon: 'currency-dollar' },
  { name: 'users', label: '用户', icon: 'users' },
]
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
        <span class="font-semibold text-[0.9375rem] tracking-[-0.01em] text-ink">{{
          appTitle
        }}</span>
        <span class="font-mono text-2xs text-ink-faint">LLM gateway</span>
      </div>
    </div>

    <nav class="flex-1 px-2 py-1.5 pb-4 flex flex-col gap-px" aria-label="主导航">
      <div
        class="px-2.5 pt-3 pb-1.5 text-2xs font-medium text-ink-faint uppercase tracking-[0.06em]"
      >
        用户
      </div>
      <RouterLink
        v-for="item in userNav"
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

      <template v-if="isAdmin">
        <div
          class="px-2.5 pt-3 pb-1.5 text-2xs font-medium text-ink-faint uppercase tracking-[0.06em]"
        >
          全局
        </div>
        <RouterLink
          v-for="item in adminNav"
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
      </template>
    </nav>

    <div class="px-3.5 pt-2.5 pb-3 border-t border-line flex items-center gap-2">
      <div class="flex-1 min-w-0 flex items-center gap-1.5">
        <span
          class="min-w-0 truncate text-sm text-ink-muted"
          :title="
            (impersonation.isImpersonating ? impersonation.target?.displayName : me?.displayName) ??
            ''
          "
          >{{
            (impersonation.isImpersonating ? impersonation.target?.displayName : me?.displayName) ??
            ''
          }}</span
        >
        <Tag v-if="impersonation.isImpersonating" variant="accent" class="shrink-0">扮演中</Tag>
      </div>
      <IconButton
        v-if="impersonation.isImpersonating"
        title="还原身份"
        aria-label="还原身份"
        @click="stopImpersonating"
      >
        <Icon name="arrow-left" :size="14" />
      </IconButton>
      <PreferencesMenu />
      <button
        type="button"
        aria-label="刷新"
        title="刷新"
        :disabled="refreshing"
        class="shrink-0 inline-flex items-center justify-center w-7 h-7 p-0 bg-transparent text-ink-muted border border-transparent rounded-md cursor-pointer transition-colors hover:bg-sidebar-hover hover:text-ink disabled:opacity-50 disabled:cursor-not-allowed"
        :class="refreshing ? 'animate-spin' : ''"
        @click="async () => {
          refreshing = true
          await queryClient.invalidateQueries()
          refreshing = false
        }"
      >
        <Icon name="refresh" :size="14" />
      </button>
    </div>
  </aside>
</template>

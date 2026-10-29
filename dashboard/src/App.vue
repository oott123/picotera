<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, RouterView } from 'vue-router'
import AppSidebar from '@/components/AppSidebar.vue'
import SidePanelHost from '@/components/SidePanelHost.vue'
import ConfirmDialog from '@/ui/ConfirmDialog.vue'
import { useExchangeRates } from '@/composables/useExchangeRates'
import { provideCurrencyContext } from '@/composables/useCurrencyContext'
import { usePreferencesStore } from '@/stores/preferences'

const route = useRoute()
const prefs = usePreferencesStore()
useExchangeRates()
provideCurrencyContext(computed(() => prefs.displayCurrency ?? null))

const pageMeta = computed(() => {
  const map: Record<string, { title: string; hint: string }> = {
    overview: { title: '概览', hint: '今天蹬了多少刀' },
    adminOverview: { title: '全局概览', hint: '众生皆蹬刀' },
    providers: { title: '渠道', hint: '词元从四面八方来' },
    models: { title: '模型', hint: '统统流口水' },
    endpoints: { title: '端点', hint: '跪求秦始皇统一接口格式' },
    requests: { title: '请求', hint: '洞悉深渊的秘密' },
    requestDetail: { title: '请求', hint: '洞悉深渊的秘密' },
    traces: { title: '追踪', hint: '今天都干啥了' },
    apiKeys: { title: '密钥', hint: '上网不涉密，涉密不上网' },
    users: { title: '用户', hint: '同志们辛苦了' },
    projects: { title: '项目', hint: '开坑不填天理难容' },
    scripts: { title: '脚本', hint: '这是图灵完备的' },
    test: { title: '测试', hint: '不试试咋知道行不行' },
    kv: { title: '缓存', hint: '研究状态科学' },
    rates: { title: '汇率', hint: '掌控国际形势' },
    settings: { title: '设置', hint: '总得有个设置吧' },
  }
  return map[route.name as string] ?? { title: '', hint: '' }
})
</script>

<template>
  <div class="flex min-h-[100dvh] bg-surface-50">
    <AppSidebar />
    <main class="flex-1 flex flex-col min-w-0">
      <header class="px-8 pt-[1.125rem] pb-3.5">
        <div class="flex flex-col gap-0.5">
          <h1 class="text-xl font-semibold tracking-[-0.015em] text-ink m-0 leading-[1.2]">
            {{ pageMeta.title }}
          </h1>
          <p class="m-0 text-sm text-ink-faint leading-[1.2]">{{ pageMeta.hint }}</p>
        </div>
      </header>
      <div class="flex-1 flex min-h-0 min-w-0">
        <div class="flex-1 min-w-0 px-8 pt-3 pb-8 overflow-y-auto">
          <RouterView />
        </div>
        <SidePanelHost />
      </div>
    </main>
  </div>
  <ConfirmDialog />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import AppSidebar from '@/components/AppSidebar.vue'
import SidePanelHost from '@/components/SidePanelHost.vue'

const route = useRoute()

const pageMeta = computed(() => {
  const map: Record<string, { title: string; hint: string }> = {
    overview: { title: '概览', hint: '今天蹬了多少刀' },
    providers: { title: '渠道', hint: '今天上哪去蹬' },
    models: { title: '模型', hint: '今天都蹬些什么' },
    endpoints: { title: '端点', hint: '今天都用什么格式蹬' },
    requests: { title: '请求', hint: '今天都怎么蹬的' },
    requestDetail: { title: '请求', hint: '今天都怎么蹬的' },
    traces: { title: '追踪', hint: '今天都蹬了哪些事' },
    apiKeys: { title: '密钥', hint: '今天用什么蹬' },
    projects: { title: '项目', hint: '今天蹬到哪里去了' },
    scripts: { title: '脚本', hint: '今天蹬点什么科技' },
    simulate: { title: '模拟', hint: '不蹬也知道蹬到哪' },
    kv: { title: 'KV 存储', hint: '今天存了些什么' },
    rates: { title: '汇率', hint: '今天都蹬什么钱' },
    me: { title: '我的账户', hint: '今天蹬的是自己' },
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
          <slot />
        </div>
        <SidePanelHost />
      </div>
    </main>
  </div>
</template>

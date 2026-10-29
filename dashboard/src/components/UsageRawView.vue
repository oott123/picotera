<script setup lang="ts">
import { computed } from 'vue'
import { StateText } from '@/ui'

// The upstream's own usage / tool_usage objects, of whatever shape that upstream
// reports. They are drill-down material for troubleshooting billing — the
// overview tab's Token / 工具用量 sections carry the normalized summary.
const props = defineProps<{
  usageRaw?: Record<string, unknown> | null
  toolUsageRaw?: Record<string, unknown> | null
}>()

const usageText = computed(() => (props.usageRaw ? JSON.stringify(props.usageRaw, null, 2) : ''))
const toolUsageText = computed(() =>
  props.toolUsageRaw ? JSON.stringify(props.toolUsageRaw, null, 2) : '',
)
const empty = computed(() => !usageText.value && !toolUsageText.value)
</script>

<template>
  <StateText v-if="empty" :dashed="false" compact>无用量数据</StateText>
  <div v-else class="flex flex-col gap-4">
    <section v-if="usageText" class="flex flex-col gap-2.5">
      <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]">原始用量</span>
      <pre
        class="font-mono text-xs whitespace-pre-wrap bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink max-h-96 overflow-auto"
        >{{ usageText }}</pre
      >
    </section>
    <section v-if="toolUsageText" class="flex flex-col gap-2.5">
      <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
        >原始工具用量</span
      >
      <pre
        class="font-mono text-xs whitespace-pre-wrap bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink max-h-96 overflow-auto"
        >{{ toolUsageText }}</pre
      >
    </section>
  </div>
</template>

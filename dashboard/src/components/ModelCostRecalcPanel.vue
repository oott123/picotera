<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import type { ModelView, RecalculateModelCostsResponseBody } from '@/api'
import { invalidateRequestCosts, recalculateModelCosts } from '@/api/client'
import { Button, Field, Select, SidePanel } from '@/ui'

const props = defineProps<{ model: ModelView }>()
const emit = defineEmits<{ close: [] }>()

type CostRecalcRange = '24h' | '168h' | '720h' | ''

const queryClient = useQueryClient()
const range = ref<CostRecalcRange>('168h')
const result = ref<RecalculateModelCostsResponseBody | null>(null)

// Wire values are Go durations (time.ParseDuration), which have no day unit —
// days are pre-converted to hours and the empty string means "the whole history".
const rangeOptions: { value: CostRecalcRange; label: string }[] = [
  { value: '24h', label: '24 小时' },
  { value: '168h', label: '7 天' },
  { value: '720h', label: '30 天' },
  { value: '', label: '全部' },
]

const mutation = useMutation({
  mutationFn: () => recalculateModelCosts({ name: props.model.name, range: range.value }),
  onSuccess: (data) => {
    result.value = data
    invalidateRequestCosts(queryClient)
  },
})

const running = computed(() => mutation.isPending.value)
const error = computed(() => (mutation.error.value as Error | null)?.message ?? '')
</script>

<template>
  <SidePanel :title="model.name" kicker="重算历史费用" @close="emit('close')">
    <Field label="时间区间" as="div">
      <Select
        :model-value="range"
        :options="rangeOptions"
        :searchable="false"
        @update:model-value="(v) => (range = String(v) as CostRecalcRange)"
      />
    </Field>
    <p v-if="running" class="m-0 bg-surface-50 text-ink-muted text-xs rounded-md px-3 py-2">
      重算中…请勿关闭页面。
    </p>
    <p v-else-if="result" class="m-0 bg-ok-faint text-ok-ink text-xs rounded-md px-3 py-2">
      已重算 {{ result.updated }} 条请求（耗时 {{ (result.tookMs / 1000).toFixed(1) }} 秒）。
    </p>
    <template #error v-if="error">{{ error }}</template>
    <template #footer>
      <Button variant="ghost" :disabled="running" @click="emit('close')">关闭</Button>
      <Button :disabled="running" @click="mutation.mutate()">
        {{ running ? '重算中…' : '开始重算' }}
      </Button>
    </template>
  </SidePanel>
</template>

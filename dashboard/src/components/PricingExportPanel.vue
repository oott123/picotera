<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ModelView } from '@/api'
import { useExchangeRates } from '@/composables/useExchangeRates'
import { buildPricingExpression } from '@/utils/pricingExpression'
import { Button, Icon, Select, SidePanel } from '@/ui'

const props = defineProps<{ model: ModelView }>()
const emit = defineEmits<{ close: [] }>()

const { rates, byCode } = useExchangeRates()

const targetCurrency = ref(props.model.pricing?.currency ?? 'USD')

const currencyOptions = computed(() =>
  rates.value.map((r) => ({ value: r.code, label: `${r.code} ${r.symbol}` })),
)

const result = computed(() =>
  buildPricingExpression(props.model.pricing!, targetCurrency.value, byCode.value),
)

const copied = ref(false)
let copyTimer: ReturnType<typeof setTimeout> | null = null

async function copy() {
  if (result.value.error || !result.value.expression) return
  try {
    await navigator.clipboard.writeText(result.value.expression)
    copied.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => {
      copied.value = false
    }, 1500)
  } catch {
    // clipboard unavailable — silently ignore
  }
}
</script>

<template>
  <SidePanel :title="model.name" kicker="导出价格" @close="emit('close')">
    <label class="flex flex-col gap-1">
      <span class="text-2xs text-ink-faint">目标币种</span>
      <Select
        :model-value="targetCurrency"
        size="sm"
        class="min-w-[8rem]"
        :options="currencyOptions"
        @update:model-value="(v) => (targetCurrency = String(v))"
      />
    </label>

    <div
      v-for="(w, i) in result.warnings"
      :key="i"
      class="bg-warn-faint text-warn-ink text-xs rounded-md px-3 py-2"
    >
      {{ w }}
    </div>

    <pre
      v-if="result.expression"
      class="font-mono text-xs whitespace-pre-wrap break-all bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink overflow-auto max-h-[480px]"
    >{{ result.expression }}</pre>

    <template v-if="result.error" #error>{{ result.error }}</template>

    <template #footer>
      <Button
        variant="ghost"
        :disabled="!!result.error || !result.expression"
        @click="copy"
      >
        <Icon :name="copied ? 'check' : 'copy'" :size="13" />
        <span>{{ copied ? '已复制' : '复制表达式' }}</span>
      </Button>
      <Button variant="primary" @click="emit('close')">关闭</Button>
    </template>
  </SidePanel>
</template>

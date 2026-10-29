<script setup lang="ts">
import { computed } from 'vue'
import type { Pricing, PricingTier } from '@/api'
import { useExchangeRates } from '@/composables/useExchangeRates'
import { Button, IconButton, Input, Select, Icon } from '@/ui'

const props = defineProps<{
  modelValue: Pricing | null | undefined
}>()
const emit = defineEmits<{
  'update:modelValue': [value: Pricing | null]
}>()

const { rates } = useExchangeRates()

const tiers = computed<PricingTier[]>(() => props.modelValue?.tiers ?? [])
const currency = computed(() => props.modelValue?.currency ?? 'USD')

const currencyOptions = computed(() =>
  rates.value.map((r) => ({ value: r.code, label: `${r.code} ${r.symbol}` })),
)
const enabled = computed(() => tiers.value.length > 0)
const isMultiTier = computed(() => tiers.value.length >= 2)

function emitUpdate(next: Pricing | null) {
  emit('update:modelValue', next)
}

function blankTier(minInputTokens: number): PricingTier {
  return {
    minInputTokens,
    input: 0,
    output: 0,
    cacheRead: 0,
    cacheWrite: 0,
    cacheWrite1h: 0,
    implicitCacheRead: 0,
  }
}

function ensurePricing(): Pricing & { tiers: PricingTier[] } {
  return {
    currency: currency.value,
    tiers: tiers.value.map((t) => ({ ...t })),
  }
}

function enablePricing() {
  emitUpdate({ currency: currency.value || 'USD', tiers: [blankTier(0)] })
}

function clearPricing() {
  emitUpdate(null)
}

function setCurrency(value: string) {
  if (!props.modelValue) return
  emitUpdate({ ...ensurePricing(), currency: value })
}

function updateTier(index: number, patch: Partial<PricingTier>) {
  const next = ensurePricing()
  next.tiers[index] = { ...next.tiers[index], ...patch } as PricingTier
  emitUpdate(next)
}

function addTier() {
  const next = ensurePricing()
  const lastMin = next.tiers[next.tiers.length - 1]?.minInputTokens ?? 0
  next.tiers.push(blankTier(Number(lastMin) + 1))
  emitUpdate(next)
}

function removeTier(index: number) {
  if (index === 0) return
  const next = ensurePricing()
  next.tiers.splice(index, 1)
  emitUpdate(next)
}

const numericFields: { key: keyof PricingTier; label: string }[] = [
  { key: 'input', label: '输入' },
  { key: 'output', label: '输出' },
  { key: 'cacheRead', label: '缓存读取' },
  { key: 'cacheWrite', label: '缓存写入' },
  { key: 'cacheWrite1h', label: '1h 缓存写入' },
  { key: 'implicitCacheRead', label: '隐式缓存读取' },
]
</script>

<template>
  <div class="flex flex-col gap-2">
    <div v-if="!enabled" class="flex items-center gap-2">
      <Button type="button" variant="ghost" size="sm" @click="enablePricing">
        <Icon name="plus" :size="13" />
        <span>添加定价</span>
      </Button>
      <span class="text-xs text-ink-faint">未定价</span>
    </div>
    <template v-else>
      <div class="flex items-center gap-2">
        <Select
          :model-value="currency"
          size="sm"
          class="min-w-[8rem]"
          :options="currencyOptions"
          @update:model-value="(v) => setCurrency(String(v))"
        />
        <span class="text-2xs text-ink-faint">每百万 tokens</span>
        <span v-if="isMultiTier" class="text-2xs font-medium text-accent"
          >阶梯定价 · {{ tiers.length }} 档</span
        >
        <span class="flex-1" />
        <Button type="button" variant="ghost" size="sm" @click="clearPricing">
          <Icon name="trash" :size="13" />
          <span>移除定价</span>
        </Button>
      </div>
      <ul class="list-none m-0 p-0 flex flex-col gap-2">
        <li
          v-for="(tier, idx) in tiers"
          :key="idx"
          class="flex flex-col gap-1.5 px-2.5 py-2 border border-line rounded-md bg-surface-0"
        >
          <div class="flex items-center gap-2">
            <span class="text-2xs text-ink-faint w-10">阶梯</span>
            <span class="text-xs font-medium text-ink-muted">≥</span>
            <Input
              :model-value="tier.minInputTokens"
              type="number"
              size="sm"
              :disabled="idx === 0"
              min="0"
              class="w-32"
              @update:model-value="(v) => updateTier(idx, { minInputTokens: Number(v) })"
            />
            <span class="text-2xs text-ink-faint">输入 tokens</span>
            <span class="flex-1" />
            <IconButton
              variant="danger"
              :disabled="idx === 0"
              :title="idx === 0 ? '首档不可删除' : '删除该档'"
              aria-label="删除阶梯"
              @click="removeTier(idx)"
            >
              <Icon name="trash" :size="13" />
            </IconButton>
          </div>
          <div class="grid grid-cols-3 gap-2">
            <label v-for="f in numericFields" :key="f.key" class="flex flex-col gap-0.5">
              <span class="text-2xs text-ink-faint">{{ f.label }}</span>
              <Input
                :model-value="tier[f.key]"
                type="number"
                size="sm"
                step="0.0001"
                min="0"
                @update:model-value="
                  (v) => updateTier(idx, { [f.key]: Number(v) } as Partial<PricingTier>)
                "
              />
            </label>
          </div>
        </li>
      </ul>
      <div>
        <Button type="button" variant="ghost" size="sm" @click="addTier">
          <Icon name="plus" :size="13" />
          <span>增加阶梯</span>
        </Button>
      </div>
    </template>
  </div>
</template>

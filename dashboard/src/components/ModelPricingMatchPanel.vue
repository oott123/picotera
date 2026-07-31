<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { ModelView, PricingMatchCandidate, PricingTier } from '@/api'
import { invalidateModels, matchPricing, upsertModel } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { useCurrencyContext } from '@/composables/useCurrencyContext'
import {
  Button,
  DataTable,
  MoneyDisplay,
  SidePanel,
  StateText,
  Td,
  Th,
  Tr,
  Icon,
} from '@/ui'

const props = defineProps<{
  model: ModelView
  onSave?: () => void
}>()

const emit = defineEmits<{ close: [] }>()

const queryClient = useQueryClient()
const error = ref('')
const selectedIndex = ref(0)
const candidatesQuery = useQuery({
  queryKey: queryKeys.pricingMatches.model(props.model.name),
  queryFn: () => matchPricing(props.model.name),
})
const saveMutation = useMutation({
  mutationFn: upsertModel,
  onSuccess: () => invalidateModels(queryClient),
})
const candidates = computed<PricingMatchCandidate[]>(() => candidatesQuery.data.value ?? [])
const loading = computed(() => candidatesQuery.isLoading.value || candidatesQuery.isFetching.value)
const saving = computed(() => saveMutation.isPending.value)

const selected = computed(() => candidates.value[selectedIndex.value] ?? null)
const currentPricing = computed(() => {
  const pricing = props.model.pricing
  return pricing?.tiers?.length ? pricing : null
})
const hasCurrentPricing = computed(() => currentPricing.value !== null)
const comparisonTiers = computed(() => {
  const currentTiers = currentPricing.value?.tiers ?? []
  const targetTiers = selected.value?.pricing.tiers ?? []
  const minInputTokens = Array.from(
    new Set([
      ...currentTiers.map((tier) => tier.minInputTokens),
      ...targetTiers.map((tier) => tier.minInputTokens),
    ]),
  ).sort((a, b) => a - b)
  return minInputTokens.map((minTokens) => ({
    current: currentTiers.find((tier) => tier.minInputTokens === minTokens) ?? null,
    target: targetTiers.find((tier) => tier.minInputTokens === minTokens) ?? null,
  }))
})

const priceFields: { key: keyof PricingTier; label: string }[] = [
  { key: 'input', label: '输入' },
  { key: 'output', label: '输出' },
  { key: 'cacheRead', label: '缓存读取' },
  { key: 'cacheWrite', label: '缓存写入' },
  { key: 'cacheWrite1h', label: '1h 缓存写入' },
  { key: 'implicitCacheRead', label: '隐式缓存读取' },
]
const currency = useCurrencyContext()

function hasEqualPrices(keyA: keyof PricingTier, keyB: keyof PricingTier) {
  const pricingList = [currentPricing.value, selected.value?.pricing].filter(
    (pricing): pricing is NonNullable<typeof pricing> => pricing != null,
  )
  return pricingList.every((pricing) =>
    (pricing.tiers ?? []).every((tier) => tier[keyA] === tier[keyB]),
  )
}

const visiblePriceFields = computed(() =>
  priceFields.filter((field) => {
    if (field.key === 'cacheWrite1h') return !hasEqualPrices('cacheWrite', field.key)
    if (field.key === 'implicitCacheRead') return !hasEqualPrices('cacheRead', field.key)
    return true
  }),
)

type PriceComparison = 'higher' | 'lower' | 'same' | 'none'

function comparePrice(
  current: PricingTier | null,
  target: PricingTier | null,
  key: keyof PricingTier,
): PriceComparison {
  if (!current || !target || !currentPricing.value || !selected.value?.pricing) return 'none'
  const currentValue = current[key]
  const targetValue = target[key]
  const converted = currency.convertTo(
    currentValue,
    currentPricing.value.currency,
    selected.value.pricing.currency,
  )
  if (
    currentPricing.value.currency !== selected.value.pricing.currency &&
    !converted.converted
  ) {
    return 'none'
  }
  if (targetValue > converted.amount) return 'higher'
  if (targetValue < converted.amount) return 'lower'
  return 'same'
}

function formatTierThreshold(tier: { current: PricingTier | null; target: PricingTier | null }) {
  const current = tier.current?.minInputTokens
  const target = tier.target?.minInputTokens
  if (current != null && current === target) return `≥ ${current.toLocaleString()} 输入 tokens`
  return `当前 ≥ ${current?.toLocaleString() ?? '—'} · 目标 ≥ ${target?.toLocaleString() ?? '—'} 输入 tokens`
}

type DiffSegment = {
  text: string
  kind: 'same' | 'insert' | 'delete'
}

function diffModelName(candidateName: string): DiffSegment[] {
  const from = Array.from(props.model.name)
  const to = Array.from(candidateName)
  const width = to.length + 1
  const dp = Array((from.length + 1) * width).fill(0) as number[]
  const at = (i: number, j: number) => dp[i * width + j] ?? 0
  const set = (i: number, j: number, value: number) => {
    dp[i * width + j] = value
  }

  for (let i = 0; i <= from.length; i++) set(i, 0, i)
  for (let j = 0; j <= to.length; j++) set(0, j, j)

  for (let i = 1; i <= from.length; i++) {
    for (let j = 1; j <= to.length; j++) {
      if (from[i - 1] === to[j - 1]) {
        set(i, j, at(i - 1, j - 1))
      } else {
        set(i, j, Math.min(at(i - 1, j), at(i, j - 1)) + 1)
      }
    }
  }

  const out: DiffSegment[] = []
  let i = from.length
  let j = to.length
  while (i > 0 || j > 0) {
    const fromChar = i > 0 ? from[i - 1] : undefined
    const toChar = j > 0 ? to[j - 1] : undefined
    if (fromChar !== undefined && toChar !== undefined && fromChar === toChar) {
      pushDiff(out, fromChar, 'same')
      i--
      j--
    } else if (toChar !== undefined && (i === 0 || at(i, j - 1) <= at(i - 1, j))) {
      pushDiff(out, toChar, 'insert')
      j--
    } else if (fromChar !== undefined) {
      pushDiff(out, fromChar, 'delete')
      i--
    } else {
      break
    }
  }

  return out.reverse()
}

function pushDiff(out: DiffSegment[], text: string, kind: DiffSegment['kind']) {
  const last = out[out.length - 1]
  if (last?.kind === kind) {
    last.text = text + last.text
    return
  }
  out.push({ text, kind })
}

async function load() {
  error.value = ''
  try {
    const res = await candidatesQuery.refetch()
    if (res.error) throw res.error
    selectedIndex.value = candidates.value.length ? 0 : -1
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '匹配价格失败'
  }
}

async function save() {
  if (!selected.value) return
  error.value = ''
  const body = {
    name: props.model.name,
    disabled: props.model.disabled ?? false,
    annotations: props.model.annotations ?? {},
    pricing: selected.value.pricing,
  }
  try {
    await saveMutation.mutateAsync(body)
    props.onSave?.()
    emit('close')
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '保存价格失败'
  }
}
</script>

<template>
  <SidePanel :title="model.name" kicker="匹配价格" @close="emit('close')">
    <section class="flex flex-col gap-2">
      <div class="flex items-baseline justify-between">
        <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]">候选</span>
        <span class="text-xs text-ink-faint tabular-nums">{{ candidates.length }}</span>
      </div>

      <StateText v-if="loading" compact>匹配中…</StateText>
      <StateText v-else-if="!candidates.length" compact>没有找到可用价格候选</StateText>
      <DataTable v-else>
        <thead>
          <tr>
            <Th>模型</Th>
            <Th>供应商</Th>
          </tr>
        </thead>
        <tbody>
          <Tr
            v-for="(candidate, idx) in candidates"
            :key="`${candidate.providerId}:${candidate.modelId}`"
            :selected="idx === selectedIndex"
            class="cursor-pointer"
            @click="selectedIndex = idx"
          >
            <Td>
              <div class="font-mono text-xs text-ink whitespace-normal break-all leading-[1.55]">
                <span
                  v-for="(segment, segmentIndex) in diffModelName(candidate.modelId)"
                  :key="segmentIndex"
                  :class="[
                    segment.kind === 'insert' ? 'bg-ok-faint text-ok-ink px-0.5 rounded-xs' : '',
                    segment.kind === 'delete'
                      ? 'bg-err-faint text-err-ink line-through px-0.5 rounded-xs'
                      : '',
                  ]"
                  >{{ segment.text }}</span
                >
              </div>
            </Td>
            <Td>
              <span class="font-medium">{{ candidate.providerName }}</span>
            </Td>
          </Tr>
        </tbody>
      </DataTable>
    </section>

    <section v-if="selected" class="flex flex-col gap-2 mt-4">
      <div class="flex items-baseline justify-between">
        <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]">价格对比</span>
        <span class="text-2xs text-ink-faint">{{ selected.pricing.currency }}</span>
      </div>
      <DataTable>
        <thead>
          <tr>
            <Th>价格项目</Th>
            <Th v-if="hasCurrentPricing">当前价格 · {{ currentPricing?.currency }}</Th>
            <Th>目标价格 · {{ selected.pricing.currency }}</Th>
          </tr>
        </thead>
        <tbody>
          <template v-for="(tier, tierIndex) in comparisonTiers" :key="tierIndex">
            <tr>
              <td
                :colspan="hasCurrentPricing ? 3 : 2"
                class="px-4 pt-3 pb-1 text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
              >
                阶梯 {{ tierIndex + 1 }} · {{ formatTierThreshold(tier) }}
              </td>
            </tr>
            <tr v-for="field in visiblePriceFields" :key="`${tierIndex}:${field.key}`">
              <Td>{{ field.label }}</Td>
              <Td v-if="hasCurrentPricing">
                <MoneyDisplay
                  :amount="tier.current?.[field.key] ?? null"
                  :currency="currentPricing?.currency"
                  :max-digits="6"
                />
              </Td>
              <Td>
                <span
                  :class="{
                    'text-err-ink': comparePrice(tier.current, tier.target, field.key) === 'higher',
                    'text-ok-ink': comparePrice(tier.current, tier.target, field.key) === 'lower',
                  }"
                >
                  <MoneyDisplay
                    :amount="tier.target?.[field.key] ?? null"
                    :currency="selected.pricing.currency"
                    :max-digits="6"
                  />
                </span>
              </Td>
            </tr>
          </template>
        </tbody>
      </DataTable>
    </section>

    <template v-if="error" #error>{{ error }}</template>

    <template #footer>
      <Button variant="ghost" @click="emit('close')">取消</Button>
      <Button variant="ghost" :disabled="loading || saving" @click="load">
        <Icon
          :name="loading ? 'loader' : 'refresh'"
          :size="13"
          :class="loading ? 'animate-spin' : ''"
        />
        <span>重新匹配</span>
      </Button>
      <Button :disabled="loading || saving || !selected" @click="save">
        {{ saving ? '保存中…' : '保存价格' }}
      </Button>
    </template>
  </SidePanel>
</template>

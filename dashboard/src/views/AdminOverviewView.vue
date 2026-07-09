<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import {
  getAdminOverviewDistribution,
  getAdminOverviewSeries,
  getAdminOverviewSpeedBoxplot,
  getAdminOverviewSummary,
  listModelLabels,
  listProviderLabels,
  listUpstreamModelLabels,
  listUsers,
} from '@/api/client'
import { OPERATIONAL_STALE_TIME } from '@/api/queryClient'
import {
  queryKeys,
  type AdminOverviewFilters,
  type OverviewGranularity,
} from '@/api/queryKeys'
import type {
  AdminOverviewBreakdownRowView,
  AdminOverviewDimension,
  AdminOverviewSeriesDimension,
  OverviewRange,
  OverviewSpeedBoxplotItemView,
  OverviewSeriesPointView,
} from '@/api'
import {
  Button,
  DataCard,
  DynamicFilterBar,
  Icon,
  MoneyDisplay,
  SegmentedControl,
  Select,
  StateText,
  TimeRangeFilter,
} from '@/ui'
import { provideCurrencyContext, useCurrencyContext } from '@/composables/useCurrencyContext'
import { usePreferencesStore } from '@/stores/preferences'
import OverviewDonut from '@/components/charts/OverviewDonut.vue'
import OverviewAreaStack from '@/components/charts/OverviewAreaStack.vue'
import OverviewLineChart from '@/components/charts/OverviewLineChart.vue'
import OverviewSpeedTimeline from '@/components/charts/OverviewSpeedTimeline.vue'
import OverviewSankey, {
  type SankeyLink,
  type SankeyNode,
} from '@/components/charts/OverviewSankey.vue'

const prefs = usePreferencesStore()
const filters = reactive({
  range: '1d' as OverviewRange,
  startAt: '',
  endAt: '',
  userId: 0,
  model: '',
  upstreamModel: '',
  providerId: 0,
})
const granularity = ref<OverviewGranularity>('auto')
const visibleFilters = ref<string[]>([])

const availableFilters = [
  { key: 'user', label: '用户' },
  { key: 'model', label: '请求模型' },
  { key: 'upstreamModel', label: '上游模型' },
  { key: 'provider', label: '渠道' },
]

function onRemoveFilter(key: string) {
  switch (key) {
    case 'user':
      filters.userId = 0
      break
    case 'model':
      filters.model = ''
      break
    case 'upstreamModel':
      filters.upstreamModel = ''
      break
    case 'provider':
      filters.providerId = 0
      break
  }
}

const distributionDimension = ref<AdminOverviewDimension>('user')
const seriesDimension = ref<AdminOverviewSeriesDimension>('none')
const speedDimension = ref<AdminOverviewSeriesDimension>('model')
const cacheHitRateDimension = ref<AdminOverviewSeriesDimension>('model')

type SankeyVariant = 'tokenComposition' | 'tokensIn' | 'tokensOut' | 'costIn' | 'costOut'

const sankeyVariant = ref<SankeyVariant>('tokenComposition')
const sankeyVariantOptions: { value: SankeyVariant; label: string }[] = [
  { value: 'tokenComposition', label: '词元类型' },
  { value: 'tokensIn', label: '词元渠道' },
  { value: 'tokensOut', label: '词元用户' },
  { value: 'costIn', label: '费用渠道' },
  { value: 'costOut', label: '费用用户' },
]

const sankeyHiddenLayers = ref<Record<SankeyVariant, number[]>>({
  tokenComposition: [],
  tokensIn: [],
  tokensOut: [],
  costIn: [],
  costOut: [],
})

const sankeyLayerLabels: Record<SankeyVariant, string[]> = {
  tokenComposition: ['总 Token', '输入/输出', '细分'],
  tokensIn: ['总 Token', '渠道', '上游模型', '请求模型', '用户'],
  tokensOut: ['总 Token', '用户', '请求模型', '上游模型', '渠道'],
  costIn: ['总费用', '渠道', '上游模型', '请求模型', '用户'],
  costOut: ['总费用', '用户', '请求模型', '上游模型', '渠道'],
}

function getSankeyHiddenForBuild(variant: SankeyVariant): number[] {
  return sankeyHiddenLayers.value[variant].filter((i) => i > 0).map((i) => i - 1)
}

function toggleSankeyLayer(index: number) {
  const variant = sankeyVariant.value
  const dataLayers = sankeyDataLayerIndices.value[variant]
  // Prune any hidden indices that no longer have data
  const arr = [...new Set(sankeyHiddenLayers.value[variant])].filter((i) => dataLayers.has(i))
  const pos = arr.indexOf(index)
  if (pos !== -1) {
    arr.splice(pos, 1)
  } else {
    // Count visible non-root DATA layers
    let visibleNonRootCount = 0
    for (const i of dataLayers) {
      if (i > 0 && !arr.includes(i)) visibleNonRootCount++
    }
    if (visibleNonRootCount <= 1) {
      if (arr.length === 0) return
      arr.shift()
    }
    arr.push(index)
  }
  sankeyHiddenLayers.value = {
    ...sankeyHiddenLayers.value,
    [variant]: arr,
  }
}

const parentCurrency = useCurrencyContext()
const overviewCurrencyValue = computed({
  get: () => prefs.overviewCurrencyOverride ?? '',
  set: (v: string) => {
    prefs.overviewCurrencyOverride = v ? v : null
  },
})
const overviewTargetCurrency = computed(() => {
  if (prefs.overviewCurrencyOverride === 'original') return null
  return prefs.overviewCurrencyOverride ?? parentCurrency.targetCurrency.value
})
const ccy = provideCurrencyContext(overviewTargetCurrency)
const isOriginalMode = computed(() => ccy.targetCurrency.value == null)
const overviewCurrencyRates = computed(() => ccy.rates.value)

const overviewCurrencyOptions = computed(() => [
  { value: '', label: '跟随设置' },
  { value: 'original', label: '原始货币' },
  ...overviewCurrencyRates.value.map((r) => ({
    value: r.code,
    label: `${r.code} ${r.symbol}`,
    hint: r.name,
  })),
])

const userOptions = computed(() => [
  { value: 0, label: '全部' },
  ...users.value.map((u) => ({ value: u.id, label: u.displayName })),
])

const providerOptions = computed(() => [
  { value: 0, label: '全部' },
  ...providers.value.map((p) => ({ value: p.id, label: p.name })),
])

const modelSelectOptions = computed(() => [
  { value: '', label: '全部' },
  ...modelOptions.value.map((m) => ({ value: m, label: m })),
])

const upstreamModelSelectOptions = computed(() => [
  { value: '', label: '全部' },
  ...upstreamModelOptions.value.map((u) => ({ value: u, label: u })),
])
const rangeOptions: { value: OverviewRange; label: string }[] = [
  { value: '1d', label: '24 小时' },
  { value: '7d', label: '7 天' },
  { value: '1m', label: '30 天' },
  { value: 'custom', label: '自定义' },
]

// 自定义窗口跨度；未填齐两端时返回 0（视作允许 10m，避免选时间过程中选项闪烁）。
const customSpanMs = computed(() => {
  if (filters.range !== 'custom') return 0
  if (!filters.startAt || !filters.endAt) return 0
  const s = Date.parse(filters.startAt)
  const e = Date.parse(filters.endAt)
  if (Number.isNaN(s) || Number.isNaN(e) || e <= s) return 0
  return e - s
})

// 10m 桶仅在窗口跨度 ≤ 7 天时提供；预设 1m 与自定义超 7 天均禁用。
const granularityOptions = computed<{ value: OverviewGranularity; label: string }[]>(() => {
  const opts: { value: OverviewGranularity; label: string }[] = [{ value: 'auto', label: '自动' }]
  if (filters.range === 'custom') {
    const span = customSpanMs.value
    if (span === 0 || span <= 7 * 24 * 60 * 60 * 1000) opts.push({ value: '10m', label: '10m' })
  } else if (filters.range !== '1m') {
    opts.push({ value: '10m', label: '10m' })
  }
  opts.push(
    { value: '1h', label: '1h' },
    { value: '6h', label: '6h' },
    { value: '12h', label: '12h' },
    { value: '24h', label: '24h' },
  )
  return opts
})

// 当前 10m 不再可用时回落到自动（覆盖切 1m 预设与自定义超跨度）。
watch(
  () => granularityOptions.value,
  (opts) => {
    if (granularity.value === '10m' && !opts.some((o) => o.value === '10m')) {
      granularity.value = 'auto'
    }
  },
)

const distributionDimensionOptions: { value: AdminOverviewDimension; label: string }[] = [
  { value: 'user', label: '用户' },
  { value: 'provider', label: '渠道' },
  { value: 'model', label: '请求模型' },
  { value: 'upstreamModel', label: '上游模型' },
]
const seriesDimensionOptions: { value: AdminOverviewSeriesDimension; label: string }[] = [
  { value: 'none', label: '全部' },
  { value: 'user', label: '用户' },
  { value: 'provider', label: '渠道' },
  { value: 'model', label: '请求模型' },
  { value: 'upstreamModel', label: '上游模型' },
]

const overviewFilters = computed<AdminOverviewFilters>(() => {
  const out: {
    range: OverviewRange
    userId?: number
    model?: string
    upstreamModel?: string
    providerId?: number
    startAt?: string
    endAt?: string
  } = {
    range: filters.range,
  }
  if (filters.range === 'custom') {
    if (filters.startAt) out.startAt = filters.startAt
    if (filters.endAt) out.endAt = filters.endAt
  }
  if (filters.userId) out.userId = filters.userId
  if (filters.model) out.model = filters.model
  if (filters.upstreamModel) out.upstreamModel = filters.upstreamModel
  if (filters.providerId) out.providerId = filters.providerId
  return out
})

const usersQuery = useQuery({ queryKey: queryKeys.users.all, queryFn: listUsers })
const providersQuery = useQuery({
  queryKey: queryKeys.labels.providers,
  queryFn: listProviderLabels,
})
const modelsQuery = useQuery({ queryKey: queryKeys.labels.models, queryFn: listModelLabels })
const upstreamModelsQuery = useQuery({
  queryKey: queryKeys.labels.upstreamModels,
  queryFn: listUpstreamModelLabels,
})

const users = computed(() => usersQuery.data.value ?? [])
const providers = computed(() => providersQuery.data.value ?? [])
const models = computed(() => modelsQuery.data.value ?? [])

const userLabelById = computed(() => {
  const m = new Map<number, string>()
  for (const u of users.value) m.set(u.id, u.displayName)
  return m
})
const providerLabelById = computed(() => {
  const m = new Map<number, string>()
  for (const p of providers.value) m.set(p.id, p.name)
  return m
})

const modelOptions = computed(() => {
  const set = new Set<string>()
  for (const m of models.value) if (m.name) set.add(m.name)
  return Array.from(set).sort()
})

const upstreamModelOptions = computed(() => [...(upstreamModelsQuery.data.value ?? [])].sort())

const summaryQuery = useQuery({
  queryKey: computed(() => queryKeys.adminOverview.summary(overviewFilters.value)),
  queryFn: () => getAdminOverviewSummary(overviewFilters.value),
  staleTime: OPERATIONAL_STALE_TIME,
})

const distributionQuery = useQuery({
  queryKey: computed(() =>
    queryKeys.adminOverview.distribution(overviewFilters.value, distributionDimension.value),
  ),
  queryFn: () => getAdminOverviewDistribution(overviewFilters.value, distributionDimension.value),
  staleTime: OPERATIONAL_STALE_TIME,
})

const seriesQuery = useQuery({
  queryKey: computed(() =>
    queryKeys.adminOverview.series(overviewFilters.value, seriesDimension.value, granularity.value),
  ),
  queryFn: () =>
    getAdminOverviewSeries(overviewFilters.value, seriesDimension.value, granularity.value),
  staleTime: OPERATIONAL_STALE_TIME,
})

const speedSeriesQuery = useQuery({
  queryKey: computed(() =>
    queryKeys.adminOverview.speed(overviewFilters.value, speedDimension.value, granularity.value),
  ),
  queryFn: () =>
    getAdminOverviewSeries(overviewFilters.value, speedDimension.value, granularity.value),
  staleTime: OPERATIONAL_STALE_TIME,
})

const speedBoxplotQuery = useQuery({
  queryKey: computed(() =>
    queryKeys.adminOverview.speedBoxplot(overviewFilters.value, speedDimension.value),
  ),
  queryFn: () => getAdminOverviewSpeedBoxplot(overviewFilters.value, speedDimension.value),
  staleTime: OPERATIONAL_STALE_TIME,
})

const cacheHitRateSeriesQuery = useQuery({
  queryKey: computed(() =>
    queryKeys.adminOverview.cacheHitRate(
      overviewFilters.value,
      cacheHitRateDimension.value,
      granularity.value,
    ),
  ),
  queryFn: () =>
    getAdminOverviewSeries(overviewFilters.value, cacheHitRateDimension.value, granularity.value),
  staleTime: OPERATIONAL_STALE_TIME,
})

const overviewRefreshing = computed(
  () =>
    summaryQuery.isFetching.value ||
    distributionQuery.isFetching.value ||
    seriesQuery.isFetching.value ||
    speedSeriesQuery.isFetching.value ||
    speedBoxplotQuery.isFetching.value ||
    cacheHitRateSeriesQuery.isFetching.value,
)

function refreshOverview() {
  void Promise.all([
    summaryQuery.refetch(),
    distributionQuery.refetch(),
    seriesQuery.refetch(),
    speedSeriesQuery.refetch(),
    speedBoxplotQuery.refetch(),
    cacheHitRateSeriesQuery.refetch(),
  ])
}

function dimensionLabel(
  dim: AdminOverviewDimension | AdminOverviewSeriesDimension,
  key: string,
): string {
  if (dim === 'user') {
    if (key === '' || key === '0') return '未关联'
    const id = Number(key)
    return Number.isFinite(id) ? (userLabelById.value.get(id) ?? `#${key}`) : key
  }
  if (key === '') return '全部'
  if (dim === 'provider') {
    const id = Number(key)
    return Number.isFinite(id) ? (providerLabelById.value.get(id) ?? `#${key}`) : key
  }
  return key
}

const TOP_N = 8

const distributionRows = computed(() => distributionQuery.data.value?.rows ?? [])

interface DonutItem {
  key: string
  label: string
  value: number
}

function buildItemsAndTopN(items: DonutItem[]): DonutItem[] {
  const sorted = items.filter((d) => d.value > 0).sort((a, b) => b.value - a.value)
  if (sorted.length <= TOP_N) return sorted
  const top = sorted.slice(0, TOP_N)
  const restValue = sorted.slice(TOP_N).reduce((acc, d) => acc + d.value, 0)
  if (restValue > 0) top.push({ key: '__other__', label: '其他', value: restValue })
  return top
}

const tokenDonutData = computed<DonutItem[]>(() =>
  buildItemsAndTopN(
    distributionRows.value.map((r) => ({
      key: r.key,
      label: dimensionLabel(distributionDimension.value, r.key),
      value: r.totalTokens ?? 0,
    })),
  ),
)

function buildCostDonutDataForCurrency(currency: string): DonutItem[] {
  return buildItemsAndTopN(
    distributionRows.value.map((r) => {
      const cost = (r.costs ?? []).find((c) => c.currency === currency)
      return {
        key: r.key,
        label: dimensionLabel(distributionDimension.value, r.key),
        value: cost?.amount ?? 0,
      }
    }),
  )
}

const costDonutDataConverted = computed<DonutItem[]>(() => {
  const target = ccy.targetCurrency.value
  if (!target) return []
  return buildItemsAndTopN(
    distributionRows.value.map((r) => {
      const sum = (r.costs ?? []).reduce(
        (acc, c) => acc + ccy.convert(c.amount, c.currency).amount,
        0,
      )
      return {
        key: r.key,
        label: dimensionLabel(distributionDimension.value, r.key),
        value: sum,
      }
    }),
  )
})

const distributionCurrenciesPresent = computed(() => {
  const set = new Set<string>()
  for (const row of distributionRows.value) {
    for (const c of row.costs ?? []) if (c.currency) set.add(c.currency)
  }
  return Array.from(set).sort()
})

const seriesData = computed(() => seriesQuery.data.value)

const seriesCurrenciesPresent = computed(() => {
  const set = new Set<string>()
  for (const p of seriesData.value?.points ?? []) {
    if (p.metric === 'cost' && p.currency) set.add(p.currency)
  }
  return Array.from(set).sort()
})

const seriesGroups = computed(() => {
  const groups = seriesData.value?.groups ?? []
  return groups.map((g) => ({ key: g.key, label: dimensionLabel(seriesDimension.value, g.key) }))
})
const seriesBuckets = computed(() => seriesData.value?.buckets ?? [])

interface SeriesPointVM {
  groupKey: string
  bucketAt: string
  value: number
}

function nonCostPoints(metric: 'tokens' | 'requests' | 'traces'): SeriesPointVM[] {
  const points: OverviewSeriesPointView[] = seriesData.value?.points ?? []
  return points
    .filter((p) => p.metric === metric)
    .map((p) => ({ groupKey: p.groupKey, bucketAt: p.bucketAt, value: p.value }))
}

function costPointsForCurrency(currency: string): SeriesPointVM[] {
  return (seriesData.value?.points ?? [])
    .filter((p) => p.metric === 'cost' && p.currency === currency)
    .map((p) => ({ groupKey: p.groupKey, bucketAt: p.bucketAt, value: p.value }))
}

const costPointsConverted = computed<SeriesPointVM[]>(() => {
  const target = ccy.targetCurrency.value
  if (!target) return []
  const byGroup = new Map<string, Map<string, number>>()
  for (const p of seriesData.value?.points ?? []) {
    if (p.metric !== 'cost' || !p.currency) continue
    let m = byGroup.get(p.groupKey)
    if (!m) {
      m = new Map()
      byGroup.set(p.groupKey, m)
    }
    const v = ccy.convert(p.value, p.currency).amount
    m.set(p.bucketAt, (m.get(p.bucketAt) ?? 0) + v)
  }
  const out: SeriesPointVM[] = []
  for (const [groupKey, m] of byGroup) {
    for (const [bucketAt, value] of m) out.push({ groupKey, bucketAt, value })
  }
  return out
})

const seriesTokens = computed(() => nonCostPoints('tokens'))
const seriesRequests = computed(() => nonCostPoints('requests'))
const seriesTraces = computed(() => nonCostPoints('traces'))

const speedSeriesData = computed(() => speedSeriesQuery.data.value)
const speedGroups = computed(() => {
  const groups = speedSeriesData.value?.groups ?? []
  return groups.map((g) => ({ key: g.key, label: dimensionLabel(speedDimension.value, g.key) }))
})
const speedBuckets = computed(() => speedSeriesData.value?.buckets ?? [])
const seriesPrefillSpeed = computed(() => {
  const points: OverviewSeriesPointView[] = speedSeriesData.value?.points ?? []
  return points
    .filter((p) => p.metric === 'prefillSpeed')
    .map((p) => ({ groupKey: p.groupKey, bucketAt: p.bucketAt, value: p.value }))
})
const seriesDecodeSpeed = computed(() => {
  const points: OverviewSeriesPointView[] = speedSeriesData.value?.points ?? []
  return points
    .filter((p) => p.metric === 'decodeSpeed')
    .map((p) => ({ groupKey: p.groupKey, bucketAt: p.bucketAt, value: p.value }))
})
const seriesAvgTtft = computed(() => {
  const points: OverviewSeriesPointView[] = speedSeriesData.value?.points ?? []
  return points
    .filter((p) => p.metric === 'avgTtft')
    .map((p) => ({ groupKey: p.groupKey, bucketAt: p.bucketAt, value: p.value }))
})
const speedBoxplotItems = computed(() => {
  const items: OverviewSpeedBoxplotItemView[] = speedBoxplotQuery.data.value?.items ?? []
  return items.map((item) => ({
    ...item,
    label: dimensionLabel(speedDimension.value, item.key),
  }))
})

const cacheHitRateSeriesData = computed(() => cacheHitRateSeriesQuery.data.value)
const cacheHitRateGroups = computed(() => {
  const groups = cacheHitRateSeriesData.value?.groups ?? []
  return groups.map((g) => ({
    key: g.key,
    label: dimensionLabel(cacheHitRateDimension.value, g.key),
  }))
})
const cacheHitRateBuckets = computed(() => cacheHitRateSeriesData.value?.buckets ?? [])
const seriesCacheHitRate = computed(() => {
  const points: OverviewSeriesPointView[] = cacheHitRateSeriesData.value?.points ?? []
  return points
    .filter((p) => p.metric === 'cacheHitRate')
    .map((p) => ({ groupKey: p.groupKey, bucketAt: p.bucketAt, value: p.value }))
})

function formatSpeed(v: number, skipUnit = false) {
  const unit = skipUnit ? '' : ' tok/s'
  if (!Number.isFinite(v)) return ''
  if (Math.abs(v) >= 1e3) return `${(v / 1e3).toFixed(1)}k${unit}`
  if (Math.abs(v) >= 1) return `${v.toFixed(0)}${unit}`
  if (v === 0) return `0${unit}`
  return `${v.toFixed(1)}${unit}`
}

function formatTtft(v: number) {
  if (!Number.isFinite(v)) return ''
  if (Math.abs(v) >= 1000) return `${(v / 1000).toFixed(1)} s`
  return `${v.toFixed(0)} ms`
}

function formatPercent(v: number) {
  if (!Number.isFinite(v)) return ''
  return `${(v * 100).toFixed(1)}%`
}

const summaryConvertedTotal = computed(() => {
  const target = ccy.targetCurrency.value
  if (!target) return 0
  return (summaryQuery.data.value?.costs ?? []).reduce(
    (acc, c) => acc + ccy.convert(c.amount, c.currency).amount,
    0,
  )
})

const tokenCompositionSankey = computed<{ nodes: SankeyNode[]; links: SankeyLink[] }>(() => {
  const tb = summaryQuery.data.value?.tokenBreakdown
  if (!tb) return { nodes: [], links: [] }
  const inputTotal = tb.input + tb.cacheRead + tb.cacheWrite + tb.cacheWrite1h
  const outputTotal = tb.output
  if (inputTotal === 0 && outputTotal === 0) return { nodes: [], links: [] }

  const hidden = new Set(sankeyHiddenLayers.value.tokenComposition)
  const showLayer1 = !hidden.has(1)
  const showLayer2 = !hidden.has(2)

  const allNodes: SankeyNode[] = [
    { id: 'root', label: '总 Token', layer: 0 },
    { id: 'output', label: '输出', layer: 1 },
    { id: 'input', label: '输入', layer: 1 },
    { id: 'in_uncached', label: '未缓存输入', layer: 2 },
    { id: 'in_cache_read', label: '缓存读取', layer: 2 },
    { id: 'in_cache_write', label: '缓存写入', layer: 2 },
    { id: 'in_cache_write_1h', label: '长期缓存写入', layer: 2 },
  ]
  const links: SankeyLink[] = []
  if (showLayer1) {
    if (outputTotal > 0) links.push({ source: 'root', target: 'output', value: outputTotal })
    if (inputTotal > 0) links.push({ source: 'root', target: 'input', value: inputTotal })
  }
  if (showLayer2) {
    const sourceForDetail = showLayer1 ? 'input' : 'root'
    if (tb.input > 0)
      links.push({ source: sourceForDetail, target: 'in_uncached', value: tb.input })
    if (tb.cacheRead > 0)
      links.push({ source: sourceForDetail, target: 'in_cache_read', value: tb.cacheRead })
    if (tb.cacheWrite > 0)
      links.push({ source: sourceForDetail, target: 'in_cache_write', value: tb.cacheWrite })
    if (tb.cacheWrite1h > 0)
      links.push({ source: sourceForDetail, target: 'in_cache_write_1h', value: tb.cacheWrite1h })
  }

  const used = new Set<string>(['root'])
  for (const l of links) {
    used.add(l.source)
    used.add(l.target)
  }
  return { nodes: allNodes.filter((n) => used.has(n.id)), links }
})

type DimKind = 'user' | 'model' | 'upstreamModel' | 'provider'

const TOP_PER_LAYER = 8

function rowDimKey(row: AdminOverviewBreakdownRowView, dim: DimKind): string {
  switch (dim) {
    case 'user':
      return `user:${row.userId || 0}`
    case 'model':
      return `model:${row.model || ''}`
    case 'upstreamModel':
      return `upstreamModel:${row.upstreamModel || ''}`
    case 'provider':
      return `provider:${row.providerId || 0}`
  }
}

function rawValueFromKey(key: string): { dim: DimKind; raw: string } | null {
  const idx = key.indexOf(':')
  if (idx < 0) return null
  const dim = key.slice(0, idx) as DimKind
  if (dim !== 'user' && dim !== 'model' && dim !== 'upstreamModel' && dim !== 'provider')
    return null
  return { dim, raw: key.slice(idx + 1) }
}

function dimNodeLabel(dim: DimKind, raw: string): string {
  if (raw === '' || raw === '0') return '未知'
  return dimensionLabel(dim, raw)
}

function buildDimensionSankey(
  rows: AdminOverviewBreakdownRowView[],
  layers: DimKind[],
  rootId: string,
  rootLabel: string,
  valueOf: (row: AdminOverviewBreakdownRowView) => number,
  skipLayer?: (row: AdminOverviewBreakdownRowView, layerIdx: number) => boolean,
  hiddenLayerIndices?: number[],
): { nodes: SankeyNode[]; links: SankeyLink[]; dataLayerIndices: Set<number> } {
  // Compute which template-layer indices (1-based) have any data, ignoring hidden state
  const dataLayerIndices = new Set<number>()
  for (const row of rows) {
    const v = valueOf(row)
    if (v <= 0) continue
    layers.forEach((dim, i) => {
      if (skipLayer?.(row, i)) return
      dataLayerIndices.add(i + 1) // template index = layers index + 1
    })
  }
  const hiddenSet = new Set(hiddenLayerIndices ?? [])
  // Per-layer aggregate value per raw key, used to pick top N per layer.
  const totalsByLayer: Map<string, number>[] = layers.map(() => new Map())
  for (const row of rows) {
    const v = valueOf(row)
    if (v <= 0) continue
    layers.forEach((dim, i) => {
      if (hiddenSet.has(i)) return
      if (skipLayer?.(row, i)) return
      const k = rowDimKey(row, dim)
      totalsByLayer[i]!.set(k, (totalsByLayer[i]!.get(k) ?? 0) + v)
    })
  }
  const keepPerLayer: Set<string>[] = totalsByLayer.map(
    (m) =>
      new Set(
        [...m.entries()]
          .sort((a, b) => b[1] - a[1])
          .slice(0, TOP_PER_LAYER)
          .map(([k]) => k),
      ),
  )
  const folded = (layerIdx: number, rawKey: string) =>
    keepPerLayer[layerIdx]?.has(rawKey) ? rawKey : `__other__@${layerIdx}`

  const linkSums = new Map<string, number>()
  const addLink = (source: string, target: string, v: number) => {
    const k = `${source}|${target}`
    linkSums.set(k, (linkSums.get(k) ?? 0) + v)
  }
  for (const row of rows) {
    const v = valueOf(row)
    if (v <= 0) continue
    const rowVisible: number[] = []
    for (let i = 0; i < layers.length; i++) {
      if (hiddenSet.has(i)) continue
      if (skipLayer?.(row, i)) continue
      rowVisible.push(i)
    }
    if (rowVisible.length === 0) continue
    const firstIdx = rowVisible[0]!
    const firstFold = folded(firstIdx, rowDimKey(row, layers[firstIdx]!))
    addLink(rootId, firstFold, v)
    for (let k = 1; k < rowVisible.length; k++) {
      const prev = rowVisible[k - 1]!
      const curr = rowVisible[k]!
      const a = folded(prev, rowDimKey(row, layers[prev]!))
      const b = folded(curr, rowDimKey(row, layers[curr]!))
      addLink(a, b, v)
    }
  }

  const usedIds = new Set<string>([rootId])
  for (const k of linkSums.keys()) {
    const [s, t] = k.split('|')
    if (s) usedIds.add(s)
    if (t) usedIds.add(t)
  }

  const nodeFor = (id: string): SankeyNode => {
    if (id === rootId) return { id, label: rootLabel, layer: 0 }
    if (id.startsWith('__other__@')) {
      const layerIdx = Number(id.slice('__other__@'.length))
      return { id, label: '其他', layer: layerIdx + 1 }
    }
    const parsed = rawValueFromKey(id)
    if (!parsed) return { id, label: id, layer: 0 }
    const layerIdx = layers.indexOf(parsed.dim)
    return { id, label: dimNodeLabel(parsed.dim, parsed.raw), layer: layerIdx + 1 }
  }

  const nodes: SankeyNode[] = [...usedIds].map(nodeFor)
  const links: SankeyLink[] = [...linkSums.entries()].map(([k, v]) => {
    const [source = '', target = ''] = k.split('|')
    return { source, target, value: v }
  })
  return { nodes, links, dataLayerIndices }
}

const breakdownRows = computed<AdminOverviewBreakdownRowView[]>(
  () => summaryQuery.data.value?.breakdown ?? [],
)

const tokensInLayers: DimKind[] = ['provider', 'upstreamModel', 'model', 'user']
const tokensOutLayers: DimKind[] = ['user', 'model', 'upstreamModel', 'provider']

function collapseUpstreamModel(layers: DimKind[]) {
  return (row: AdminOverviewBreakdownRowView, layerIdx: number): boolean =>
    layers[layerIdx] === 'upstreamModel' && row.model !== '' && row.model === row.upstreamModel
}

const tokensInSankey = computed(() =>
  buildDimensionSankey(
    breakdownRows.value,
    tokensInLayers,
    'root',
    '总 Token',
    (row) => row.totalTokens,
    collapseUpstreamModel(tokensInLayers),
    getSankeyHiddenForBuild('tokensIn'),
  ),
)

const tokensOutSankey = computed(() =>
  buildDimensionSankey(
    breakdownRows.value,
    tokensOutLayers,
    'root',
    '总 Token',
    (row) => row.totalTokens,
    collapseUpstreamModel(tokensOutLayers),
    getSankeyHiddenForBuild('tokensOut'),
  ),
)

const breakdownCurrenciesPresent = computed(() => {
  const set = new Set<string>()
  for (const row of breakdownRows.value) {
    for (const c of row.costs ?? []) {
      if (c.currency) set.add(c.currency)
    }
  }
  return [...set].sort()
})

function rowCostInCurrency(row: AdminOverviewBreakdownRowView, currency: string): number {
  const c = (row.costs ?? []).find((x) => x.currency === currency)
  return c?.amount ?? 0
}

function rowCostConverted(row: AdminOverviewBreakdownRowView): number {
  return (row.costs ?? []).reduce((acc, c) => acc + ccy.convert(c.amount, c.currency).amount, 0)
}

const costInLayers: DimKind[] = ['provider', 'upstreamModel', 'model', 'user']
const costOutLayers: DimKind[] = ['user', 'model', 'upstreamModel', 'provider']

const costInSankeyConverted = computed(() => {
  const target = ccy.targetCurrency.value
  if (!target) return { nodes: [], links: [], dataLayerIndices: new Set<number>() }
  return buildDimensionSankey(
    breakdownRows.value,
    costInLayers,
    'root',
    '总费用',
    rowCostConverted,
    collapseUpstreamModel(costInLayers),
    getSankeyHiddenForBuild('costIn'),
  )
})

const costOutSankeyConverted = computed(() => {
  const target = ccy.targetCurrency.value
  if (!target) return { nodes: [], links: [], dataLayerIndices: new Set<number>() }
  return buildDimensionSankey(
    breakdownRows.value,
    costOutLayers,
    'root',
    '总费用',
    rowCostConverted,
    collapseUpstreamModel(costOutLayers),
    getSankeyHiddenForBuild('costOut'),
  )
})

function buildCostInSankeyForCurrency(currency: string) {
  return buildDimensionSankey(
    breakdownRows.value,
    costInLayers,
    'root',
    `总费用 · ${currency}`,
    (row) => rowCostInCurrency(row, currency),
    collapseUpstreamModel(costInLayers),
    getSankeyHiddenForBuild('costIn'),
  )
}
function buildCostOutSankeyForCurrency(currency: string) {
  return buildDimensionSankey(
    breakdownRows.value,
    costOutLayers,
    'root',
    `总费用 · ${currency}`,
    (row) => rowCostInCurrency(row, currency),
    collapseUpstreamModel(costOutLayers),
    getSankeyHiddenForBuild('costOut'),
  )
}

const tokenCompositionDataLayers = computed(() => {
  const tb = summaryQuery.data.value?.tokenBreakdown
  if (!tb) return new Set<number>([0])
  const present = new Set<number>([0])
  const inputDetail = tb.input + tb.cacheRead + tb.cacheWrite + tb.cacheWrite1h
  if (tb.output > 0 || inputDetail > 0) present.add(1)
  if (inputDetail > 0) present.add(2)
  return present
})

const sankeyDataLayerIndices = computed<Record<SankeyVariant, Set<number>>>(() => ({
  tokenComposition: tokenCompositionDataLayers.value,
  tokensIn: tokensInSankey.value.dataLayerIndices,
  tokensOut: tokensOutSankey.value.dataLayerIndices,
  costIn: costInSankeyConverted.value.dataLayerIndices,
  costOut: costOutSankeyConverted.value.dataLayerIndices,
}))

function sankeyLayersForVariant(variant: SankeyVariant): { label: string; index: number }[] {
  const dataLayers = sankeyDataLayerIndices.value[variant]
  return sankeyLayerLabels[variant]
    .map((l, i) => ({ label: l, index: i }))
    .filter((x) => dataLayers.has(x.index))
}

function compactNumber(v: number) {
  if (!Number.isFinite(v)) return ''
  if (Math.abs(v) >= 1e9) return `${(v / 1e9).toFixed(1)}B`
  if (Math.abs(v) >= 1e6) return `${(v / 1e6).toFixed(1)}M`
  if (Math.abs(v) >= 1e3) return `${(v / 1e3).toFixed(1)}k`
  return v.toFixed(0)
}

function formatBucket(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const hh = d.getHours().toString().padStart(2, '0')
  if (granularity.value === '10m') {
    const mm = d.getMinutes().toString().padStart(2, '0')
    if (filters.range === '1d') return `${hh}:${mm}`
    return `${d.getMonth() + 1}/${d.getDate()} ${hh}:${mm}`
  }
  const buckets = seriesBuckets.value
  if (buckets.length <= 24) {
    return `${hh}:00`
  }
  if (buckets.length <= 24 * 7) {
    return `${d.getMonth() + 1}/${d.getDate()} ${hh}:00`
  }
  return `${d.getMonth() + 1}/${d.getDate()}`
}

function formatCurrencyCompact(v: number, code: string) {
  if (!Number.isFinite(v)) return ''
  const abs = Math.abs(v)
  let scaled = v
  let suffix = ''
  if (abs >= 1e9) {
    scaled = v / 1e9
    suffix = 'B'
  } else if (abs >= 1e6) {
    scaled = v / 1e6
    suffix = 'M'
  } else if (abs >= 1e3) {
    scaled = v / 1e3
    suffix = 'k'
  }
  const digits = suffix ? 1 : 2
  return ccy.format(scaled, code, { minDigits: digits, maxDigits: digits }) + suffix
}
</script>

<template>
  <div class="flex flex-col gap-3">
    <!-- Controls bar -->
    <div class="flex flex-wrap items-end gap-3">
      <div class="flex flex-col gap-1">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">时间范围</span>
        <SegmentedControl v-model="filters.range" :options="rangeOptions" />
      </div>
      <div v-if="filters.range === 'custom'" class="flex flex-col gap-1">
        <TimeRangeFilter
          :model-value="{ startAt: filters.startAt, endAt: filters.endAt }"
          @update:model-value="
            (v) => {
              filters.startAt = v.startAt
              filters.endAt = v.endAt
            }
          "
        />
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">统计粒度</span>
        <SegmentedControl v-model="granularity" :options="granularityOptions" />
      </div>
      <div class="flex flex-col gap-1">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">货币</span>
        <Select v-model="overviewCurrencyValue" size="sm" :options="overviewCurrencyOptions" />
      </div>
      <DynamicFilterBar
        v-model="visibleFilters"
        :available="availableFilters"
        @remove="onRemoveFilter"
      >
        <template #user>
          <Select v-model="filters.userId" size="sm" :options="userOptions" />
        </template>
        <template #model>
          <Select v-model="filters.model" size="sm" :options="modelSelectOptions" />
        </template>
        <template #upstreamModel>
          <Select
            v-model="filters.upstreamModel"
            size="sm"
            :options="upstreamModelSelectOptions"
          />
        </template>
        <template #provider>
          <Select v-model="filters.providerId" size="sm" :options="providerOptions" />
        </template>
      </DynamicFilterBar>
      <Button
        variant="ghost"
        size="sm"
        class="mb-px"
        :disabled="overviewRefreshing"
        @click="refreshOverview"
      >
        <Icon name="refresh" :size="13" />
        {{ overviewRefreshing ? '刷新中' : '刷新' }}
      </Button>
    </div>

    <!-- Bento totals -->
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
      <DataCard class="min-h-20">
        <div class="p-4 min-h-20 flex flex-col gap-1.5">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >总 Token</span
          >
          <StateText v-if="summaryQuery.isLoading.value" compact :dashed="false">加载中…</StateText>
          <StateText v-else-if="summaryQuery.isError.value" compact :dashed="false">{{
            (summaryQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <span v-else class="text-xl font-semibold mono tabular text-ink">{{
            (summaryQuery.data.value?.totalTokens ?? 0).toLocaleString()
          }}</span>
        </div>
      </DataCard>
      <DataCard class="min-h-20">
        <div class="p-4 min-h-20 flex flex-col gap-1.5">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >总请求</span
          >
          <StateText v-if="summaryQuery.isLoading.value" compact :dashed="false">加载中…</StateText>
          <StateText v-else-if="summaryQuery.isError.value" compact :dashed="false">{{
            (summaryQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <span v-else class="text-xl font-semibold mono tabular text-ink">{{
            (summaryQuery.data.value?.totalRequests ?? 0).toLocaleString()
          }}</span>
        </div>
      </DataCard>
      <DataCard class="min-h-20">
        <div class="p-4 min-h-20 flex flex-col gap-1.5">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >总费用</span
          >
          <StateText v-if="summaryQuery.isLoading.value" compact :dashed="false">加载中…</StateText>
          <StateText v-else-if="summaryQuery.isError.value" compact :dashed="false">{{
            (summaryQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <div
            v-else-if="(summaryQuery.data.value?.costs ?? []).length === 0"
            class="text-xl text-ink-faint"
          >
            —
          </div>
          <MoneyDisplay
            v-else-if="!isOriginalMode"
            class="text-xl font-semibold mono tabular text-ink"
            :amount="summaryConvertedTotal"
            :currency="ccy.targetCurrency.value"
          />
          <div v-else class="flex flex-row flex-wrap items-baseline gap-x-1 gap-y-0.5">
            <template v-for="(c, i) in summaryQuery.data.value?.costs ?? []" :key="c.currency">
              <span v-if="i > 0" class="text-ink-faint text-base">+</span>
              <MoneyDisplay
                class="text-xl font-semibold mono tabular text-ink"
                :amount="c.amount"
                :currency="c.currency"
              />
            </template>
          </div>
        </div>
      </DataCard>
      <DataCard class="min-h-20">
        <div class="p-4 min-h-20 flex flex-col gap-1.5">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >总追踪</span
          >
          <StateText v-if="summaryQuery.isLoading.value" compact :dashed="false">加载中…</StateText>
          <StateText v-else-if="summaryQuery.isError.value" compact :dashed="false">{{
            (summaryQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <span v-else class="text-xl font-semibold mono tabular text-ink">{{
            (summaryQuery.data.value?.totalTraceCount ?? 0).toLocaleString()
          }}</span>
        </div>
      </DataCard>
    </div>

    <!-- Sankey -->
    <div class="flex flex-wrap items-end gap-3">
      <div class="flex flex-col gap-1">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">流向</span>
        <SegmentedControl v-model="sankeyVariant" :options="sankeyVariantOptions" />
      </div>
    </div>
    <div class="grid grid-cols-1 gap-3">
      <DataCard
        class="min-h-[22.5rem]"
        v-if="
          !isOriginalMode ||
          (sankeyVariant !== 'costIn' && sankeyVariant !== 'costOut') ||
          breakdownCurrenciesPresent.length <= 0
        "
      >
        <div class="p-4 min-h-[22.5rem] flex flex-col gap-3">
          <StateText v-if="summaryQuery.isLoading.value" compact :dashed="false">加载中…</StateText>
          <StateText v-else-if="summaryQuery.isError.value" compact :dashed="false">{{
            (summaryQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>

          <template v-else-if="sankeyVariant === 'tokenComposition'">
            <StateText v-if="!tokenCompositionSankey.links.length" compact>暂无数据</StateText>
            <OverviewSankey
              v-else
              :nodes="tokenCompositionSankey.nodes"
              :links="tokenCompositionSankey.links"
              :value-format="(v) => compactNumber(v)"
              :layers="sankeyLayersForVariant('tokenComposition')"
              :hidden-layer-indices="sankeyHiddenLayers.tokenComposition"
              @toggle-layer="toggleSankeyLayer"
            />
          </template>

          <template v-else-if="sankeyVariant === 'tokensIn'">
            <StateText v-if="!tokensInSankey.links.length" compact>暂无数据</StateText>
            <OverviewSankey
              v-else
              :nodes="tokensInSankey.nodes"
              :links="tokensInSankey.links"
              :value-format="(v) => compactNumber(v)"
              :layers="sankeyLayersForVariant('tokensIn')"
              :hidden-layer-indices="sankeyHiddenLayers.tokensIn"
              @toggle-layer="toggleSankeyLayer"
            />
          </template>
          <template v-else-if="sankeyVariant === 'tokensOut'">
            <StateText v-if="!tokensOutSankey.links.length" compact>暂无数据</StateText>
            <OverviewSankey
              v-else
              :nodes="tokensOutSankey.nodes"
              :links="tokensOutSankey.links"
              :value-format="(v) => compactNumber(v)"
              :layers="sankeyLayersForVariant('tokensOut')"
              :hidden-layer-indices="sankeyHiddenLayers.tokensOut"
              @toggle-layer="toggleSankeyLayer"
            />
          </template>

          <template v-else-if="sankeyVariant === 'costIn' && !isOriginalMode">
            <StateText v-if="!costInSankeyConverted.links.length" compact>暂无数据</StateText>
            <OverviewSankey
              v-else
              :nodes="costInSankeyConverted.nodes"
              :links="costInSankeyConverted.links"
              :value-format="(v) => formatCurrencyCompact(v, ccy.targetCurrency.value ?? '')"
              :layers="sankeyLayersForVariant('costIn')"
              :hidden-layer-indices="sankeyHiddenLayers.costIn"
              @toggle-layer="toggleSankeyLayer"
            />
          </template>
          <template v-else-if="sankeyVariant === 'costOut' && !isOriginalMode">
            <StateText v-if="!costOutSankeyConverted.links.length" compact>暂无数据</StateText>
            <OverviewSankey
              v-else
              :nodes="costOutSankeyConverted.nodes"
              :links="costOutSankeyConverted.links"
              :value-format="(v) => formatCurrencyCompact(v, ccy.targetCurrency.value ?? '')"
              :layers="sankeyLayersForVariant('costOut')"
              :hidden-layer-indices="sankeyHiddenLayers.costOut"
              @toggle-layer="toggleSankeyLayer"
            />
          </template>

          <template
            v-else-if="
              isOriginalMode &&
              (sankeyVariant === 'costIn' || sankeyVariant === 'costOut') &&
              breakdownCurrenciesPresent.length === 0
            "
          >
            <StateText compact>暂无数据</StateText>
          </template>
        </div>
      </DataCard>
      <template
        v-if="
          isOriginalMode &&
          (sankeyVariant === 'costIn' || sankeyVariant === 'costOut') &&
          breakdownCurrenciesPresent.length > 0
        "
      >
        <DataCard
          v-for="currency in breakdownCurrenciesPresent"
          :key="`${sankeyVariant}-${currency}`"
          class="min-h-[22.5rem]"
        >
          <div class="p-4 min-h-[22.5rem] flex flex-col gap-3">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">
              费用 · {{ currency }}
            </span>
            <StateText
              v-if="
                (sankeyVariant === 'costIn'
                  ? buildCostInSankeyForCurrency(currency)
                  : buildCostOutSankeyForCurrency(currency)
                ).links.length === 0
              "
              compact
              >暂无数据</StateText
            >
            <OverviewSankey
              v-else
              :nodes="
                (sankeyVariant === 'costIn'
                  ? buildCostInSankeyForCurrency(currency)
                  : buildCostOutSankeyForCurrency(currency)
                ).nodes
              "
              :links="
                (sankeyVariant === 'costIn'
                  ? buildCostInSankeyForCurrency(currency)
                  : buildCostOutSankeyForCurrency(currency)
                ).links
              "
              :value-format="(v) => formatCurrencyCompact(v, currency)"
              :layers="sankeyLayersForVariant(sankeyVariant)"
              :hidden-layer-indices="sankeyHiddenLayers[sankeyVariant]"
              @toggle-layer="toggleSankeyLayer"
            />
          </div>
        </DataCard>
      </template>
    </div>

    <!-- Distribution -->
    <div class="flex flex-wrap items-end gap-3">
      <div class="flex flex-col gap-1">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
          >分布统计</span
        >
        <SegmentedControl v-model="distributionDimension" :options="distributionDimensionOptions" />
      </div>
    </div>
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
      <DataCard class="min-h-[17.5rem]">
        <div class="p-4 min-h-[17.5rem] flex flex-col gap-3">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >Token 分布</span
          >
          <StateText v-if="distributionQuery.isLoading.value" compact :dashed="false"
            >加载中…</StateText
          >
          <StateText v-else-if="distributionQuery.isError.value" compact :dashed="false">{{
            (distributionQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <StateText v-else-if="!tokenDonutData.length" compact>暂无数据</StateText>
          <OverviewDonut v-else :data="tokenDonutData" :value-format="(v) => compactNumber(v)" />
        </div>
      </DataCard>
      <template v-if="!isOriginalMode">
        <DataCard class="min-h-[17.5rem]">
          <div class="p-4 min-h-[17.5rem] flex flex-col gap-3">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
              >费用分布<span class="ml-1 text-ink-faint normal-case"
                >· {{ ccy.targetCurrency.value }}</span
              ></span
            >
            <StateText v-if="distributionQuery.isLoading.value" compact :dashed="false"
              >加载中…</StateText
            >
            <StateText v-else-if="distributionQuery.isError.value" compact :dashed="false">{{
              (distributionQuery.error.value as Error)?.message ?? '加载失败'
            }}</StateText>
            <StateText v-else-if="!costDonutDataConverted.length" compact>暂无数据</StateText>
            <OverviewDonut
              v-else
              :data="costDonutDataConverted"
              :value-format="(v) => formatCurrencyCompact(v, ccy.targetCurrency.value ?? '')"
            />
          </div>
        </DataCard>
      </template>
      <template v-else-if="distributionCurrenciesPresent.length === 0">
        <DataCard class="min-h-[17.5rem]">
          <div class="p-4 min-h-[17.5rem] flex flex-col gap-3">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
              >费用分布</span
            >
            <StateText v-if="distributionQuery.isLoading.value" compact :dashed="false"
              >加载中…</StateText
            >
            <StateText v-else-if="distributionQuery.isError.value" compact :dashed="false">{{
              (distributionQuery.error.value as Error)?.message ?? '加载失败'
            }}</StateText>
            <StateText v-else compact>暂无数据</StateText>
          </div>
        </DataCard>
      </template>
      <template v-else>
        <DataCard
          v-for="currency in distributionCurrenciesPresent"
          :key="currency"
          class="min-h-[17.5rem]"
        >
          <div class="p-4 min-h-[17.5rem] flex flex-col gap-3">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
              >费用分布<span class="ml-1 text-ink-faint normal-case">· {{ currency }}</span></span
            >
            <StateText v-if="distributionQuery.isLoading.value" compact :dashed="false"
              >加载中…</StateText
            >
            <StateText v-else-if="distributionQuery.isError.value" compact :dashed="false">{{
              (distributionQuery.error.value as Error)?.message ?? '加载失败'
            }}</StateText>
            <StateText v-else-if="!buildCostDonutDataForCurrency(currency).length" compact
              >暂无数据</StateText
            >
            <OverviewDonut
              v-else
              :data="buildCostDonutDataForCurrency(currency)"
              :value-format="(v) => formatCurrencyCompact(v, currency)"
            />
          </div>
        </DataCard>
      </template>
    </div>

    <!-- Series -->
    <div class="flex flex-wrap items-end gap-3">
      <div class="flex flex-col gap-1">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
          >用量统计</span
        >
        <SegmentedControl v-model="seriesDimension" :options="seriesDimensionOptions" />
      </div>
    </div>
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
      <DataCard class="min-h-[17rem]">
        <div class="p-4 min-h-[17rem] flex flex-col gap-3">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">Token</span>
          <StateText v-if="seriesQuery.isLoading.value" compact :dashed="false">加载中…</StateText>
          <StateText v-else-if="seriesQuery.isError.value" compact :dashed="false">{{
            (seriesQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <OverviewAreaStack
            v-else
            :groups="seriesGroups"
            :buckets="seriesBuckets"
            :points="seriesTokens"
            :value-format="(v) => compactNumber(v)"
            :bucket-format="formatBucket"
          />
        </div>
      </DataCard>
      <template v-if="!isOriginalMode">
        <DataCard class="min-h-[17rem]">
          <div class="p-4 min-h-[17rem] flex flex-col gap-3">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
              >费用<span class="ml-1 text-ink-faint normal-case"
                >· {{ ccy.targetCurrency.value }}</span
              ></span
            >
            <StateText v-if="seriesQuery.isLoading.value" compact :dashed="false"
              >加载中…</StateText
            >
            <StateText v-else-if="seriesQuery.isError.value" compact :dashed="false">{{
              (seriesQuery.error.value as Error)?.message ?? '加载失败'
            }}</StateText>
            <OverviewAreaStack
              v-else
              :groups="seriesGroups"
              :buckets="seriesBuckets"
              :points="costPointsConverted"
              :value-format="(v) => formatCurrencyCompact(v, ccy.targetCurrency.value ?? '')"
              :bucket-format="formatBucket"
            />
          </div>
        </DataCard>
      </template>
      <template v-else-if="seriesCurrenciesPresent.length === 0">
        <DataCard class="min-h-[17rem]">
          <div class="p-4 min-h-[17rem] flex flex-col gap-3">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
              >费用</span
            >
            <StateText v-if="seriesQuery.isLoading.value" compact :dashed="false"
              >加载中…</StateText
            >
            <StateText v-else-if="seriesQuery.isError.value" compact :dashed="false">{{
              (seriesQuery.error.value as Error)?.message ?? '加载失败'
            }}</StateText>
            <StateText v-else compact>暂无数据</StateText>
          </div>
        </DataCard>
      </template>
      <template v-else>
        <DataCard v-for="currency in seriesCurrenciesPresent" :key="currency" class="min-h-[17rem]">
          <div class="p-4 min-h-[17rem] flex flex-col gap-3">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
              >费用<span class="ml-1 text-ink-faint normal-case">· {{ currency }}</span></span
            >
            <StateText v-if="seriesQuery.isLoading.value" compact :dashed="false"
              >加载中…</StateText
            >
            <StateText v-else-if="seriesQuery.isError.value" compact :dashed="false">{{
              (seriesQuery.error.value as Error)?.message ?? '加载失败'
            }}</StateText>
            <OverviewAreaStack
              v-else
              :groups="seriesGroups"
              :buckets="seriesBuckets"
              :points="costPointsForCurrency(currency)"
              :value-format="(v) => formatCurrencyCompact(v, currency)"
              :bucket-format="formatBucket"
            />
          </div>
        </DataCard>
      </template>
      <DataCard class="min-h-[17rem]">
        <div class="p-4 min-h-[17rem] flex flex-col gap-3">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >请求数</span
          >
          <StateText v-if="seriesQuery.isLoading.value" compact :dashed="false">加载中…</StateText>
          <StateText v-else-if="seriesQuery.isError.value" compact :dashed="false">{{
            (seriesQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <OverviewAreaStack
            v-else
            :groups="seriesGroups"
            :buckets="seriesBuckets"
            :points="seriesRequests"
            :value-format="(v) => compactNumber(v)"
            :bucket-format="formatBucket"
          />
        </div>
      </DataCard>
      <DataCard class="min-h-[17rem]">
        <div class="p-4 min-h-[17rem] flex flex-col gap-3">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >追踪数</span
          >
          <StateText v-if="seriesQuery.isLoading.value" compact :dashed="false">加载中…</StateText>
          <StateText v-else-if="seriesQuery.isError.value" compact :dashed="false">{{
            (seriesQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <OverviewAreaStack
            v-else
            :groups="seriesGroups"
            :buckets="seriesBuckets"
            :points="seriesTraces"
            :value-format="(v) => compactNumber(v)"
            :bucket-format="formatBucket"
          />
        </div>
      </DataCard>
    </div>

    <!-- Speed -->
    <div class="flex flex-wrap items-end gap-3">
      <div class="flex flex-col gap-1">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
          >速度统计</span
        >
        <SegmentedControl v-model="speedDimension" :options="seriesDimensionOptions" />
      </div>
    </div>
    <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
      <DataCard class="min-h-[17rem]">
        <div class="p-4 min-h-[17rem] flex flex-col gap-3">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >输入速度</span
          >
          <StateText v-if="speedSeriesQuery.isLoading.value" compact :dashed="false"
            >加载中…</StateText
          >
          <StateText v-else-if="speedSeriesQuery.isError.value" compact :dashed="false">{{
            (speedSeriesQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <OverviewLineChart
            v-else
            :groups="speedGroups"
            :buckets="speedBuckets"
            :points="seriesPrefillSpeed"
            :value-format="(v, s = false) => formatSpeed(v, s)"
            :bucket-format="formatBucket"
          />
        </div>
      </DataCard>
      <DataCard class="min-h-[17rem]">
        <div class="p-4 min-h-[17rem] flex flex-col gap-3">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >输出速度</span
          >
          <StateText v-if="speedSeriesQuery.isLoading.value" compact :dashed="false"
            >加载中…</StateText
          >
          <StateText v-else-if="speedSeriesQuery.isError.value" compact :dashed="false">{{
            (speedSeriesQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <OverviewLineChart
            v-else
            :groups="speedGroups"
            :buckets="speedBuckets"
            :points="seriesDecodeSpeed"
            :value-format="(v) => formatSpeed(v)"
            :bucket-format="formatBucket"
          />
        </div>
      </DataCard>
      <DataCard class="min-h-[17rem]">
        <div class="p-4 min-h-[17rem] flex flex-col gap-3">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >TTFT 平均时间</span
          >
          <StateText v-if="speedSeriesQuery.isLoading.value" compact :dashed="false"
            >加载中…</StateText
          >
          <StateText v-else-if="speedSeriesQuery.isError.value" compact :dashed="false">{{
            (speedSeriesQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <OverviewLineChart
            v-else
            :groups="speedGroups"
            :buckets="speedBuckets"
            :points="seriesAvgTtft"
            :value-format="(v) => formatTtft(v)"
            :bucket-format="formatBucket"
          />
        </div>
      </DataCard>
      <DataCard class="min-h-[12rem]">
        <div class="p-4 min-h-[12rem] flex flex-col gap-3">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >输出速度</span
          >
          <StateText v-if="speedBoxplotQuery.isLoading.value" compact :dashed="false"
            >加载中…</StateText
          >
          <StateText v-else-if="speedBoxplotQuery.isError.value" compact :dashed="false">{{
            (speedBoxplotQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <OverviewSpeedTimeline
            v-else
            :items="speedBoxplotItems"
            :value-format="(v) => formatSpeed(v)"
          />
        </div>
      </DataCard>
    </div>

    <!-- Cache Hit Rate -->
    <div class="flex flex-wrap items-end gap-3">
      <div class="flex flex-col gap-1">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
          >缓存命中率</span
        >
        <SegmentedControl v-model="cacheHitRateDimension" :options="seriesDimensionOptions" />
      </div>
    </div>
    <div class="grid grid-cols-1 gap-3">
      <DataCard class="min-h-[17rem]">
        <div class="p-4 min-h-[17rem] flex flex-col gap-3">
          <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >缓存命中率</span
          >
          <StateText v-if="cacheHitRateSeriesQuery.isLoading.value" compact :dashed="false"
            >加载中…</StateText
          >
          <StateText v-else-if="cacheHitRateSeriesQuery.isError.value" compact :dashed="false">{{
            (cacheHitRateSeriesQuery.error.value as Error)?.message ?? '加载失败'
          }}</StateText>
          <OverviewLineChart
            v-else
            :groups="cacheHitRateGroups"
            :buckets="cacheHitRateBuckets"
            :points="seriesCacheHitRate"
            :value-format="(v) => formatPercent(v)"
            :bucket-format="formatBucket"
          />
        </div>
      </DataCard>
    </div>
  </div>
</template>

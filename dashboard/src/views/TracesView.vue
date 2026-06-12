<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useCurrencyContext } from '@/composables/useCurrencyContext'
import { useProjectsMap } from '@/composables/useProjectsMap'
import { listRequestTraces } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import type { RequestTraceView, TraceCostView } from '@/api'
import { AutoDataTable, Button, DataCard, Icon, IconButton, type AutoDataTableColumn } from '@/ui'
import AutoRefreshSelect from '@/components/AutoRefreshSelect.vue'
import { usePreferencesStore } from '@/stores/preferences'

const router = useRouter()
const route = useRoute()
const currency = useCurrencyContext()
const { projectLabel } = useProjectsMap()
const prefs = usePreferencesStore()
const pageSize = 30
const initialCursor = typeof route.query.cursor === 'string' ? route.query.cursor : ''
const cursorIndex = ref(initialCursor ? 1 : 0)
const pageCursors = ref<string[]>(initialCursor ? ['', initialCursor] : [''])
const hasPaginationHistory = ref(!initialCursor)

const currentCursor = computed(() =>
  typeof route.query.cursor === 'string' ? route.query.cursor : '',
)

const tracesQuery = useQuery({
  queryKey: computed(() =>
    queryKeys.requestTraces.list({ limit: pageSize, cursor: currentCursor.value }),
  ),
  queryFn: () => listRequestTraces({ limit: pageSize, cursor: currentCursor.value || undefined }),
  refetchInterval: computed(() => (prefs.tracesRefreshMs > 0 ? prefs.tracesRefreshMs : false)),
  // Immediate refresh on return to a hidden/minimized tab, only while auto-refresh is on.
  refetchOnWindowFocus: () => prefs.tracesRefreshMs > 0,
})
const traces = computed<RequestTraceView[]>(() => tracesQuery.data.value?.items ?? [])

const previousTraceIds = ref(new Set<string | number>())
const newRowKeys = ref(new Set<string | number>())
const lastContextKey = ref<string | null>(null)
const contextKey = computed(() => JSON.stringify({ cursor: currentCursor.value }))

watch(
  traces,
  (newTraces) => {
    const currentIds = new Set(newTraces.map((r) => rowKey(r)))
    if (contextKey.value !== lastContextKey.value) {
      lastContextKey.value = contextKey.value
      previousTraceIds.value = currentIds
      newRowKeys.value = new Set()
      return
    }
    const fresh = new Set<string | number>()
    for (const id of currentIds) {
      if (!previousTraceIds.value.has(id)) fresh.add(id)
    }
    previousTraceIds.value = currentIds
    newRowKeys.value = fresh
    if (fresh.size > 0) {
      setTimeout(() => {
        newRowKeys.value = new Set()
      }, 100)
    }
  },
  { flush: 'post' },
)
const loading = computed(() => tracesQuery.isLoading.value)
const hasMore = computed(() => tracesQuery.data.value?.pagination.hasMore ?? false)
const canGoHome = computed(() => !!currentCursor.value)
const canGoPrevious = computed(
  () =>
    hasPaginationHistory.value &&
    cursorIndex.value > 1 &&
    pageCursors.value[cursorIndex.value - 1] !== undefined,
)
const canGoNext = computed(() => hasPaginationHistory.value && hasMore.value)

watch(
  () => route.query.cursor,
  (value) => {
    const next = typeof value === 'string' ? value : ''
    const knownIndex = pageCursors.value.indexOf(next)
    cursorIndex.value = knownIndex >= 0 ? knownIndex : next ? 1 : 0
  },
)

const columns = computed<AutoDataTableColumn<RequestTraceView>[]>(() => [
  { key: 'lastRequestAt', header: '最近请求' },
  { key: 'firstRequestAt', header: '首次请求' },
  { key: 'userMessagePreview', header: '用户消息' },
  { key: 'projectId', header: '项目' },
  { key: 'id', header: 'Trace ID' },
  { key: 'metaRequestCount', header: '请求', align: 'right' },
  { key: 'totalTokens', header: 'Token', align: 'right' },
  { key: 'cacheHitRate', header: '缓存命中', align: 'right' },
  { key: 'modelCosts', header: '模型成本', align: 'right' },
])

function rowKey(row: RequestTraceView) {
  return row.id
}

function openTrace(row: RequestTraceView) {
  router.push({ name: 'requests', query: { traceId: row.id } })
}

function openProject(event: Event, projectId: number) {
  event.stopPropagation()
  router.push({ name: 'requests', query: { projectId: String(projectId) } })
}

function pushCursorQuery(nextCursor: string) {
  const query = { ...route.query }
  if (nextCursor) query.cursor = nextCursor
  else delete query.cursor
  router.push({ name: 'traces', query })
}

function resetPaginationMemory() {
  pageCursors.value = ['']
  cursorIndex.value = 0
  hasPaginationHistory.value = true
}

function goHome() {
  resetPaginationMemory()
  pushCursorQuery('')
}

function goPrevious() {
  if (!canGoPrevious.value) return
  const previousCursor = pageCursors.value[cursorIndex.value - 1]
  if (previousCursor === undefined) return
  pushCursorQuery(previousCursor)
}

function goNext() {
  if (!canGoNext.value) return
  const nextCursor = tracesQuery.data.value?.pagination.nextCursor
  if (!nextCursor) return
  pageCursors.value = pageCursors.value.slice(0, cursorIndex.value + 1)
  pageCursors.value[cursorIndex.value + 1] = nextCursor
  cursorIndex.value += 1
  hasPaginationHistory.value = true
  pushCursorQuery(nextCursor)
}

function formatTimeParts(iso: string | undefined): { time: string; date: string } {
  if (!iso) return { time: '—', date: '' }
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return { time: iso, date: '' }
  const pad = (n: number) => String(n).padStart(2, '0')
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  const date = `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  return { time, date }
}

function formatNumber(value: number): string {
  return value.toLocaleString()
}

function cacheHitRate(row: RequestTraceView): number | null {
  const denominator =
    row.inputTokens + row.cacheReadTokens + row.cacheWriteTokens + row.cacheWrite1hTokens
  if (denominator <= 0) return null
  return row.cacheReadTokens / denominator
}

function formatCacheHitRate(row: RequestTraceView): string {
  const rate = cacheHitRate(row)
  if (rate == null) return '—'
  return `${parseFloat((rate * 100).toFixed(2))}%`
}

function normalizeCosts(costs: TraceCostView[] | null): TraceCostView[] {
  return costs ?? []
}

function nativeCostText(costs: TraceCostView[] | null): string {
  const items = normalizeCosts(costs)
  if (items.length === 0) return '—'
  return items.map((c) => currency.format(c.amount, c.currency)).join(' + ')
}

function formatCosts(costs: TraceCostView[] | null): { text: string; title?: string } {
  const items = normalizeCosts(costs)
  if (items.length === 0) return { text: '—' }
  const target = currency.targetCurrency.value
  if (!target) return { text: nativeCostText(items) }

  let total = 0
  for (const cost of items) {
    const converted = currency.convert(cost.amount, cost.currency)
    if (converted.currency !== target) {
      return { text: nativeCostText(items) }
    }
    total += converted.amount
  }

  return {
    text: currency.format(total, target),
    title: nativeCostText(items),
  }
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-center justify-between gap-3 flex-wrap">
      <span class="text-xs text-ink-faint tabular-nums">
        {{ traces.length }} 条追踪<span v-if="hasMore">（还有更多）</span>
      </span>
      <div class="flex items-center gap-2">
        <IconButton
          title="刷新"
          aria-label="刷新"
          :disabled="loading"
          @click="tracesQuery.refetch()"
        >
          <Icon name="refresh" :size="13" />
        </IconButton>
        <AutoRefreshSelect v-model="prefs.tracesRefreshMs" />
      </div>
    </div>

    <DataCard>
      <AutoDataTable
        :columns="columns"
        :items="traces"
        :row-key="rowKey"
        :new-row-keys="newRowKeys"
        :on-row-click="openTrace"
      >
        <template #cell-lastRequestAt="{ row }">
          <div class="flex flex-col leading-tight">
            <span class="font-mono tabular-nums text-ink">{{
              formatTimeParts(row.lastRequestAt).time
            }}</span>
            <span class="font-mono text-2xs text-ink-faint">{{
              formatTimeParts(row.lastRequestAt).date
            }}</span>
          </div>
        </template>
        <template #cell-firstRequestAt="{ row }">
          <div class="flex flex-col leading-tight">
            <span class="font-mono tabular-nums text-ink">{{
              formatTimeParts(row.firstRequestAt).time
            }}</span>
            <span class="font-mono text-2xs text-ink-faint">{{
              formatTimeParts(row.firstRequestAt).date
            }}</span>
          </div>
        </template>
        <template #cell-userMessagePreview="{ row }">
          <span
            class="block max-w-[18rem] truncate"
            :class="row.userMessagePreview ? 'text-ink' : 'text-ink-faint'"
            :title="row.userMessagePreview"
          >
            {{ row.userMessagePreview || '—' }}
          </span>
        </template>
        <template #cell-projectId="{ row }">
          <button
            v-if="row.projectId"
            type="button"
            class="font-medium text-ink hover:text-accent transition-colors bg-transparent border-0 p-0 cursor-pointer"
            @click="(ev: Event) => openProject(ev, row.projectId!)"
          >
            {{ projectLabel(row.projectId) }}
          </button>
          <span v-else class="text-ink-faint">—</span>
        </template>
        <template #cell-id="{ row }">
          <div class="flex max-w-[13rem] flex-col leading-tight">
            <span class="truncate font-mono text-xs text-ink" :title="row.id">{{ row.id }}</span>
            <span class="truncate font-mono text-2xs text-ink-faint" :title="row.parentSpanId">
              {{ row.parentSpanId }}
            </span>
          </div>
        </template>
        <template #cell-metaRequestCount="{ row }">
          <div class="flex flex-col items-end leading-tight">
            <span class="font-mono tabular-nums text-ink">{{
              row.metaRequestCount.toLocaleString()
            }}</span>
            <span
              v-if="row.metaRequestCount !== row.upstreamRequestCount"
              class="font-mono text-2xs text-ink-faint tabular-nums"
              title="上游（实际）请求数"
            >
              {{
                row.upstreamRequestCount - row.metaRequestCount > 0
                  ? `+${row.upstreamRequestCount - row.metaRequestCount}`
                  : row.upstreamRequestCount - row.metaRequestCount
              }}
            </span>
          </div>
        </template>
        <template #cell-totalTokens="{ row }">
          <div class="flex flex-col items-end leading-tight">
            <span class="font-mono tabular-nums text-ink">{{ formatNumber(row.totalTokens) }}</span>
            <span class="font-mono text-2xs text-ink-faint tabular-nums">
              {{
                formatNumber(
                  row.inputTokens +
                    row.cacheReadTokens +
                    row.cacheWriteTokens +
                    row.cacheWrite1hTokens,
                )
              }}
              / {{ formatNumber(row.outputTokens) }}
            </span>
          </div>
        </template>
        <template #cell-cacheHitRate="{ row }">
          <span
            class="font-mono tabular-nums"
            :class="cacheHitRate(row) == null ? 'text-ink-faint' : 'text-ink'"
            :title="`cache read ${formatNumber(row.cacheReadTokens)} / input ${formatNumber(row.inputTokens + row.cacheReadTokens + row.cacheWriteTokens + row.cacheWrite1hTokens)}`"
          >
            {{ formatCacheHitRate(row) }}
          </span>
        </template>
        <template #cell-modelCosts="{ row }">
          <span
            class="font-mono tabular-nums"
            :class="normalizeCosts(row.modelCosts).length === 0 ? 'text-ink-faint' : 'text-ink'"
            :title="formatCosts(row.modelCosts).title"
          >
            {{ formatCosts(row.modelCosts).text }}
          </span>
        </template>
        <template #empty>
          <span v-if="loading">加载中…</span>
          <span v-else>暂无追踪</span>
        </template>
      </AutoDataTable>
    </DataCard>

    <div v-if="canGoHome || canGoPrevious || canGoNext" class="flex justify-center gap-2 py-1">
      <Button v-if="canGoHome" variant="ghost" :disabled="loading" @click="goHome">首页</Button>
      <Button v-if="canGoPrevious" variant="ghost" :disabled="loading" @click="goPrevious">
        上一页
      </Button>
      <Button v-if="canGoNext" variant="ghost" :disabled="loading" @click="goNext">
        {{ loading ? '加载中…' : '下一页' }}
      </Button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, watch, computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useProvidersMap } from '@/composables/useProvidersMap'
import { useProjectsMap } from '@/composables/useProjectsMap'
import type { RequestView, EndpointView, ModelView } from '@/api'
import { listEndpoints, listModels, listRequests } from '@/api/client'
import { queryKeys, type RequestsFilters } from '@/api/queryKeys'
import RequestDetailsPanel from '@/components/RequestDetailsPanel.vue'
import AutoRefreshSelect from '@/components/AutoRefreshSelect.vue'
import { useSidePanel } from '@/composables/useSidePanel'
import { usePreferencesStore } from '@/stores/preferences'
import {
  Button,
  IconButton,
  DataCard,
  AutoDataTable,
  Tag,
  Field,
  Icon,
  SegmentedControl,
  ColumnFilter,
  MoneyDisplay,
  type AutoDataTableColumn,
  type ColumnFilterOption,
} from '@/ui'
import { finishReasonLabel } from '@/utils/requestLabels'

const panel = useSidePanel()
const route = useRoute()
const router = useRouter()
const { providers, providerLabel } = useProvidersMap()
const { projects, projectLabel } = useProjectsMap()
const prefs = usePreferencesStore()

// Track new rows for animation on refresh
const previousRequestIds = ref(new Set<string | number>())
const newRowKeys = ref(new Set<string | number>())
const lastContextKey = ref<string | null>(null)

type RequestKind = 'meta' | 'upstream' | 'all'

const filters = reactive({
  type: 'meta' as RequestKind,
  providerId: 0,
  endpointPath: '',
  model: '',
  upstreamModel: '',
  traceId: typeof route.query.traceId === 'string' ? route.query.traceId : '',
  projectId: typeof route.query.projectId === 'string' ? Number(route.query.projectId) || 0 : 0,
})

const typeOptions: { value: RequestKind; label: string }[] = [
  { value: 'meta', label: '元请求' },
  { value: 'upstream', label: '上游请求' },
  { value: 'all', label: '全部' },
]
const appBase = import.meta.env.BASE_URL.replace(/\/$/, '')
const pageSize = 30
const initialCursor = typeof route.query.cursor === 'string' ? route.query.cursor : ''
const cursorIndex = ref(initialCursor ? 1 : 0)
const pageCursors = ref<string[]>(initialCursor ? ['', initialCursor] : [''])
const hasPaginationHistory = ref(!initialCursor)

const endpointsQuery = useQuery({
  queryKey: queryKeys.endpoints.all,
  queryFn: listEndpoints,
})
const modelsQuery = useQuery({
  queryKey: queryKeys.models.all,
  queryFn: listModels,
})
const endpoints = computed<EndpointView[]>(() => endpointsQuery.data.value ?? [])
const models = computed<ModelView[]>(() => modelsQuery.data.value ?? [])

const requestFilters = computed<RequestsFilters>(() => {
  const out: {
    type?: number
    providerId?: number
    endpointPath?: string
    model?: string
    upstreamModel?: string
    traceId?: string
    projectId?: number
  } = {}
  if (filters.type === 'meta') out.type = 0
  else if (filters.type === 'upstream') out.type = 1
  if (filters.providerId) out.providerId = filters.providerId
  if (filters.endpointPath) out.endpointPath = filters.endpointPath
  if (filters.model) out.model = filters.model
  if (filters.upstreamModel) out.upstreamModel = filters.upstreamModel
  if (filters.traceId) out.traceId = filters.traceId
  if (filters.projectId) out.projectId = filters.projectId
  return out
})

const currentCursor = computed(() =>
  typeof route.query.cursor === 'string' ? route.query.cursor : '',
)

const contextKey = computed(() =>
  JSON.stringify({ ...requestFilters.value, cursor: currentCursor.value }),
)

const requestsQuery = useQuery({
  queryKey: computed(() =>
    queryKeys.requests.list({
      ...requestFilters.value,
      limit: pageSize,
      cursor: currentCursor.value,
    }),
  ),
  queryFn: () =>
    listRequests({
      ...requestFilters.value,
      limit: pageSize,
      cursor: currentCursor.value || undefined,
    }),
  refetchInterval: computed(() => (prefs.requestsRefreshMs > 0 ? prefs.requestsRefreshMs : false)),
  // Immediate refresh on return to a hidden/minimized tab, only while auto-refresh is on.
  refetchOnWindowFocus: () => prefs.requestsRefreshMs > 0,
})
const requests = computed<RequestView[]>(() => requestsQuery.data.value?.items ?? [])

watch(
  requests,
  (newRequests) => {
    const currentIds = new Set(newRequests.map((r) => rowKey(r)))
    // Navigation / filter change → new context: reset baseline, do not flash.
    if (contextKey.value !== lastContextKey.value) {
      lastContextKey.value = contextKey.value
      previousRequestIds.value = currentIds
      newRowKeys.value = new Set()
      return
    }
    // Same context (manual or auto refresh) → flash genuinely new rows.
    const fresh = new Set<string | number>()
    for (const id of currentIds) {
      if (!previousRequestIds.value.has(id)) fresh.add(id)
    }
    previousRequestIds.value = currentIds
    newRowKeys.value = fresh
    if (fresh.size > 0) {
      setTimeout(() => {
        newRowKeys.value = new Set()
      }, 100)
    }
  },
  { flush: 'post' },
)
const loading = computed(() => requestsQuery.isLoading.value)
const hasMore = computed(() => requestsQuery.data.value?.pagination.hasMore ?? false)
const canGoHome = computed(() => !!currentCursor.value)
const canGoPrevious = computed(
  () =>
    hasPaginationHistory.value &&
    cursorIndex.value > 1 &&
    pageCursors.value[cursorIndex.value - 1] !== undefined,
)
const canGoNext = computed(() => hasPaginationHistory.value && hasMore.value)

watch(
  () => [
    filters.type,
    filters.providerId,
    filters.endpointPath,
    filters.model,
    filters.upstreamModel,
    filters.traceId,
    filters.projectId,
  ],
  () => {
    resetPaginationMemory()
    syncFiltersToQuery()
  },
)

watch(
  () => route.query.cursor,
  (value) => {
    const next = typeof value === 'string' ? value : ''
    const knownIndex = pageCursors.value.indexOf(next)
    cursorIndex.value = knownIndex >= 0 ? knownIndex : next ? 1 : 0
  },
)

watch(
  () => route.query.traceId,
  (value) => {
    const next = typeof value === 'string' ? value : ''
    if (filters.traceId !== next) {
      filters.traceId = next
    }
  },
)

watch(
  () => route.query.projectId,
  (value) => {
    const next = typeof value === 'string' ? Number(value) || 0 : 0
    if (filters.projectId !== next) {
      filters.projectId = next
    }
  },
)

function rowKey(r: RequestView) {
  return r.id
}

function currentSearchParams(): URLSearchParams {
  return new URLSearchParams(window.location.search)
}

function currentAppPathname(): string {
  const pathname = window.location.pathname
  if (!appBase) return pathname
  if (pathname === appBase) return '/'
  if (pathname.startsWith(`${appBase}/`)) return pathname.slice(appBase.length)
  return pathname
}

function replaceBrowserUrl(pathname: string, searchParams = currentSearchParams()) {
  const query = searchParams.toString()
  const basePath = appBase ? `${appBase}${pathname}` : pathname
  window.history.replaceState(window.history.state, '', `${basePath}${query ? `?${query}` : ''}`)
}

function pushCursorQuery(nextCursor: string) {
  const query = { ...route.query }
  if (nextCursor) query.cursor = nextCursor
  else delete query.cursor
  router.push({ name: route.name ?? 'requests', params: route.params, query })
}

function replaceRequestDetailUrl(requestId: string) {
  replaceBrowserUrl(`/requests/${encodeURIComponent(requestId)}`)
}

function replaceRequestsUrl() {
  replaceBrowserUrl('/requests')
}

function openDetails(r: RequestView) {
  const key = `request:${r.id}`
  if (panel.isActive(key)) {
    panel.close()
    replaceRequestsUrl()
    return
  }
  panel.open(
    RequestDetailsPanel,
    {
      requestId: r.id,
      providers: providers.value,
      onSelectedRequest: replaceRequestDetailUrl,
    },
    { key, width: '520px' },
  )
  replaceRequestDetailUrl(r.id)
}

function rowSelected(r: RequestView) {
  return panel.isActive(`request:${r.id}`)
}

watch(
  () => panel.activeKey.value,
  (key) => {
    if (!currentAppPathname().startsWith('/requests/')) return
    if (typeof key === 'string' && key.startsWith('request:')) return
    replaceRequestsUrl()
  },
)

const columns = computed<AutoDataTableColumn<RequestView>[]>(() => {
  const base: AutoDataTableColumn<RequestView>[] = [{ key: 'createdAt', header: '时间' }]
  if (filters.type === 'all') {
    base.push({ key: 'type', header: '类型' })
  }
  base.push(
    { key: 'userMessagePreview', header: '用户消息' },
    {
      key: 'projectId',
      header: '项目',
      headerClass: filters.projectId ? 'shadow-[inset_0_-2px_0_var(--color-accent)]' : '',
    },
    {
      key: 'providerId',
      header: '渠道',
      headerClass: filters.providerId ? 'shadow-[inset_0_-2px_0_var(--color-accent)]' : '',
    },
    {
      key: 'endpointPath',
      header: '端点',
      headerClass: filters.endpointPath ? 'shadow-[inset_0_-2px_0_var(--color-accent)]' : '',
    },
    {
      key: 'model',
      headerClass:
        filters.model || filters.upstreamModel ? 'shadow-[inset_0_-2px_0_var(--color-accent)]' : '',
    },
    { key: 'status', header: '状态' },
    { key: 'tokens', header: 'Token' },
    { key: 'cost', header: '成本', align: 'right' },
    { key: 'timeSpentMs', header: '耗时', align: 'right' },
  )
  return base
})

const providerOptions = computed<ColumnFilterOption<number>[]>(() =>
  providers.value.map((p) => ({ value: p.id, label: p.name })),
)
const projectOptions = computed<ColumnFilterOption<number>[]>(() =>
  projects.value.map((p) => ({ value: p.id, label: p.name })),
)
const endpointOptions = computed<ColumnFilterOption<string>[]>(() =>
  endpoints.value.map((e) => ({ value: e.path, label: e.path })),
)
const modelOptions = computed<ColumnFilterOption<string>[]>(() =>
  models.value.map((m) => ({ value: m.name, label: m.name })),
)
const upstreamModelOptions = computed<ColumnFilterOption<string>[]>(() => {
  const seen = new Set<string>()
  const opts: ColumnFilterOption<string>[] = []
  for (const r of requests.value) {
    if (r.upstreamModel && !seen.has(r.upstreamModel)) {
      seen.add(r.upstreamModel)
      opts.push({ value: r.upstreamModel, label: r.upstreamModel })
    }
  }
  for (const p of providers.value) {
    if (!p.providerModels) continue
    for (const m of p.providerModels) {
      if (m.upstreamModelName && !seen.has(m.upstreamModelName)) {
        seen.add(m.upstreamModelName)
        opts.push({ value: m.upstreamModelName, label: m.upstreamModelName })
      }
      if (!m.upstreamModelName && m.model && !seen.has(m.model)) {
        seen.add(m.model)
        opts.push({ value: m.model, label: m.model })
      }
    }
  }
  return opts
})

function activeFilterCount(): number {
  let n = 0
  if (filters.providerId) n++
  if (filters.endpointPath) n++
  if (filters.model) n++
  if (filters.upstreamModel) n++
  if (filters.traceId) n++
  if (filters.projectId) n++
  return n
}

function clearAllFilters() {
  filters.providerId = 0
  filters.endpointPath = ''
  filters.model = ''
  filters.upstreamModel = ''
  filters.traceId = ''
  filters.projectId = 0
}

function clearTraceFilter() {
  filters.traceId = ''
}

function syncFiltersToQuery() {
  const query = currentSearchParams()
  const currentTrace = query.get('traceId') ?? ''
  const currentProject = Number(query.get('projectId') ?? '') || 0
  if (filters.traceId) {
    query.set('traceId', filters.traceId)
  } else {
    query.delete('traceId')
  }
  if (filters.projectId) {
    query.set('projectId', String(filters.projectId))
  } else {
    query.delete('projectId')
  }
  query.delete('cursor')
  if (
    filters.traceId === currentTrace &&
    filters.projectId === currentProject &&
    !currentCursor.value
  )
    return
  replaceBrowserUrl(currentAppPathname(), query)
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
  const nextCursor = requestsQuery.data.value?.pagination.nextCursor
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

type RequestState = 'pending' | 'ok' | 'err'
function requestState(r: RequestView): RequestState {
  // status: 0=Pending 1=HeaderReceived 2=Completed 3=Failed
  if (r.status === 0 || r.status === 1) return 'pending'
  if (r.status === 2) return 'ok'
  return 'err'
}

function formatTimeSpent(ms: number | undefined): string {
  if (ms === undefined) return '—'
  if (ms < 1000) return `${ms}ms`
  return `${parseFloat((ms / 1000).toFixed(1))}s`
}

function outputSpeed(r: RequestView): string | null {
  if (!r.outputTokens || !r.timeSpentMs) return null
  const seconds = (r.timeSpentMs - (r.ttftMs ?? 0)) / 1000
  if (seconds <= 0) return null
  return (r.outputTokens / seconds).toFixed(0)
}

function inputSideTokens(r: RequestView): number {
  return (
    (r.inputTokens || 0) +
    (r.cacheReadTokens || 0) +
    (r.cacheWriteTokens || 0) +
    (r.cacheWrite1hTokens || 0)
  )
}

function totalTokens(r: RequestView): number {
  return inputSideTokens(r) + (r.outputTokens || 0)
}

function cacheHitRate(r: RequestView): number | null {
  const denominator = inputSideTokens(r)
  if (denominator <= 0 || !r.cacheReadTokens) return null
  return r.cacheReadTokens / denominator
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-end justify-between gap-3 flex-wrap">
      <Field label="类型" as="div">
        <SegmentedControl v-model="filters.type" :options="typeOptions" />
      </Field>
      <div class="flex items-center gap-2">
        <button
          v-if="activeFilterCount() > 0"
          type="button"
          class="inline-flex items-center gap-1 px-1.5 py-0.5 bg-transparent border-0 rounded-xs text-xs text-ink-faint cursor-pointer transition-colors hover:text-ink hover:bg-surface-100"
          @click="clearAllFilters"
        >
          <Icon name="close" :size="11" />
          <span>清除筛选 ({{ activeFilterCount() }})</span>
        </button>
        <span class="text-xs text-ink-faint tabular-nums">
          {{ requests.length }} 条<span v-if="hasMore">（还有更多）</span>
        </span>
        <IconButton title="刷新" aria-label="刷新" @click="requestsQuery.refetch()">
          <Icon name="refresh" :size="13" />
        </IconButton>
        <AutoRefreshSelect v-model="prefs.requestsRefreshMs" />
      </div>
    </div>

    <div
      v-if="filters.traceId"
      class="flex items-center justify-between gap-3 rounded-md border border-line bg-surface-0 px-3 py-2"
    >
      <div class="min-w-0 flex items-center gap-2">
        <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">追踪</span>
        <span class="min-w-0 truncate font-mono text-xs text-ink" :title="filters.traceId">
          {{ filters.traceId }}
        </span>
      </div>
      <button
        type="button"
        class="inline-flex items-center gap-1 px-1.5 py-0.5 bg-transparent border-0 rounded-xs text-xs text-ink-faint cursor-pointer transition-colors hover:text-ink hover:bg-surface-100"
        @click="clearTraceFilter"
      >
        <Icon name="close" :size="11" />
        <span>清除</span>
      </button>
    </div>

    <DataCard>
      <AutoDataTable
        :columns="columns"
        :items="requests"
        :row-key="rowKey"
        :selected="rowSelected"
        :new-row-keys="newRowKeys"
        :on-row-click="(r) => openDetails(r)"
      >
        <template #header-projectId>
          <ColumnFilter
            v-model.number="filters.projectId"
            label="项目"
            :options="projectOptions"
            :empty-value="0"
            placeholder="过滤项目…"
          />
        </template>
        <template #header-providerId>
          <ColumnFilter
            v-model.number="filters.providerId"
            label="渠道"
            :options="providerOptions"
            :empty-value="0"
            placeholder="过滤渠道…"
          />
        </template>
        <template #header-endpointPath>
          <ColumnFilter
            v-model="filters.endpointPath"
            label="端点"
            :options="endpointOptions"
            placeholder="过滤端点…"
          />
        </template>
        <template #header-model>
          <ColumnFilter
            v-model="filters.model"
            label="模型"
            :options="modelOptions"
            placeholder="按路由的模型过滤"
          />
          <ColumnFilter
            v-model="filters.upstreamModel"
            label="上游"
            :options="upstreamModelOptions"
            placeholder="按实际发到上游的模型过滤"
          />
        </template>
        <template #cell-createdAt="{ row }">
          <div class="flex flex-col leading-tight">
            <span class="font-mono tabular-nums text-ink">{{
              formatTimeParts(row.createdAt).time
            }}</span>
            <span class="font-mono text-2xs text-ink-faint">{{
              formatTimeParts(row.createdAt).date
            }}</span>
          </div>
        </template>
        <template #cell-type="{ row }">
          <Tag :variant="row.type === 0 ? 'accent' : 'muted'">{{
            row.type === 0 ? 'META' : 'UP'
          }}</Tag>
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
          <span v-if="row.projectId" class="font-medium">{{ projectLabel(row.projectId) }}</span>
          <span v-else class="text-ink-faint">—</span>
        </template>
        <template #cell-providerId="{ row }">
          <span v-if="row.providerId" class="font-medium">{{ providerLabel(row.providerId) }}</span>
          <span v-else class="text-ink-faint">—</span>
        </template>
        <template #cell-endpointPath="{ row }">
          <span class="font-mono text-ink-faint">{{ row.endpointPath }}</span>
        </template>
        <template #cell-model="{ row }">
          <div class="flex flex-col leading-tight">
            <span v-if="row.model" class="font-mono text-ink">{{ row.model }}</span>
            <span v-else class="text-ink-faint">—</span>
            <span
              v-if="row.model && row.upstreamModel && row.model !== row.upstreamModel"
              class="font-mono text-2xs text-ink-faint"
              >{{ row.upstreamModel }}</span
            >
          </div>
        </template>
        <template #cell-status="{ row }">
          <div class="inline-flex items-center gap-1.5">
            <span
              v-if="requestState(row) === 'pending'"
              class="inline-flex items-center px-1.5 py-0.5 rounded-[5px] font-mono text-2xs leading-[1.2] bg-surface-100 text-ink-muted border border-line-soft"
              >...</span
            >
            <span
              v-else-if="requestState(row) === 'ok'"
              class="inline-flex items-center px-1.5 py-0.5 rounded-[5px] text-2xs leading-[1.2] bg-ok-faint text-ok-ink border border-transparent"
              >成功</span
            >
            <span
              v-else
              class="inline-flex items-center px-1.5 py-0.5 rounded-[5px] text-2xs leading-[1.2] bg-err-faint text-err-ink border border-transparent"
              >{{ finishReasonLabel(row.finishReason) }}</span
            >
          </div>
        </template>
        <template #cell-tokens="{ row }">
          <div class="text-xs">
            <span class="font-mono tabular-nums text-ink">
              {{ totalTokens(row).toLocaleString() }}
            </span>
            <div
              v-if="row.cacheReadTokens || row.cacheWriteTokens || row.cacheWrite1hTokens"
              class="flex items-center gap-1.5 mt-0.5 text-ink-faint text-2xs"
            >
              <span v-if="cacheHitRate(row) != null"
                >{{ parseFloat(((cacheHitRate(row) ?? 0) * 100).toFixed(2)) }}%</span
              >
            </div>
          </div>
        </template>
        <template #cell-cost="{ row }">
          <div class="flex justify-end">
            <MoneyDisplay :amount="row.modelCost ?? null" :currency="row.modelCostCurrency || ''" />
          </div>
        </template>
        <template #cell-timeSpentMs="{ row }">
          <div class="flex flex-col items-end leading-tight">
            <span class="font-mono tabular-nums text-ink">{{
              formatTimeSpent(row.timeSpentMs)
            }}</span>
            <span
              v-if="row.ttftMs != null || outputSpeed(row)"
              class="font-mono text-2xs text-ink-faint tabular-nums"
            >
              <span v-if="row.ttftMs != null" title="TTFT">{{ formatTimeSpent(row.ttftMs) }}</span>
              <span v-if="row.ttftMs != null && outputSpeed(row)" class="px-0.5">&middot;</span>
              <span v-if="outputSpeed(row)" title="输出速度"
                >{{ outputSpeed(row) }}<span class="pl-0.5">tps</span></span
              >
            </span>
          </div>
        </template>
        <template #empty>
          <span v-if="loading">加载中…</span>
          <span v-else>暂无请求</span>
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

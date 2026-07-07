<script setup lang="ts">
import { reactive, watch, computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import { useProvidersMap } from '@/composables/useProvidersMap'
import { useProjectsMap } from '@/composables/useProjectsMap'
import type { RequestView, EndpointLabel, ModelLabel } from '@/api'
import {
  listEndpointLabels,
  listModelLabels,
  listRequests,
  listUpstreamModelLabels,
} from '@/api/client'
import { queryKeys, type RequestsFilters } from '@/api/queryKeys'
import RequestDetailsPanel from '@/components/RequestDetailsPanel.vue'
import { useSidePanel } from '@/composables/useSidePanel'
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
  TimeRangeFilter,
  MoneyDisplay,
  type AutoDataTableColumn,
  type ColumnFilterOption,
} from '@/ui'
import { finishReasonLabel, isUnifiedEndpoint, unifiedEndpointName } from '@/utils/requestLabels'

const panel = useSidePanel()
const route = useRoute()
const router = useRouter()
const { providers, providerLabel } = useProvidersMap()
const { projects, projectLabel } = useProjectsMap()

// Track new rows for animation on refresh
const previousRequestIds = ref(new Set<string | number>())
const newRowKeys = ref(new Set<string | number>())
const isRefreshing = ref(false)

type RequestKind = 'meta' | 'upstream' | 'all'

const filters = reactive({
  type: 'meta' as RequestKind,
  providerId: 0,
  endpointPath: '',
  model: '',
  upstreamModel: '',
  traceId: typeof route.query.traceId === 'string' ? route.query.traceId : '',
  projectId: typeof route.query.projectId === 'string' ? Number(route.query.projectId) || 0 : 0,
  startAt: typeof route.query.startAt === 'string' ? route.query.startAt : '',
  endAt: typeof route.query.endAt === 'string' ? route.query.endAt : '',
  emptyResponse: 0,
  finishReason: 0,
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
  queryKey: queryKeys.labels.endpoints,
  queryFn: listEndpointLabels,
})
const modelsQuery = useQuery({
  queryKey: queryKeys.labels.models,
  queryFn: listModelLabels,
})
const upstreamModelsQuery = useQuery({
  queryKey: queryKeys.labels.upstreamModels,
  queryFn: listUpstreamModelLabels,
})
const endpoints = computed<EndpointLabel[]>(() => endpointsQuery.data.value ?? [])
const models = computed<ModelLabel[]>(() => modelsQuery.data.value ?? [])
const upstreamModels = computed<string[]>(() => upstreamModelsQuery.data.value ?? [])

const requestFilters = computed<RequestsFilters>(() => {
  const out: {
    type?: number
    providerId?: number
    endpointPath?: string
    model?: string
    upstreamModel?: string
    traceId?: string
    projectId?: number
    startAt?: string
    endAt?: string
    emptyResponse?: boolean
    finishReason?: number
  } = {}
  if (filters.type === 'meta') out.type = 0
  else if (filters.type === 'upstream') out.type = 1
  if (filters.providerId) out.providerId = filters.providerId
  if (filters.endpointPath) out.endpointPath = filters.endpointPath
  if (filters.model) out.model = filters.model
  if (filters.upstreamModel) out.upstreamModel = filters.upstreamModel
  if (filters.traceId) out.traceId = filters.traceId
  if (filters.projectId) out.projectId = filters.projectId
  if (filters.startAt) out.startAt = filters.startAt
  if (filters.endAt) out.endAt = filters.endAt
  if (filters.emptyResponse) out.emptyResponse = true
  if (filters.finishReason) out.finishReason = filters.finishReason
  return out
})

const currentCursor = computed(() =>
  typeof route.query.cursor === 'string' ? route.query.cursor : '',
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
})
const requests = computed<RequestView[]>(() => requestsQuery.data.value?.items ?? [])

// When requests change after refresh, compute which ones are new
watch(
  requests,
  (newRequests) => {
    if (!isRefreshing.value) return
    isRefreshing.value = false
    const currentIds = new Set(newRequests.map((r) => rowKey(r)))
    const fresh = new Set<string | number>()
    for (const id of currentIds) {
      if (!previousRequestIds.value.has(id)) {
        fresh.add(id)
      }
    }
    newRowKeys.value = fresh
    previousRequestIds.value = currentIds
    if (fresh.size > 0) {
      setTimeout(() => {
        newRowKeys.value = new Set()
      }, 100)
    }
  },
  { flush: 'post' },
)
const loading = computed(() => requestsQuery.isLoading.value || requestsQuery.isFetching.value)
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
    filters.startAt,
    filters.endAt,
    filters.emptyResponse,
    filters.finishReason,
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

watch(
  () => route.query.startAt,
  (value) => {
    const next = typeof value === 'string' ? value : ''
    if (filters.startAt !== next) {
      filters.startAt = next
    }
  },
)

watch(
  () => route.query.endAt,
  (value) => {
    const next = typeof value === 'string' ? value : ''
    if (filters.endAt !== next) {
      filters.endAt = next
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

function requestHref(r: RequestView) {
  return router.resolve({ name: 'requestDetail', params: { requestId: r.id } }).href
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
    { key: 'status', header: '完成原因', headerClass: filters.finishReason ? 'shadow-[inset_0_-2px_0_var(--color-accent)]' : '' },
    { key: 'tokens', header: 'Token', headerClass: filters.emptyResponse ? 'shadow-[inset_0_-2px_0_var(--color-accent)]' : '' },
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
const endpointNameByPath = computed(() => {
  const m = new Map<string, string>()
  for (const e of endpoints.value) m.set(e.path, e.name)
  return m
})

function endpointDisplay(path: string | undefined | null): { name: string; unified: boolean } {
  if (!path) return { name: '—', unified: false }
  if (isUnifiedEndpoint(path)) return { name: unifiedEndpointName(path), unified: true }
  return { name: endpointNameByPath.value.get(path) || path, unified: false }
}

const endpointOptions = computed<ColumnFilterOption<string>[]>(() =>
  endpoints.value.map((e) => {
    const d = endpointDisplay(e.path)
    return { value: e.path, label: d.unified ? `${d.name} [统一网关]` : d.name }
  }),
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
  for (const name of upstreamModels.value) {
    if (name && !seen.has(name)) {
      seen.add(name)
      opts.push({ value: name, label: name })
    }
  }
  return opts
})

// Finish-reason filter options: the 7 fixed finish reasons (1..7) plus a
// "失败" catch-all (sentinel -1 = all reasons except 正常结束/3). "Pending"
// (NULL finish_reason) is intentionally excluded — in-flight rows still show
// but are not a filter value. Labels mirror finishReasonLabel's fixed cases.
const finishReasonOptions: ColumnFilterOption<number>[] = [
  { value: -1, label: '非正常结束' },
  { value: 3, label: '正常结束' },
  { value: 1, label: '内部错误' },
  { value: 2, label: '已取消' },
  { value: 4, label: '请求头超时' },
  { value: 5, label: '读取超时' },
  { value: 6, label: '流式错误' },
  { value: 7, label: '控制台打断' },
]

function activeFilterCount(): number {
  let n = 0
  if (filters.providerId) n++
  if (filters.endpointPath) n++
  if (filters.model) n++
  if (filters.upstreamModel) n++
  if (filters.traceId) n++
  if (filters.projectId) n++
  if (filters.startAt || filters.endAt) n++
  if (filters.emptyResponse) n++
  if (filters.finishReason) n++
  return n
}

function clearAllFilters() {
  filters.providerId = 0
  filters.endpointPath = ''
  filters.model = ''
  filters.upstreamModel = ''
  filters.traceId = ''
  filters.projectId = 0
  filters.startAt = ''
  filters.endAt = ''
  filters.emptyResponse = 0
  filters.finishReason = 0
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
  if (filters.startAt) {
    query.set('startAt', filters.startAt)
  } else {
    query.delete('startAt')
  }
  if (filters.endAt) {
    query.set('endAt', filters.endAt)
  } else {
    query.delete('endAt')
  }
  query.delete('cursor')
  const currentStart = query.get('startAt') ?? ''
  const currentEnd = query.get('endAt') ?? ''
  if (
    filters.traceId === currentTrace &&
    filters.projectId === currentProject &&
    filters.startAt === currentStart &&
    filters.endAt === currentEnd &&
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

function resetCursorAndReload() {
  previousRequestIds.value = new Set(requests.value.map((r) => rowKey(r)))
  isRefreshing.value = true
  requestsQuery.refetch()
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-end justify-between gap-3 flex-wrap">
      <div class="flex items-end gap-3 flex-wrap">
        <Field label="类型" as="div">
          <SegmentedControl v-model="filters.type" :options="typeOptions" />
        </Field>
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
        <IconButton title="刷新" aria-label="刷新" @click="resetCursorAndReload">
          <Icon name="refresh" :size="13" />
        </IconButton>
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
        :row-href="requestHref"
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
        <template #header-tokens>
          <ColumnFilter
            v-model.number="filters.emptyResponse"
            label="Token"
            :options="[{ value: 1, label: '空回' }]"
            :empty-value="0"
            :searchable="false"
          />
        </template>
        <template #header-status>
          <ColumnFilter
            v-model.number="filters.finishReason"
            label="完成原因"
            :options="finishReasonOptions"
            :empty-value="0"
            :searchable="false"
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
          <div class="flex items-center gap-1.5 min-w-0 max-w-2xs">
            <span class="truncate text-ink" :title="row.endpointPath">{{
              endpointDisplay(row.endpointPath).name
            }}</span>
            <Tag v-if="endpointDisplay(row.endpointPath).unified" variant="accent" title="统一网关">U</Tag>
          </div>
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
              >处理中</span
            >
            <span
              v-else-if="requestState(row) === 'ok'"
              class="inline-flex items-center px-1.5 py-0.5 rounded-[5px] text-2xs leading-[1.2] bg-ok-faint text-ok-ink border border-transparent"
              >{{ finishReasonLabel(row.finishReason) }}</span
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

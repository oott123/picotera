<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import type { RequestView, ProviderLabel, RequestLiveView } from '@/api'
import { listRequestSpans, getRequestLive, interruptRequest } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { StateText, Field, Tag, IconButton, Icon, Tabs, MoneyDisplay, Button } from '@/ui'
import RawArtifactView from './RawArtifactView.vue'
import LogsArtifactView from './LogsArtifactView.vue'
import ConversationArtifactView from './ConversationArtifactView.vue'
import TimedRawView from './TimedRawView.vue'
import { useRequestDetailUiState, type DetailTab } from '@/composables/useRequestDetailUiState'

const props = defineProps<{ requestId: string; providers?: ProviderLabel[] }>()
const emit = defineEmits<{ selectedRequest: [requestId: string] }>()

const selectedId = ref<string>('')
const spansQuery = useQuery({
  queryKey: computed(() => queryKeys.requestSpans.detail(props.requestId)),
  queryFn: () => listRequestSpans(props.requestId),
})
const spans = computed<RequestView[]>(() => {
  const items = spansQuery.data.value ?? []
  return [...items].sort((a, b) => {
    const aMeta = a.id === a.spanId ? 0 : 1
    const bMeta = b.id === b.spanId ? 0 : 1
    if (aMeta !== bMeta) return aMeta - bMeta
    const at = a.createdAt || ''
    const bt = b.createdAt || ''
    return at < bt ? -1 : at > bt ? 1 : 0
  })
})
const loading = computed(() => spansQuery.isLoading.value)
const error = computed(() => spansQuery.error.value?.message ?? '')

const providersMap = computed(() => {
  const m = new Map<number, ProviderLabel>()
  for (const p of props.providers ?? []) m.set(p.id, p)
  return m
})

const meta = computed(() => spans.value.find((s) => s.id === s.spanId) ?? null)
const upstreams = computed(() => spans.value.filter((s) => s.id !== s.spanId))
const selected = computed(() => spans.value.find((s) => s.id === selectedId.value) ?? null)

// In-flight = pending (0) or header-received (1); these rows have live status
// in process memory and can be interrupted from the dashboard.
function isInFlight(r: RequestView | null | undefined): boolean {
  return !!r && (r.status === 0 || r.status === 1)
}
const selectedInFlight = computed(() => isInFlight(selected.value))

const queryClient = useQueryClient()
const liveQuery = useQuery({
  queryKey: computed(() => queryKeys.requestLive.detail(selectedId.value)),
  queryFn: () => getRequestLive(selectedId.value),
  enabled: computed(() => !!selectedId.value && selectedInFlight.value),
  staleTime: 0,
})
const live = computed<RequestLiveView | null>(() => liveQuery.data.value ?? null)

const interruptMutation = useMutation({
  mutationFn: (id: string) => interruptRequest(id),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: queryKeys.requestSpans.detail(props.requestId) })
    queryClient.invalidateQueries({ queryKey: queryKeys.requestLive.detail(selectedId.value) })
  },
})
function interruptSelected() {
  if (!selected.value) return
  interruptMutation.mutate(selected.value.id)
}

// Manual refresh only — no automatic polling. Refetches the span list and,
// when the selected span is in-flight, its live snapshot.
function refreshAll() {
  spansQuery.refetch()
  if (selectedInFlight.value) liveQuery.refetch()
}

function livePhaseLabel(phase: string | undefined): string {
  switch (phase) {
    case 'pending':
      return '等待上游响应'
    case 'headerReceived':
      return '已收到响应头'
    case 'streaming':
      return '流式接收中'
    default:
      return '—'
  }
}

function formatBytes(n: number | undefined | null) {
  if (n === undefined || n === null) return '—'
  return `${n.toLocaleString()} B`
}

function ensureSelectedRequest(sorted = spans.value) {
  if (!selectedId.value || !sorted.find((s) => s.id === selectedId.value)) {
    const match = sorted.find((s) => s.id === props.requestId)
    selectedId.value = match?.id ?? sorted[0]?.id ?? ''
  }
}

watch(
  () => props.requestId,
  () => {
    selectedId.value = ''
    ensureSelectedRequest()
  },
)
watch(spans, (sorted) => ensureSelectedRequest(sorted), { immediate: true })

function formatTime(iso: string | undefined) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

function formatTimeSpent(ms: number | undefined | null) {
  if (ms === undefined || ms === null) return '—'
  if (ms < 1000) return `${ms} ms`
  return `${(ms / 1000).toFixed(2)} s`
}

function outputSpeed(r: RequestView | null): string {
  if (!r || !r.outputTokens || !r.timeSpentMs) return '—'
  const seconds = (r.timeSpentMs - (r.ttftMs ?? 0)) / 1000
  if (seconds <= 0) return '—'
  return `${(r.outputTokens / seconds).toFixed(0)} tok/s`
}

function fmtNum(n: number | undefined | null) {
  return n === undefined || n === null ? '—' : n.toLocaleString()
}

function providerLabel(id: number | undefined | null) {
  if (!id) return '—'
  const p = providersMap.value.get(id)
  return p ? p.name : `#${id}`
}

// inferredModelSource: 1 = 思维链签名, 2 = 响应结构, 其余/缺省 = 无来源。
function inferredModelSourceLabel(source: number | undefined | null): string {
  switch (source) {
    case 1:
      return '思维链'
    case 2:
      return '响应'
    default:
      return ''
  }
}

function statusVariantTag(code: number | undefined | null): 'ok' | 'default' | 'muted' | 'accent' {
  if (!code) return 'muted'
  if (code >= 200 && code < 300) return 'ok'
  return 'default'
}

function statusCodeClass(code: number | undefined | null) {
  const c = code ?? 0
  if (c >= 200 && c < 300) return 'bg-ok-faint text-ok-ink'
  if (c >= 400 && c < 500) return 'bg-warn-faint text-warn-ink'
  return 'bg-err-faint text-err-ink'
}

type RequestState = 'pending' | 'ok' | 'warn' | 'err'
function requestState(r: RequestView): RequestState {
  // status: 0=Pending 1=HeaderReceived 2=Completed 3=Failed
  if (r.status === 0 || r.status === 1) return 'pending'
  if (r.statusCode === undefined || r.statusCode === null) return 'err'
  if (r.statusCode >= 200 && r.statusCode < 300) return 'ok'
  if (r.statusCode >= 400 && r.statusCode < 500) return 'warn'
  return 'err'
}

function typeLabel(t: number) {
  return t === 0 ? 'META' : 'UPSTREAM'
}

function statusLabel(s: number) {
  switch (s) {
    case 0:
      return 'pending'
    case 1:
      return 'header'
    case 2:
      return 'completed'
    case 3:
      return 'failed'
    default:
      return String(s)
  }
}

import { finishReasonLabel } from '@/utils/requestLabels'

function finishReasonVariant(
  reason: number | undefined | null,
): 'ok' | 'default' | 'muted' | 'accent' {
  if (reason === undefined || reason === null) return 'muted'
  if (reason === 3) return 'ok' // EOF is normal
  return 'default'
}

function selectRequest(requestId: string) {
  if (selectedId.value === requestId) return
  selectedId.value = requestId
  emit('selectedRequest', requestId)
}

const {
  detailTab,
  requestBodyView,
  requestHeadersOpen,
  responseSubView,
  responseHeadersOpen,
  responseThinkingOpen,
  liveShowTimings,
} = useRequestDetailUiState()
const isMeta = computed(() => !!selected.value && selected.value.id === selected.value.spanId)
const detailTabs = computed(() => {
  const base: { value: DetailTab; label: string }[] = [
    { value: 'overview', label: '概览' },
    { value: 'request', label: '原始请求' },
    { value: 'response', label: '原始响应' },
    { value: 'conversation', label: '对话' },
  ]
  if (isMeta.value) base.push({ value: 'logs', label: '日志' })
  return base
})
watch(detailTabs, (tabs) => {
  if (!tabs.find((t) => t.value === detailTab.value)) {
    detailTab.value = 'overview'
  }
})
</script>

<template>
  <div class="flex flex-col gap-4">
    <StateText v-if="loading && !spans.length" :dashed="false" compact>加载中…</StateText>
    <div
      v-else-if="error"
      class="rounded-md border border-line bg-err-faint px-3 py-2 text-sm text-err-ink"
    >
      {{ error }}
    </div>
    <StateText v-else-if="!spans.length" :dashed="false" compact>暂无请求详情</StateText>
    <template v-else-if="spans.length">
      <div class="flex items-start gap-2">
        <div class="flex gap-2 overflow-x-auto -mx-1 px-1 pb-1 flex-1 min-w-0">
          <button
            v-if="meta"
            type="button"
            class="flex flex-col gap-1 shrink-0 min-w-44 max-w-56 p-2.5 rounded-md border text-left transition-colors cursor-pointer"
            :class="
              selectedId === meta.id
                ? 'border-accent bg-accent-faint'
                : 'border-line hover:bg-surface-50'
            "
            @click="selectRequest(meta.id)"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="text-2xs font-semibold text-accent-ink uppercase tracking-[0.04em]"
                >meta</span
              >
              <span
                class="inline-flex items-center px-1.5 py-0.5 rounded-[5px] font-mono text-2xs leading-[1.2]"
                :class="
                  requestState(meta) === 'pending'
                    ? 'bg-surface-100 text-ink-muted border border-line-soft'
                    : statusCodeClass(meta.statusCode)
                "
                >{{ requestState(meta) === 'pending' ? '处理中' : meta.statusCode }}</span
              >
            </div>
            <div class="font-mono tabular-nums text-2xs text-ink-faint">
              {{ formatTimeSpent(meta.timeSpentMs) }}
            </div>
          </button>
          <button
            v-for="s in upstreams"
            :key="s.id"
            type="button"
            class="flex flex-col gap-1 shrink-0 min-w-44 max-w-56 p-2.5 rounded-md border text-left transition-colors cursor-pointer"
            :class="
              selectedId === s.id
                ? 'border-accent bg-accent-faint'
                : 'border-line hover:bg-surface-50'
            "
            @click="selectRequest(s.id)"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="text-2xs font-semibold text-ink-muted uppercase tracking-[0.04em]">
                {{ providerLabel(s.providerId) }}</span
              >
              <span
                class="inline-flex items-center px-1.5 py-0.5 rounded-[5px] font-mono text-2xs leading-[1.2]"
                :class="
                  requestState(s) === 'pending'
                    ? 'bg-surface-100 text-ink-muted border border-line-soft'
                    : statusCodeClass(s.statusCode)
                "
                >{{ requestState(s) === 'pending' ? '处理中' : s.statusCode }}</span
              >
            </div>
            <div class="font-mono tabular-nums text-2xs text-ink-faint">
              {{ formatTimeSpent(s.timeSpentMs) }}
            </div>
          </button>
        </div>
        <IconButton title="刷新" aria-label="刷新" @click="refreshAll()">
          <Icon name="refresh" :size="13" />
        </IconButton>
      </div>

      <template v-if="selected">
        <section
          v-if="selectedInFlight"
          class="flex flex-col gap-2.5 rounded-md border border-line bg-surface-50 p-3"
        >
          <div class="flex items-center justify-between gap-2">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
              >实时状态</span
            >
            <Button
              variant="danger"
              size="sm"
              :disabled="interruptMutation.isPending.value"
              @click="interruptSelected()"
            >
              {{ interruptMutation.isPending.value ? '打断中…' : '打断' }}
            </Button>
          </div>
          <div class="grid grid-cols-2 gap-2.5">
            <Field label="阶段" as="div">
              <Tag :variant="live?.phase === 'streaming' ? 'accent' : 'muted'">{{
                livePhaseLabel(live?.phase)
              }}</Tag>
            </Field>
            <Field label="状态码" as="div">
              <span class="font-mono tabular-nums text-sm">{{ live?.statusCode || '—' }}</span>
            </Field>
            <Field label="已收到字节" as="div">
              <span class="font-mono tabular-nums text-sm">{{
                formatBytes(live?.bytesReceived)
              }}</span>
            </Field>
            <Field label="最近更新" as="div">
              <span class="font-mono tabular-nums text-xs">{{
                formatTime(live?.lastChunkAt)
              }}</span>
            </Field>
          </div>
          <div v-if="live?.body" class="flex flex-col gap-1.5">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
              >响应体（至今）</span
            >
            <label
              v-if="live?.timings?.length"
              class="flex items-center gap-1.5 text-2xs text-ink-muted select-none"
            >
              <input type="checkbox" v-model="liveShowTimings" class="accent-accent-ink" />
              显示到达时间
            </label>
            <TimedRawView
              v-if="liveShowTimings && live?.timings?.length"
              :body="live.body"
              :timings="live.timings!"
            />
            <pre
              v-else
              class="font-mono text-xs whitespace-pre-wrap break-all bg-surface-0 border border-line-soft rounded-md p-3 m-0 text-ink max-h-80 overflow-auto"
              >{{ live.body }}</pre
            >
          </div>
          <StateText v-else-if="liveQuery.isFetching.value" :dashed="false" compact
            >加载实时状态…</StateText
          >
        </section>
        <Tabs
          :model-value="detailTab"
          :tabs="detailTabs"
          @update:model-value="(v: string | number) => (detailTab = v as DetailTab)"
        />
        <template v-if="detailTab === 'overview'">
          <section class="flex flex-col gap-2.5">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
              >基本信息</span
            >
            <div class="grid grid-cols-2 gap-2.5">
              <Field label="ID" as="div" class="col-span-2">
                <span class="font-mono text-xs text-ink break-all">{{ selected.id }}</span>
              </Field>
              <Field label="类型" as="div">
                <Tag :variant="selected.type === 0 ? 'accent' : 'muted'">{{
                  typeLabel(selected.type)
                }}</Tag>
              </Field>
              <Field label="状态" as="div">
                <Tag
                  :variant="
                    requestState(selected) === 'pending'
                      ? 'muted'
                      : statusVariantTag(selected.statusCode)
                  "
                  >{{ statusLabel(selected.status) }}</Tag
                >
              </Field>
              <Field v-if="selected.spanId" label="Span" as="div">
                <span class="font-mono text-xs text-ink break-all">{{ selected.spanId }}</span>
              </Field>
              <Field v-if="selected.parentSpanId" label="Parent Span" as="div">
                <span class="font-mono text-xs text-ink break-all">{{
                  selected.parentSpanId
                }}</span>
              </Field>
              <Field
                v-if="selected.userMessagePreview"
                label="用户消息"
                as="div"
                class="col-span-2"
              >
                <span class="block truncate text-sm text-ink" :title="selected.userMessagePreview">
                  {{ selected.userMessagePreview }}
                </span>
              </Field>
              <Field label="渠道" as="div">
                <span class="font-mono text-sm break-all">{{ providerLabel(selected.providerId) }}</span>
              </Field>
              <Field label="推测渠道" as="div">
                <span class="font-mono text-sm break-all">{{ selected.inferredProvider || '—' }}</span>
              </Field>
              <Field label="模型" as="div">
                <span class="font-mono text-sm break-all">{{ selected.model || '—' }}</span>
              </Field>
              <Field label="推测模型" as="div">
                <span class="inline-flex items-center gap-1.5 min-w-0">
                  <span class="font-mono text-sm break-all">{{ selected.inferredModel || '—' }}</span>
                  <Tag
                    v-if="inferredModelSourceLabel(selected.inferredModelSource)"
                    variant="muted"
                    >{{ inferredModelSourceLabel(selected.inferredModelSource) }}</Tag
                  >
                </span>
              </Field>
              <Field label="端点" as="div">
                <span class="font-mono text-sm break-all">{{ selected.endpointPath || '—' }}</span>
              </Field>
              <Field label="状态码" as="div">
                <span
                  v-if="requestState(selected) === 'pending'"
                  class="inline-flex items-center px-1.5 py-0.5 rounded-[5px] font-mono text-xs bg-surface-100 text-ink-muted border border-line-soft w-fit"
                  >处理中</span
                >
                <span
                  v-else
                  class="inline-flex items-center px-1.5 py-0.5 rounded-[5px] font-mono text-xs border border-transparent w-fit"
                  :class="statusCodeClass(selected.statusCode)"
                  >{{ selected.statusCode }}</span
                >
              </Field>
              <Field label="完成原因" as="div">
                <Tag :variant="finishReasonVariant(selected.finishReason)">
                  {{ finishReasonLabel(selected.finishReason) }}
                </Tag>
              </Field>
              <Field label="时间" as="div">
                <span class="font-mono text-xs">{{ formatTime(selected.createdAt) }}</span>
              </Field>
            </div>
          </section>

          <section class="flex flex-col gap-2.5">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
              >性能</span
            >
            <div class="grid grid-cols-2 gap-2.5">
              <Field label="TTFT" as="div">
                <span class="font-mono tabular-nums text-sm">{{
                  formatTimeSpent(selected.ttftMs)
                }}</span>
              </Field>
              <Field label="总耗时" as="div">
                <span class="font-mono tabular-nums text-sm">{{
                  formatTimeSpent(selected.timeSpentMs)
                }}</span>
              </Field>
              <Field label="输出速度" as="div">
                <span class="font-mono tabular-nums text-sm">{{ outputSpeed(selected) }}</span>
              </Field>
            </div>
          </section>

          <section class="flex flex-col gap-2.5">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
              >Token</span
            >
            <div class="grid grid-cols-2 gap-2.5">
              <Field label="输入" as="div">
                <span class="font-mono tabular-nums text-sm">{{
                  fmtNum(selected.inputTokens)
                }}</span>
              </Field>
              <Field label="输出" as="div">
                <span class="font-mono tabular-nums text-sm">{{
                  fmtNum(selected.outputTokens)
                }}</span>
              </Field>
              <Field label="缓存读取" as="div">
                <span class="font-mono tabular-nums text-sm">{{
                  fmtNum(selected.cacheReadTokens)
                }}</span>
              </Field>
              <Field label="缓存写入" as="div">
                <span class="font-mono tabular-nums text-sm">{{
                  fmtNum(selected.cacheWriteTokens)
                }}</span>
              </Field>
              <Field label="1h 缓存写入" as="div">
                <span class="font-mono tabular-nums text-sm">{{
                  fmtNum(selected.cacheWrite1hTokens)
                }}</span>
              </Field>
            </div>
          </section>

          <section v-if="selected.modelCost != null" class="flex flex-col gap-2.5">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
              >成本</span
            >
            <div class="grid grid-cols-2 gap-2.5">
              <Field label="模型价" as="div">
                <span class="font-mono tabular-nums text-sm">
                  <MoneyDisplay
                    :amount="selected.modelCost ?? null"
                    :currency="selected.modelCostCurrency ?? ''"
                  />
                </span>
              </Field>
            </div>
          </section>

          <section v-if="selected.errorMessage" class="flex flex-col gap-2.5">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
              >错误信息</span
            >
            <pre
              class="font-mono text-xs whitespace-pre-wrap bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink"
              >{{ selected.errorMessage }}</pre
            >
          </section>
        </template>
        <RawArtifactView
          v-else-if="detailTab === 'request'"
          v-model:body-view="requestBodyView"
          v-model:headers-open="requestHeadersOpen"
          :url="selected.requestArtifactUrl"
          kind="request"
        />
        <RawArtifactView
          v-else-if="detailTab === 'response'"
          v-model:body-view="responseSubView"
          v-model:headers-open="responseHeadersOpen"
          v-model:thinking-open="responseThinkingOpen"
          :url="selected.responseArtifactUrl"
          kind="response"
          :request-id="selected.id"
        />
        <ConversationArtifactView
          v-else-if="detailTab === 'conversation'"
          :request-url="selected.requestArtifactUrl"
          :response-url="selected.responseArtifactUrl"
        />
        <LogsArtifactView v-else-if="detailTab === 'logs'" :url="selected.responseArtifactUrl" />
      </template>
    </template>
  </div>
</template>

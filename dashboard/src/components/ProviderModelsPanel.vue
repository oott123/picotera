<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useMutation, useQueries, useQueryClient } from '@tanstack/vue-query'
import AnnotationsEditor from '@/components/AnnotationsEditor.vue'
import type { ProviderView, ProviderModelEntry, ProviderEndpointView } from '@/api'
import {
  fetchProviderModels,
  invalidateProviderEndpoints,
  listProviderEndpoints,
  getProvider,
  updateProviderModels,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { SidePanel, Button, IconButton, Input, Field, StateText, Tag, TagList, Icon } from '@/ui'

type Row = {
  uid: number
  modelName: string
  upstreamModelName: string
  endpoints: string[]
  priority: number
  annotations: Record<string, string>
  disabled: boolean
  expanded: boolean
}

const props = defineProps<{ providerId: number; providerName: string; onSave?: () => void }>()
const emit = defineEmits<{ close: [] }>()
const queryClient = useQueryClient()

const provider = ref<ProviderView | null>(null)
const rows = ref<Row[]>([])
const saving = ref(false)
const error = ref('')
const newModelName = ref('')
const fetching = ref(false)
type MissingRow = { uid: number; modelName: string; upstreamModelName: string }

const fetchSummary = ref<{ added: number; missing: MissingRow[]; removedHint: string[] } | null>(
  null,
)
const pendingDeletions = ref<Record<number, boolean>>({})

let nextUid = 0

type ComparableModel = {
  modelName: string
  upstreamModelName: string
  endpoints: string[]
  priority: number
  annotations: Record<string, string>
  disabled: boolean
}

const queries = useQueries({
  queries: computed(() => [
    {
      queryKey: queryKeys.providers.detail(props.providerId),
      queryFn: () => getProvider(props.providerId),
    },
    {
      queryKey: queryKeys.providerEndpoints.list({ providerId: props.providerId }),
      queryFn: () => listProviderEndpoints(props.providerId),
    },
  ]),
})
const providerEndpoints = computed<ProviderEndpointView[]>(
  () => (queries.value[1]?.data ?? []) as ProviderEndpointView[],
)
const loading = computed(() => queries.value.some((q) => q.isLoading))
const fetchModelsMutation = useMutation({
  mutationFn: fetchProviderModels,
})
const saveProviderMutation = useMutation({
  mutationFn: ({ id, providerModels }: { id: number; providerModels: ProviderModelEntry[] }) =>
    updateProviderModels(id, providerModels),
  onSuccess: () => invalidateProviderEndpoints(queryClient),
})

function entryToRow(entry: ProviderModelEntry): Row {
  return {
    uid: nextUid++,
    modelName: entry.model ?? '',
    upstreamModelName: entry.upstreamModelName ?? '',
    endpoints: [...(entry.endpoints ?? [])],
    priority: entry.priority ?? 0,
    annotations: { ...entry.annotations },
    disabled: entry.disabled ?? false,
    expanded: false,
  }
}

function emptyRow(modelName: string): Row {
  return {
    uid: nextUid++,
    modelName,
    upstreamModelName: '',
    endpoints: [],
    priority: 0,
    annotations: {},
    disabled: false,
    expanded: false,
  }
}

function rowsFromProvider(p: ProviderView): Row[] {
  const list = (p.providerModels ?? []) as ProviderModelEntry[]
  return list
    .map((entry) => entryToRow(entry))
    .sort((a, b) => {
      const cmp = a.modelName.localeCompare(b.modelName)
      if (cmp !== 0) return cmp
      return b.priority - a.priority
    })
}

function pairKey(model: string, upstream: string): string {
  return `${model}\u0000${upstream ?? ''}`
}

function normalizeAnnotations(value: Record<string, string> | undefined): Record<string, string> {
  return Object.fromEntries(Object.entries(value ?? {}).sort(([a], [b]) => a.localeCompare(b)))
}

function comparableRow(row: Row): ComparableModel {
  return {
    modelName: row.modelName,
    upstreamModelName: row.upstreamModelName,
    endpoints: [...row.endpoints].sort(),
    priority: row.priority,
    annotations: normalizeAnnotations(row.annotations),
    disabled: row.disabled,
  }
}

function comparableEntry(entry: ProviderModelEntry): ComparableModel {
  return {
    modelName: entry.model ?? '',
    upstreamModelName: entry.upstreamModelName ?? '',
    endpoints: [...(entry.endpoints ?? [])].sort(),
    priority: entry.priority ?? 0,
    annotations: normalizeAnnotations(entry.annotations),
    disabled: entry.disabled ?? false,
  }
}

function comparableModelSortKey(value: ComparableModel): string {
  return [
    value.modelName,
    value.upstreamModelName,
    String(value.priority),
    value.disabled ? '1' : '0',
    value.endpoints.join('\u0001'),
    JSON.stringify(value.annotations),
  ].join('\u0000')
}

function modelSignatureFromComparable(list: ComparableModel[]): string {
  return JSON.stringify(
    [...list].sort((a, b) => comparableModelSortKey(a).localeCompare(comparableModelSortKey(b))),
  )
}

function rowsToList(list: Row[]): ProviderModelEntry[] {
  const out: ProviderModelEntry[] = []
  for (const row of list) {
    const name = row.modelName.trim()
    if (!name) continue
    const entry: ProviderModelEntry = { model: name }
    if (row.upstreamModelName.trim()) entry.upstreamModelName = row.upstreamModelName.trim()
    if (row.endpoints.length) entry.endpoints = [...row.endpoints]
    if (row.priority) entry.priority = row.priority
    if (row.disabled) entry.disabled = true
    if (Object.keys(row.annotations).length) entry.annotations = { ...row.annotations }
    out.push(entry)
  }
  const seen = new Set<string>()
  return out.filter((e) => {
    const k = pairKey(e.model, e.upstreamModelName ?? '')
    if (seen.has(k)) return false
    seen.add(k)
    return true
  })
}

const modelCount = computed(() => rows.value.length)
const localModelSignature = computed(() =>
  modelSignatureFromComparable(rows.value.map((row) => comparableRow(row))),
)
const serverModelSignature = computed(() =>
  modelSignatureFromComparable(
    ((provider.value?.providerModels ?? []) as ProviderModelEntry[]).map((entry) =>
      comparableEntry(entry),
    ),
  ),
)
const hasUnsavedRows = computed(() => localModelSignature.value !== serverModelSignature.value)

const availableEndpointPaths = computed(() => providerEndpoints.value.map((pe) => pe.endpointPath))

const hasModelsEndpoint = computed(() => !!provider.value?.modelsEndpointUrl)

function resetLocalSummaryState() {
  fetchSummary.value = null
  pendingDeletions.value = {}
}

function applyLoadedData(forceRows = false) {
  error.value = ''
  const pData = queries.value[0]?.data as ProviderView | undefined
  if (!pData) return
  if (pData.id !== props.providerId) return
  const providerChanged = provider.value?.id !== pData.id
  const dirtyBeforeUpdate = hasUnsavedRows.value
  provider.value = pData
  if (forceRows || providerChanged || !dirtyBeforeUpdate) {
    rows.value = rowsFromProvider(pData)
    resetLocalSummaryState()
  }
}

watch(
  () => props.providerId,
  () => {
    provider.value = null
    rows.value = []
    resetLocalSummaryState()
    error.value = ''
  },
)

watch(
  () => queries.value[0]?.data,
  () => applyLoadedData(),
  { immediate: true },
)

function addModel() {
  const name = newModelName.value.trim()
  if (!name) return
  rows.value.unshift(emptyRow(name))
  newModelName.value = ''
  error.value = ''
}

function removeRow(uid: number) {
  const i = rows.value.findIndex((r) => r.uid === uid)
  if (i >= 0) rows.value.splice(i, 1)
}

function onLocalModelNameChange(row: Row, newName: string | number) {
  const trimmed = String(newName).trim()
  if (
    row.upstreamModelName.trim() === '' &&
    row.modelName.trim() !== '' &&
    trimmed !== row.modelName
  ) {
    row.upstreamModelName = row.modelName
  }
  row.modelName = trimmed
}

function toggleEndpoint(row: Row, path: string) {
  const idx = row.endpoints.indexOf(path)
  if (idx >= 0) row.endpoints.splice(idx, 1)
  else row.endpoints.push(path)
}

type EndpointOption = { path: string; stale: boolean }

// Render every endpoint this row could reference: the provider's currently-bound
// endpoints, plus any endpoint paths the row still has selected but the provider
// no longer binds (stale). Stale options remain checked and selectable so users can
// clear them; once toggled off they leave row.endpoints and disappear from the list.
function rowEndpointPaths(row: Row): EndpointOption[] {
  const available = new Set(availableEndpointPaths.value)
  const out: EndpointOption[] = []
  const seen = new Set<string>()
  for (const p of availableEndpointPaths.value) {
    if (seen.has(p)) continue
    seen.add(p)
    out.push({ path: p, stale: false })
  }
  for (const p of row.endpoints) {
    if (available.has(p) || seen.has(p)) continue
    seen.add(p)
    out.push({ path: p, stale: true })
  }
  return out
}

async function fetchFromUpstream() {
  if (!hasModelsEndpoint.value) {
    error.value = '请先在渠道编辑表单配置模型列表 URL'
    return
  }
  fetching.value = true
  error.value = ''
  fetchSummary.value = null
  pendingDeletions.value = {}
  let data
  try {
    data = await fetchModelsMutation.mutateAsync({
      providerId: props.providerId,
    })
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '拉取模型失败'
    fetching.value = false
    return
  }
  fetching.value = false

  const serverList = (data?.providerModels ?? []) as ProviderModelEntry[]
  const removedHint = (data?.removedModels ?? []) as string[]

  const rowPairs = new Set(rows.value.map((r) => pairKey(r.modelName, r.upstreamModelName)))
  const serverPairs = new Set(serverList.map((e) => pairKey(e.model, e.upstreamModelName ?? '')))

  let added = 0
  for (const entry of serverList) {
    const key = pairKey(entry.model, entry.upstreamModelName ?? '')
    if (!rowPairs.has(key)) {
      rows.value.push(entryToRow(entry))
      rowPairs.add(key)
      added++
    }
  }

  rows.value.sort((a, b) => {
    const cmp = a.modelName.localeCompare(b.modelName)
    if (cmp !== 0) return cmp
    return b.priority - a.priority
  })

  const missing: MissingRow[] = rows.value
    .filter((r) => !serverPairs.has(pairKey(r.modelName, r.upstreamModelName)))
    .map((r) => ({ uid: r.uid, modelName: r.modelName, upstreamModelName: r.upstreamModelName }))

  fetchSummary.value = { added, missing, removedHint }
}

function applyDeletions() {
  const toDelete = new Set(
    Object.entries(pendingDeletions.value)
      .filter(([, v]) => v)
      .map(([k]) => Number(k)),
  )
  if (!toDelete.size) {
    fetchSummary.value = null
    pendingDeletions.value = {}
    return
  }
  rows.value = rows.value.filter((r) => !toDelete.has(r.uid))
  fetchSummary.value = null
  pendingDeletions.value = {}
}

function dismissSummary() {
  fetchSummary.value = null
  pendingDeletions.value = {}
}

const allMissingSelected = computed(() => {
  const m = fetchSummary.value?.missing ?? []
  if (!m.length) return false
  return m.every((r) => pendingDeletions.value[r.uid])
})

function toggleSelectAllMissing(checked: boolean) {
  const m = fetchSummary.value?.missing ?? []
  const next: Record<number, boolean> = {}
  if (checked) for (const r of m) next[r.uid] = true
  pendingDeletions.value = next
}

function removeAllRows() {
  rows.value = []
  fetchSummary.value = null
  pendingDeletions.value = {}
}

async function save() {
  if (!provider.value) return
  saving.value = true
  error.value = ''
  try {
    await saveProviderMutation.mutateAsync({
      id: provider.value.id,
      providerModels: rowsToList(rows.value),
    })
    props.onSave?.()
    emit('close')
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '保存失败'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <SidePanel :title="providerName" kicker="模型" @close="emit('close')">
    <section v-if="loading" class="flex flex-col gap-2">
      <StateText compact>加载中…</StateText>
    </section>

    <template v-else>
      <section class="flex flex-col gap-2">
        <div class="flex items-baseline justify-between">
          <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >从上游拉取</span
          >
        </div>
        <div class="flex items-center gap-2">
          <Button size="sm" :disabled="fetching || !hasModelsEndpoint" @click="fetchFromUpstream">
            <Icon
              :name="fetching ? 'loader' : 'cloud-download'"
              :size="13"
              :class="fetching ? 'animate-spin' : ''"
            />
            <span>{{ fetching ? '拉取中…' : '拉取' }}</span>
          </Button>
          <span v-if="!hasModelsEndpoint" class="text-xs text-ink-faint">
            请先在渠道编辑表单配置模型列表 URL
          </span>
        </div>

        <div
          v-if="fetchSummary"
          class="flex flex-col gap-2 px-2.5 py-2 border border-line rounded-md bg-surface-50"
        >
          <div class="text-xs text-ink">
            新增 {{ fetchSummary.added }} 项<span v-if="fetchSummary.missing.length"
              >，本地有但上游缺失 {{ fetchSummary.missing.length }} 项</span
            >
          </div>
          <div v-if="fetchSummary.missing.length" class="flex flex-col gap-1.5">
            <div class="flex items-center justify-between">
              <div class="text-2xs text-ink-muted">勾选要删除的模型：</div>
              <label
                class="inline-flex items-center gap-1.5 text-2xs text-ink-muted cursor-pointer"
              >
                <input
                  type="checkbox"
                  class="cursor-pointer"
                  :checked="allMissingSelected"
                  @change="toggleSelectAllMissing(($event.target as HTMLInputElement).checked)"
                />
                <span>全选</span>
              </label>
            </div>
            <ul class="list-none m-0 p-0 flex flex-col gap-1 max-h-40 overflow-y-auto">
              <li
                v-for="row in fetchSummary.missing"
                :key="row.uid"
                class="flex items-center gap-2 text-xs"
              >
                <input
                  :id="`del-${row.uid}`"
                  v-model="pendingDeletions[row.uid]"
                  type="checkbox"
                  class="cursor-pointer"
                />
                <label :for="`del-${row.uid}`" class="font-mono text-ink cursor-pointer"
                  >{{ row.modelName
                  }}<span v-if="row.upstreamModelName"> → {{ row.upstreamModelName }}</span></label
                >
              </li>
            </ul>
            <div class="flex gap-2 justify-end">
              <Button variant="ghost" size="sm" @click="dismissSummary">忽略</Button>
              <Button variant="danger" size="sm" @click="applyDeletions">确认删除</Button>
            </div>
          </div>
          <div v-else class="flex justify-end">
            <Button variant="ghost" size="sm" @click="dismissSummary">关闭</Button>
          </div>
        </div>
      </section>

      <section class="flex flex-col gap-2">
        <div class="flex items-baseline justify-between">
          <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]"
            >模型列表</span
          >
          <span class="text-xs text-ink-faint tabular-nums">{{ modelCount }}</span>
        </div>
        <form class="flex gap-2" @submit.prevent="addModel">
          <Input
            v-model="newModelName"
            size="sm"
            class="flex-1 min-w-0"
            placeholder="新增模型名（picotera 内部模型名）"
          />
          <Button type="submit" size="sm" :disabled="!newModelName.trim()">
            <Icon name="plus" :size="13" />
            <span>添加</span>
          </Button>
          <Button
            type="button"
            variant="danger"
            size="sm"
            :disabled="!rows.length"
            @click="removeAllRows"
          >
            <Icon name="trash" :size="13" />
            <span>清空</span>
          </Button>
        </form>
        <StateText v-if="!rows.length" compact>暂无模型，添加或从上游拉取</StateText>
        <ul v-else class="list-none m-0 p-0 flex flex-col gap-2">
          <li
            v-for="row in rows"
            :key="row.uid"
            class="flex flex-col gap-2 px-2.5 py-2 border border-line rounded-md bg-surface-0"
            :class="row.disabled ? 'opacity-55' : ''"
          >
            <div class="flex items-center gap-2">
              <button
                type="button"
                class="flex-1 min-w-0 flex items-center gap-1.5 text-left bg-transparent border-0 cursor-pointer p-0"
                @click="row.expanded = !row.expanded"
              >
                <Icon
                  :name="row.expanded ? 'chevron-down' : 'chevron-down'"
                  :size="12"
                  :class="row.expanded ? '' : '-rotate-90'"
                />
                <span
                  class="font-mono text-sm text-ink overflow-hidden text-ellipsis whitespace-nowrap"
                  >{{ row.modelName }}</span
                >
              </button>
              <TagList v-if="!row.expanded">
                <Tag v-if="row.upstreamModelName" variant="accent"
                  >→ {{ row.upstreamModelName }}</Tag
                >
                <Tag v-if="row.priority" variant="more">P{{ row.priority }}</Tag>
                <Tag v-if="row.endpoints.length" variant="more"
                  >{{ row.endpoints.length }} 端点</Tag
                >
                <Tag v-if="row.disabled" variant="muted">已禁用</Tag>
              </TagList>
              <IconButton
                :title="row.disabled ? '启用此模型' : '禁用此模型'"
                :aria-label="row.disabled ? '启用此模型' : '禁用此模型'"
                @click="row.disabled = !row.disabled"
              >
                <Icon :name="row.disabled ? 'puzzle-off' : 'puzzle'" :size="13" />
              </IconButton>
              <IconButton
                variant="danger"
                title="删除"
                :aria-label="`删除模型 ${row.modelName}`"
                @click="removeRow(row.uid)"
              >
                <Icon name="trash" :size="13" />
              </IconButton>
            </div>
            <div v-if="row.expanded" class="flex flex-col gap-3 pl-4">
              <Field label="本地模型名">
                <Input
                  :model-value="row.modelName"
                  size="sm"
                  @update:model-value="onLocalModelNameChange(row, $event)"
                />
              </Field>
              <Field label="上游模型名（可选）">
                <Input
                  v-model="row.upstreamModelName"
                  size="sm"
                  placeholder="保留为空 = 与本地名一致"
                />
              </Field>
              <Field label="优先级">
                <Input v-model.number="row.priority" type="number" size="sm" />
              </Field>
              <Field label="端点（不勾选 = 全部已绑定端点）" as="div">
                <div
                  v-if="!availableEndpointPaths.length && !row.endpoints.length"
                  class="text-2xs text-ink-faint"
                >
                  渠道尚未绑定任何端点
                </div>
                <ul v-else class="list-none m-0 p-0 flex flex-col gap-1">
                  <li
                    v-for="opt in rowEndpointPaths(row)"
                    :key="opt.path"
                    class="flex items-center gap-2 text-xs"
                  >
                    <input
                      :id="`ep-${row.uid}-${opt.path}`"
                      type="checkbox"
                      class="cursor-pointer"
                      :checked="row.endpoints.includes(opt.path)"
                      @change="toggleEndpoint(row, opt.path)"
                    />
                    <label
                      :for="`ep-${row.uid}-${opt.path}`"
                      class="font-mono cursor-pointer"
                      :class="opt.stale ? 'text-ink-faint line-through' : 'text-ink'"
                      >{{ opt.path }}</label
                    >
                    <span
                      v-if="opt.stale"
                      title="该端点已从此渠道解绑，取消勾选即可移除"
                      class="inline-flex items-center px-1.5 py-0.5 rounded-[5px] font-mono text-2xs leading-[1.2] bg-warn-faint text-warn-ink"
                      >已解绑</span
                    >
                  </li>
                </ul>
              </Field>
              <Field label="标注" as="div">
                <AnnotationsEditor v-model="row.annotations" />
              </Field>
              <Field label="状态" as="div">
                <label class="inline-flex items-center gap-2 text-sm cursor-pointer">
                  <input v-model="row.disabled" type="checkbox" class="cursor-pointer" />
                  <span>禁用此模型（不参与调度）</span>
                </label>
              </Field>
            </div>
          </li>
        </ul>
      </section>
    </template>

    <template v-if="error" #error>{{ error }}</template>

    <template #footer>
      <Button variant="ghost" @click="emit('close')">取消</Button>
      <Button :disabled="saving || loading" @click="save">
        {{ saving ? '保存中…' : '保存' }}
      </Button>
    </template>
  </SidePanel>
</template>

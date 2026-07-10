<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { EndpointView, ProviderEndpointView } from '@/api'
import {
  deleteProviderEndpoint,
  invalidateProviderEndpoints,
  listEndpoints,
  listProviderEndpoints,
  upsertProviderEndpoint,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { SidePanel, Button, IconButton, Input, Select, Field, StateText, Tag, Icon } from '@/ui'

type Resolver = NonNullable<ProviderEndpointView['credentialsResolver']>

const RESOLVER_OPTIONS: ReadonlyArray<{ value: Resolver; label: string }> = [
  { value: 'unknown', label: '继承端点设置' },
  { value: 'followRequest', label: '跟随请求' },
  { value: 'bearerToken', label: 'Bearer Token' },
  { value: 'xApiKey', label: 'X-Api-Key' },
  { value: 'searchKey', label: 'Search Key (?key=)' },
  { value: 'googApiKey', label: 'X-Goog-Api-Key' },
]

const RESOLVER_LABEL = Object.fromEntries(
  RESOLVER_OPTIONS.map((o) => [o.value, o.label]),
) as Record<Resolver, string>

const props = defineProps<{ providerId: number; providerName: string; modelsEndpointUrl?: string }>()
const emit = defineEmits<{ close: [] }>()
const queryClient = useQueryClient()

const error = ref('')
const endpointsQuery = useQuery({
  queryKey: queryKeys.endpoints.all,
  queryFn: listEndpoints,
})
const providerEndpointsQuery = useQuery({
  queryKey: computed(() => queryKeys.providerEndpoints.list({ providerId: props.providerId })),
  queryFn: () => listProviderEndpoints(props.providerId),
})
const endpoints = computed<EndpointView[]>(() => endpointsQuery.data.value ?? [])
const providerEndpoints = computed<ProviderEndpointView[]>(
  () => providerEndpointsQuery.data.value ?? [],
)
const loading = computed(
  () => endpointsQuery.isLoading.value || providerEndpointsQuery.isLoading.value,
)
const form = ref<{ endpointPath: string; upstreamUrl: string; credentialsResolver: Resolver }>({
  endpointPath: '',
  upstreamUrl: '',
  credentialsResolver: 'unknown',
})
const saving = ref(false)

const editingPath = ref<string | null>(null)
const editDraft = ref<{ upstreamUrl: string; credentialsResolver: Resolver }>({
  upstreamUrl: '',
  credentialsResolver: 'unknown',
})

const endpointNameByPath = computed(() => {
  const map = new Map<string, string>()
  for (const e of endpoints.value) map.set(e.path, e.name)
  return map
})

const availableEndpoints = computed(() =>
  endpoints.value.filter(
    (e) =>
      e.endpointType !== 'modelList' &&
      !providerEndpoints.value.some((pe) => pe.endpointPath === e.path),
  ),
)

const endpointPathOptions = computed(() => [
  {
    value: '',
    label: availableEndpoints.value.length ? '选择端点' : '该渠道暂无可绑定端点',
    disabled: true,
  },
  ...availableEndpoints.value.map((e) => ({
    value: e.path,
    label: e.name || e.path,
    hint: e.name ? e.path : undefined,
  })),
])

function guessUpstreamUrl(endpointPath: string) {
  if (!endpointPath) return ''

  // 1. 优先从已有绑定推断
  const shortestMatchedBinding = providerEndpoints.value
    .filter((pe) => pe.upstreamUrl.endsWith(pe.endpointPath))
    .sort((a, b) => a.upstreamUrl.length - b.upstreamUrl.length)[0]

  if (shortestMatchedBinding) {
    const prefix = shortestMatchedBinding.upstreamUrl.slice(
      0,
      shortestMatchedBinding.upstreamUrl.length - shortestMatchedBinding.endpointPath.length,
    )
    return `${prefix}${endpointPath}`
  }

  // 2. fallback：从 provider 的 modelsEndpointUrl 推断
  const modelsUrl = props.modelsEndpointUrl
  if (modelsUrl) {
    if (modelsUrl.endsWith('/v1/models')) {
      return `${modelsUrl.slice(0, -'/v1/models'.length)}${endpointPath}`
    }
    if (modelsUrl.endsWith('/models')) {
      return `${modelsUrl.slice(0, -'/models'.length)}${endpointPath}`
    }
  }

  return endpointPath
}

const upsertMutation = useMutation({
  mutationFn: upsertProviderEndpoint,
  onSuccess: () => invalidateProviderEndpoints(queryClient),
})
const deleteMutation = useMutation({
  mutationFn: deleteProviderEndpoint,
  onSuccess: () => invalidateProviderEndpoints(queryClient),
})

watch(
  () => props.providerId,
  () => {
    form.value.endpointPath = ''
    form.value.upstreamUrl = ''
    form.value.credentialsResolver = 'unknown'
    editingPath.value = null
  },
)

watch(
  () => form.value.endpointPath,
  (path) => {
    form.value.upstreamUrl = guessUpstreamUrl(path)
  },
)

async function addBinding() {
  if (!form.value.endpointPath || !form.value.upstreamUrl) return
  saving.value = true
  error.value = ''
  try {
    await upsertMutation.mutateAsync({
      providerId: props.providerId,
      endpointPath: form.value.endpointPath,
      upstreamUrl: form.value.upstreamUrl,
      credentialsResolver: form.value.credentialsResolver,
    })
    form.value.endpointPath = ''
    form.value.upstreamUrl = ''
    form.value.credentialsResolver = 'unknown'
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '添加绑定失败'
  } finally {
    saving.value = false
  }
}

function startEdit(pe: ProviderEndpointView) {
  editingPath.value = pe.endpointPath
  editDraft.value = {
    upstreamUrl: pe.upstreamUrl,
    credentialsResolver: (pe.credentialsResolver ?? 'unknown') as Resolver,
  }
}

function cancelEdit() {
  editingPath.value = null
}

function isEditDirty(pe: ProviderEndpointView) {
  if (!editDraft.value.upstreamUrl) return false
  const currentResolver = (pe.credentialsResolver ?? 'unknown') as Resolver
  return (
    editDraft.value.upstreamUrl !== pe.upstreamUrl ||
    editDraft.value.credentialsResolver !== currentResolver
  )
}

async function saveEdit(pe: ProviderEndpointView) {
  if (!editDraft.value.upstreamUrl) return
  if (!isEditDirty(pe)) {
    editingPath.value = null
    return
  }
  error.value = ''
  try {
    await upsertMutation.mutateAsync({
      ...pe,
      upstreamUrl: editDraft.value.upstreamUrl,
      credentialsResolver: editDraft.value.credentialsResolver,
    })
    editingPath.value = null
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '更新绑定失败'
  }
}

async function deleteBinding(path: string) {
  error.value = ''
  try {
    await deleteMutation.mutateAsync({ providerId: props.providerId, endpointPath: path })
    if (editingPath.value === path) editingPath.value = null
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '删除绑定失败'
  }
}

function onEditKeydown(e: KeyboardEvent, pe: ProviderEndpointView) {
  if (e.key === 'Enter') {
    e.preventDefault()
    saveEdit(pe)
  } else if (e.key === 'Escape') {
    e.preventDefault()
    cancelEdit()
  }
}
</script>

<template>
  <SidePanel :title="providerName" kicker="端点绑定" @close="emit('close')">
    <section class="flex flex-col gap-2">
      <div class="flex items-baseline justify-between">
        <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]">已绑定</span>
        <span class="text-xs text-ink-faint tabular-nums">{{ providerEndpoints.length }}</span>
      </div>
      <StateText v-if="loading" :dashed="false" compact>加载中…</StateText>
      <StateText v-else-if="!providerEndpoints.length" compact
        >暂无绑定，下方选择端点添加</StateText
      >
      <ul v-else class="list-none m-0 p-0 flex flex-col gap-2">
        <li
          v-for="pe in providerEndpoints"
          :key="pe.endpointPath"
          class="px-2.5 py-2 border border-line rounded-md bg-surface-0"
        >
          <div class="flex items-center gap-2 min-w-0">
            <span
              class="flex-1 min-w-0 text-sm font-semibold text-ink truncate"
              :title="endpointNameByPath.get(pe.endpointPath) ?? pe.endpointPath"
            >
              {{ endpointNameByPath.get(pe.endpointPath) ?? pe.endpointPath }}
            </span>
            <div class="flex items-center gap-1 shrink-0">
              <template v-if="editingPath === pe.endpointPath">
                <IconButton
                  size="sm"
                  title="保存修改"
                  :aria-label="`保存 ${pe.endpointPath} 绑定`"
                  :disabled="!isEditDirty(pe)"
                  @click="saveEdit(pe)"
                >
                  <Icon name="check" :size="13" />
                </IconButton>
                <IconButton
                  size="sm"
                  title="取消编辑"
                  :aria-label="`取消编辑 ${pe.endpointPath} 绑定`"
                  @click="cancelEdit"
                >
                  <Icon name="close" :size="13" />
                </IconButton>
              </template>
              <template v-else>
                <IconButton
                  size="sm"
                  title="编辑绑定"
                  :aria-label="`编辑 ${pe.endpointPath} 绑定`"
                  @click="startEdit(pe)"
                >
                  <Icon name="edit" :size="13" />
                </IconButton>
                <IconButton
                  size="sm"
                  variant="danger"
                  title="删除绑定"
                  :aria-label="`删除 ${pe.endpointPath} 绑定`"
                  @click="deleteBinding(pe.endpointPath)"
                >
                  <Icon name="trash" :size="13" />
                </IconButton>
              </template>
            </div>
          </div>

          <template v-if="editingPath !== pe.endpointPath">
            <div class="font-mono text-2xs text-ink-faint truncate mt-0.5" :title="pe.endpointPath">
              {{ pe.endpointPath }}
            </div>
            <div class="font-mono text-xs text-ink-muted truncate mt-1" :title="pe.upstreamUrl">
              {{ pe.upstreamUrl }}
            </div>
            <div
              v-if="pe.credentialsResolver && pe.credentialsResolver !== 'unknown'"
              class="mt-1.5"
            >
              <Tag variant="muted">{{ RESOLVER_LABEL[pe.credentialsResolver as Resolver] }}</Tag>
            </div>
          </template>

          <template v-else>
            <div class="font-mono text-2xs text-ink-faint truncate mt-0.5" :title="pe.endpointPath">
              {{ pe.endpointPath }}
            </div>
            <div class="flex flex-col gap-2 mt-2">
              <Field label="上游 URL">
                <Input
                  v-model="editDraft.upstreamUrl"
                  size="sm"
                  placeholder="https://api.example.com/v1/…"
                  autofocus
                  @keydown="onEditKeydown($event, pe)"
                />
              </Field>
              <Field label="凭证发送方式">
                <Select
                  v-model="editDraft.credentialsResolver"
                  size="sm"
                  :options="RESOLVER_OPTIONS"
                  @keydown="onEditKeydown($event, pe)"
                />
              </Field>
            </div>
          </template>
        </li>
      </ul>
    </section>

    <section class="flex flex-col gap-2">
      <div class="flex items-baseline justify-between">
        <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]">新增绑定</span>
      </div>
      <form class="flex flex-col gap-2" @submit.prevent="addBinding">
        <Field label="端点">
          <Select
            v-model="form.endpointPath"
            size="sm"
            :options="endpointPathOptions"
            :disabled="!availableEndpoints.length"
          />
        </Field>
        <Field label="上游 URL">
          <Input
            v-model="form.upstreamUrl"
            size="sm"
            placeholder="https://api.example.com/v1/…"
            :disabled="!availableEndpoints.length"
          />
        </Field>
        <Field label="凭证发送方式">
          <Select
            v-model="form.credentialsResolver"
            size="sm"
            :options="RESOLVER_OPTIONS"
            :disabled="!availableEndpoints.length"
          />
        </Field>
        <div class="flex justify-end">
          <Button
            type="submit"
            size="sm"
            :disabled="saving || !form.endpointPath || !form.upstreamUrl"
          >
            {{ saving ? '添加中…' : '添加' }}
          </Button>
        </div>
      </form>
    </section>

    <template v-if="error" #error>{{ error }}</template>
  </SidePanel>
</template>

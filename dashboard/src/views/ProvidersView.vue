<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useConfirm } from '@/composables/useConfirm'
import type { ProviderView } from '@/api'
import {
  deleteProvider,
  invalidateProviderEndpoints,
  invalidateProviders,
  listProviderEndpoints,
  listProviders,
  upsertProvider,
  upsertProviderEndpoint,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import ProviderForm from '@/components/ProviderForm.vue'
import ProviderEndpointsPanel from '@/components/ProviderEndpointsPanel.vue'
import ProviderModelsPanel from '@/components/ProviderModelsPanel.vue'
import { useSidePanel } from '@/composables/useSidePanel'
import {
  Button,
  IconButton,
  DataCard,
  DataTable,
  Th,
  Td,
  Tr,
  StateText,
  Badge,
  Tag,
  TagList,
  Icon,
  MultiColumnFilter,
  type ColumnFilterOption,
} from '@/ui'

const panel = useSidePanel()
const confirm = useConfirm()
const queryClient = useQueryClient()
const error = ref('')
const duplicatingProviderId = ref<number | null>(null)
const selectedModels = ref<string[]>([])
const modelMatchMode = ref<'or' | 'and'>('or')
const modelMatchModeOptions = [
  { value: 'or', label: '或' },
  { value: 'and', label: '与' },
]

const providersQuery = useQuery({
  queryKey: queryKeys.providers.all,
  queryFn: listProviders,
})
const providers = computed(() =>
  [...(providersQuery.data.value ?? [])].sort((a, b) => {
    const priority = b.priority - a.priority
    if (priority !== 0) return priority
    return b.id - a.id
  }),
)
const loading = computed(() => providersQuery.isLoading.value)
const hasModelFilter = computed(() => selectedModels.value.length > 0)
const modelFilterOptions = computed<ColumnFilterOption<string>[]>(() => {
  const names = new Set<string>()
  for (const provider of providers.value) {
    for (const model of modelNames(provider)) names.add(model)
  }
  return Array.from(names)
    .sort((a, b) => a.localeCompare(b))
    .map((name) => ({ value: name, label: name }))
})
const filteredProviders = computed(() => providers.value.filter(providerMatchesModelFilter))
const count = computed(() => filteredProviders.value.length)
const totalCount = computed(() => providers.value.length)

const updateProviderMutation = useMutation({
  mutationFn: upsertProvider,
  onSuccess: () => invalidateProviders(queryClient),
})
const deleteProviderMutation = useMutation({
  mutationFn: deleteProvider,
  onSuccess: () => invalidateProviders(queryClient),
})
const duplicateProviderMutation = useMutation({
  mutationFn: upsertProvider,
  onSuccess: () => invalidateProviders(queryClient),
})

function editKey(id: number) {
  return `provider:${id}:edit`
}
function bindingKey(id: number) {
  return `provider:${id}:bindings`
}
function modelsKey(id: number) {
  return `provider:${id}:models`
}

function modelNames(p: ProviderView): string[] {
  const list = (p.providerModels ?? []) as { model?: string }[]
  return Array.from(new Set(list.map((e) => e.model).filter((m): m is string => !!m)))
}

function providerMatchesModelFilter(p: ProviderView): boolean {
  if (!selectedModels.value.length) return true

  const models = new Set(modelNames(p))
  if (modelMatchMode.value === 'or') {
    return selectedModels.value.some((model) => models.has(model))
  }
  return selectedModels.value.every((model) => models.has(model))
}

function nextDuplicatedProviderName(sourceName: string) {
  const names = new Set(providers.value.map((p) => p.name))
  for (let i = 1; ; i += 1) {
    const candidate = `${sourceName} (${i})`
    if (!names.has(candidate)) return candidate
  }
}

function openCreate() {
  panel.open(ProviderForm, {}, { key: 'provider:new' })
}

function openEdit(p: ProviderView) {
  panel.open(ProviderForm, { provider: p }, { key: editKey(p.id) })
}

function toggleBindings(p: ProviderView) {
  panel.toggle(
    ProviderEndpointsPanel,
    {
      providerId: p.id,
      providerName: p.name,
      modelsEndpointUrl: p.modelsEndpointUrl || undefined,
    },
    { key: bindingKey(p.id) },
  )
}

function toggleModels(p: ProviderView) {
  panel.toggle(
    ProviderModelsPanel,
    { providerId: p.id, providerName: p.name },
    { key: modelsKey(p.id) },
  )
}

async function toggleDisabled(p: ProviderView) {
  await updateProviderMutation.mutateAsync({ ...p, disabled: !p.disabled })
}

async function duplicateProvider(p: ProviderView) {
  error.value = ''
  duplicatingProviderId.value = p.id

  try {
    const created = await duplicateProviderMutation.mutateAsync({
      ...p,
      id: 0,
      name: nextDuplicatedProviderName(p.name),
    })

    try {
      const bindings = await listProviderEndpoints(p.id)
      await Promise.all(
        bindings.map((binding) =>
          upsertProviderEndpoint({
            ...binding,
            providerId: created.id,
          }),
        ),
      )
      await invalidateProviderEndpoints(queryClient)
    } catch (e: unknown) {
      error.value =
        e instanceof Error
          ? `渠道已创建，但端点绑定复制失败：${e.message}`
          : '渠道已创建，但端点绑定复制失败'
    }

    panel.open(ProviderForm, { provider: created }, { key: editKey(created.id) })
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '复制渠道失败'
  } finally {
    duplicatingProviderId.value = null
  }
}

function confirmDelete(_event: Event, p: ProviderView) {
  confirm.require({
    message: `确定要删除渠道「${p.name}」吗？此操作不可撤销。`,
    accept: async () => {
      await deleteProviderMutation.mutateAsync(p.id)
      if (
        panel.isActive(editKey(p.id)) ||
        panel.isActive(bindingKey(p.id)) ||
        panel.isActive(modelsKey(p.id))
      )
        panel.close()
    },
  })
}

function rowSelected(id: number) {
  return (
    panel.isActive(editKey(id)) || panel.isActive(bindingKey(id)) || panel.isActive(modelsKey(id))
  )
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">
        <template v-if="hasModelFilter">{{ count }} / {{ totalCount }} 个渠道</template>
        <template v-else>{{ count }} 个渠道</template>
      </span>
      <div class="flex items-center gap-2">
        <Button @click="openCreate">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>新增渠道</span>
        </Button>
      </div>
    </div>
    <StateText v-if="error" :dashed="false">{{ error }}</StateText>
    <StateText v-if="loading">加载中…</StateText>
    <template v-else-if="providers.length">
      <DataCard>
        <DataTable fixed>
          <colgroup>
            <col style="width: 5rem" />
            <col style="width: 18rem" />
            <col style="width: 6rem" />
            <col />
            <col style="width: 15rem" />
          </colgroup>
          <thead>
            <tr>
              <Th>ID</Th>
              <Th>名称</Th>
              <Th>优先级</Th>
              <Th>
                <div class="flex items-center gap-1.5 min-w-0">
                  <MultiColumnFilter
                    v-model="selectedModels"
                    v-model:match-mode="modelMatchMode"
                    label="模型"
                    :options="modelFilterOptions"
                    :match-mode-options="modelMatchModeOptions"
                    placeholder="过滤模型…"
                  />
                </div>
              </Th>
              <Th actions />
            </tr>
          </thead>
          <tbody>
            <Tr
              v-for="p in filteredProviders"
              :key="p.id"
              :selected="rowSelected(p.id)"
              :dimmed="p.disabled"
            >
              <Td
                ><span class="font-mono text-ink-faint">{{ p.id }}</span></Td
              >
              <Td>
                <div class="flex items-center gap-1.5 min-w-0">
                  <span class="font-medium truncate" :title="p.name">{{ p.name }}</span>
                  <Tag v-if="p.disabled" variant="muted" class="flex-none">已禁用</Tag>
                </div>
              </Td>
              <Td
                ><Badge>{{ p.priority }}</Badge></Td
              >
              <Td>
                <TagList class="min-w-0 overflow-hidden">
                  <Tag v-for="m in modelNames(p).slice(0, 3)" :key="m" variant="accent">{{
                    m
                  }}</Tag>
                  <Tag v-if="modelNames(p).length > 3" variant="more"
                    >+{{ modelNames(p).length - 3 }}</Tag
                  >
                </TagList>
              </Td>
              <Td actions>
                <div
                  class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity"
                >
                  <IconButton
                    :title="p.disabled ? '启用渠道' : '禁用渠道'"
                    :aria-label="p.disabled ? '启用渠道' : '禁用渠道'"
                    @click="toggleDisabled(p)"
                  >
                    <Icon :name="p.disabled ? 'puzzle-off' : 'puzzle'" :size="13" />
                  </IconButton>
                  <IconButton
                    :active="panel.isActive(modelsKey(p.id))"
                    title="模型"
                    aria-label="模型"
                    :aria-pressed="panel.isActive(modelsKey(p.id))"
                    @click="toggleModels(p)"
                  >
                    <Icon name="cpu" :size="13" />
                  </IconButton>
                  <IconButton
                    :active="panel.isActive(bindingKey(p.id))"
                    title="端点绑定"
                    aria-label="端点绑定"
                    :aria-pressed="panel.isActive(bindingKey(p.id))"
                    @click="toggleBindings(p)"
                  >
                    <Icon name="link" :size="13" />
                  </IconButton>
                  <IconButton
                    title="复制渠道"
                    aria-label="复制渠道"
                    :disabled="duplicatingProviderId === p.id"
                    @click="duplicateProvider(p)"
                  >
                    <Icon name="copy" :size="13" />
                  </IconButton>
                  <IconButton
                    :active="panel.isActive(editKey(p.id))"
                    title="编辑"
                    aria-label="编辑"
                    @click="openEdit(p)"
                  >
                    <Icon name="edit" :size="13" />
                  </IconButton>
                  <IconButton
                    variant="danger"
                    title="删除"
                    aria-label="删除"
                    @click="(ev: Event) => confirmDelete(ev, p)"
                  >
                    <Icon name="trash" :size="13" />
                  </IconButton>
                </div>
              </Td>
            </Tr>
          </tbody>
        </DataTable>
      </DataCard>
      <StateText v-if="!filteredProviders.length">暂无匹配渠道</StateText>
    </template>
    <StateText v-else>暂无渠道，点击右上角按钮新增</StateText>
  </div>
</template>

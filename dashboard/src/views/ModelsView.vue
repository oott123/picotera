<script setup lang="ts">
import { ref, computed } from 'vue'
import { useMutation, useQueries, useQueryClient } from '@tanstack/vue-query'
import { useConfirm } from '@/composables/useConfirm'
import type {
  ModelView,
  ProviderView,
  ProviderModelEntry,
  ProviderEndpointView,
  EndpointView,
} from '@/api'
import {
  deleteModel,
  invalidateModels,
  listEndpoints,
  listModels,
  listProviderEndpoints,
  listProviders,
  upsertModel,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import ModelForm from '@/components/ModelForm.vue'
import ModelPricingMatchPanel from '@/components/ModelPricingMatchPanel.vue'
import ModelUpstreamsPanel, { type Upstream } from '@/components/ModelUpstreamsPanel.vue'
import { useSidePanel } from '@/composables/useSidePanel'
import { useSession } from '@/composables/useSession'
import {
  Button,
  IconButton,
  DataCard,
  DataTable,
  Th,
  Td,
  Tr,
  StateText,
  Tag,
  TagList,
  Icon,
  MoneyDisplay,
} from '@/ui'

const panel = useSidePanel()
const session = useSession()
const confirm = useConfirm()
const queryClient = useQueryClient()

const queries = useQueries({
  queries: [
    { queryKey: queryKeys.models.all, queryFn: listModels },
    { queryKey: queryKeys.providers.all, queryFn: listProviders },
    { queryKey: queryKeys.providerEndpoints.list(), queryFn: () => listProviderEndpoints() },
    { queryKey: queryKeys.endpoints.all, queryFn: listEndpoints },
  ],
})
const models = computed(() => (queries.value[0].data ?? []) as ModelView[])
const providers = computed(() => (queries.value[1].data ?? []) as ProviderView[])
const providerEndpoints = computed(() => (queries.value[2].data ?? []) as ProviderEndpointView[])
const endpoints = computed(() => (queries.value[3].data ?? []) as EndpointView[])
const loading = computed(() => queries.value.some((q) => q.isLoading))
const orphanExpanded = ref(false)
const upsertModelMutation = useMutation({
  mutationFn: upsertModel,
  onSuccess: () => invalidateModels(queryClient),
})
const deleteModelMutation = useMutation({
  mutationFn: deleteModel,
  onSuccess: () => invalidateModels(queryClient),
})

const routablePathSet = computed(() => new Set(endpoints.value.map((e) => e.path)))

const endpointNameByPath = computed(() => {
  const map: Record<string, string> = {}
  for (const e of endpoints.value) map[e.path] = e.name
  return map
})

const providerEndpointMap = computed(() => {
  const map: Record<number, string[]> = {}
  for (const pe of providerEndpoints.value) {
    if (!routablePathSet.value.has(pe.endpointPath)) continue
    ;(map[pe.providerId] ??= []).push(pe.endpointPath)
  }
  for (const arr of Object.values(map)) arr.sort()
  return map
})

const upstreamIndex = computed<Record<string, Upstream[]>>(() => {
  const out: Record<string, Upstream[]> = {}
  for (const provider of providers.value) {
    const list = (provider.providerModels ?? []) as ProviderModelEntry[]
    for (const entry of list) {
      const modelName = entry.model
      if (!modelName) continue
      const entryEndpoints = entry.endpoints ?? []
      const expandedFromProvider = !entryEndpoints.length
      const endpointPaths = expandedFromProvider
        ? (providerEndpointMap.value[provider.id] ?? [])
        : entryEndpoints.filter((p) => routablePathSet.value.has(p))
      const upstream: Upstream = {
        providerId: provider.id,
        providerName: provider.name,
        upstreamModelName: entry.upstreamModelName?.trim() || modelName,
        endpointPaths,
        priority: entry.priority ?? 0,
        providerPriority: provider.priority ?? 0,
        expandedFromProvider,
        providerDisabled: provider.disabled ?? false,
        entryDisabled: entry.disabled ?? false,
      }
      ;(out[modelName] ??= []).push(upstream)
    }
  }
  return out
})

const registeredNames = computed(() => new Set(models.value.map((m) => m.name)))

const sortedModels = computed(() =>
  [...models.value].sort((a, b) => {
    if (a.disabled !== b.disabled) return a.disabled ? 1 : -1
    return a.name.localeCompare(b.name)
  }),
)

const orphanRows = computed<{ name: string; providerNames: string[] }[]>(() => {
  const out: { name: string; providerNames: string[] }[] = []
  for (const [name, list] of Object.entries(upstreamIndex.value)) {
    if (registeredNames.value.has(name)) continue
    out.push({ name, providerNames: Array.from(new Set(list.map((u) => u.providerName))) })
  }
  out.sort((a, b) => a.name.localeCompare(b.name))
  return out
})

const count = computed(() => models.value.length)

function openCreate() {
  panel.open(ModelForm, {}, { key: 'model:new' })
}

function openEdit(m: ModelView) {
  panel.open(ModelForm, { model: m }, { key: `model:${m.name}` })
}

function openUpstreams(m: ModelView) {
  const list = upstreamIndex.value[m.name] ?? []
  if (!list.length) return
  panel.open(
    ModelUpstreamsPanel,
    {
      modelName: m.name,
      modelDisabled: m.disabled ?? false,
      upstreams: list,
      endpointNames: endpointNameByPath.value,
    },
    { key: `model-upstreams:${m.name}` },
  )
}

function openPricingMatch(m: ModelView) {
  panel.open(ModelPricingMatchPanel, { model: m }, { key: `model-pricing-match:${m.name}` })
}

async function toggleDisabled(m: ModelView) {
  const body = {
    name: m.name,
    disabled: !m.disabled,
    annotations: m.annotations ?? {},
    ...(m.pricing ? { pricing: m.pricing } : {}),
  }
  await upsertModelMutation.mutateAsync(body)
}

function openCreateFromOrphan(name: string) {
  panel.open(ModelForm, { defaultName: name, lockedName: true }, { key: `model:new:${name}` })
}

function confirmDelete(_event: Event, m: ModelView) {
  confirm.require({
    message: `确定要删除模型「${m.name}」吗？此操作不可撤销。`,
    accept: async () => {
      await deleteModelMutation.mutateAsync(m.name)
    },
  })
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">{{ count }} 个模型</span>
      <div class="flex items-center gap-2">
        <Button v-if="session.isAdmin.value" @click="openCreate">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>新增模型</span>
        </Button>
      </div>
    </div>
    <StateText v-if="loading">加载中…</StateText>
    <template v-else>
      <DataCard v-if="models.length">
        <DataTable>
          <thead>
            <tr>
              <Th>名称</Th>
              <Th>价格</Th>
              <Th>上游</Th>
              <Th actions />
            </tr>
          </thead>
          <tbody>
            <Tr
              v-for="m in sortedModels"
              :key="m.name"
              :selected="panel.isActive(`model:${m.name}`)"
              :class="m.disabled ? 'opacity-55' : ''"
            >
              <Td>
                <span class="font-mono font-medium">{{ m.name }}</span>
                <span v-if="m.disabled" class="text-ink-faint ml-1.5">（已禁用）</span>
              </Td>
              <Td>
                <template v-if="!m.pricing || !m.pricing.tiers || m.pricing.tiers.length === 0">
                  <div v-if="session.isAdmin.value" class="inline-flex items-center gap-2">
                    <Button type="button" variant="ghost" size="sm" @click="openPricingMatch(m)">
                      <Icon name="cloud-dollar" :size="13" />
                      <span>匹配价格</span>
                    </Button>
                  </div>
                </template>
                <template v-else>
                  <span class="inline-flex items-baseline gap-1.5 text-xs">
                    <MoneyDisplay
                      :amount="m.pricing.tiers[0]?.input ?? null"
                      :currency="m.pricing.currency"
                      :max-digits="2"
                    />
                    <span class="text-ink-faint">/</span>
                    <MoneyDisplay
                      :amount="m.pricing.tiers[0]?.output ?? null"
                      :currency="m.pricing.currency"
                      :max-digits="2"
                    />
                    <Tag v-if="m.pricing.tiers.length > 1" variant="accent"
                      >+{{ m.pricing.tiers.length - 1 }}</Tag
                    >
                  </span>
                </template>
              </Td>
              <Td>
                <span
                  v-if="(upstreamIndex[m.name]?.length ?? 0) > 0"
                  class="font-medium tabular-nums text-ink"
                  >{{ upstreamIndex[m.name]!.length }}</span
                >
                <span v-else class="text-ink-faint">—</span>
              </Td>
              <Td actions>
                <div
                  class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity"
                >
                  <template v-if="session.isAdmin.value">
                    <IconButton
                      :title="m.disabled ? '启用模型' : '禁用模型'"
                      :aria-label="m.disabled ? '启用模型' : '禁用模型'"
                      @click="toggleDisabled(m)"
                    >
                      <Icon :name="m.disabled ? 'puzzle-off' : 'puzzle'" :size="13" />
                    </IconButton>
                  </template>
                  <IconButton
                    :active="panel.isActive(`model-upstreams:${m.name}`)"
                    :disabled="(upstreamIndex[m.name]?.length ?? 0) === 0"
                    :title="(upstreamIndex[m.name]?.length ?? 0) === 0 ? '无上游' : '查看上游'"
                    aria-label="查看上游"
                    @click="openUpstreams(m)"
                  >
                    <Icon name="cloud-upload" :size="13" />
                  </IconButton>
                  <IconButton
                    v-if="session.isAdmin.value"
                    :active="panel.isActive(`model-pricing-match:${m.name}`)"
                    title="匹配价格"
                    aria-label="匹配价格"
                    @click="openPricingMatch(m)"
                  >
                    <Icon name="cloud-dollar" :size="13" />
                  </IconButton>
                  <template v-if="session.isAdmin.value">
                    <IconButton
                      :active="panel.isActive(`model:${m.name}`)"
                      title="编辑"
                      aria-label="编辑"
                      @click="openEdit(m)"
                    >
                      <Icon name="edit" :size="13" />
                    </IconButton>
                    <IconButton
                      variant="danger"
                      title="删除"
                      aria-label="删除"
                      @click="(e: Event) => confirmDelete(e, m)"
                    >
                      <Icon name="trash" :size="13" />
                    </IconButton>
                  </template>
                </div>
              </Td>
            </Tr>
          </tbody>
        </DataTable>
      </DataCard>
      <StateText v-else>暂无模型，点击右上角按钮新增</StateText>

      <section v-if="orphanRows.length" class="flex flex-col gap-2">
        <button
          type="button"
          class="flex items-center gap-1.5 text-left bg-transparent border-0 cursor-pointer p-0 text-xs font-medium text-ink-muted uppercase tracking-[0.03em]"
          @click="orphanExpanded = !orphanExpanded"
        >
          <Icon name="chevron-down" :size="12" :class="orphanExpanded ? '' : '-rotate-90'" />
          <span>未注册上游模型 ({{ orphanRows.length }})</span>
        </button>
        <DataCard v-if="orphanExpanded">
          <ul class="list-none m-0 p-0 flex flex-col">
            <li
              v-for="row in orphanRows"
              :key="row.name"
              class="group flex items-center gap-2 px-3 py-2 border-b border-line last:border-b-0"
            >
              <span class="font-mono text-sm text-ink flex-none">{{ row.name }}</span>
              <TagList class="flex-1 min-w-0">
                <Tag v-for="p in row.providerNames" :key="p">{{ p }}</Tag>
              </TagList>
              <IconButton
                v-if="session.isAdmin.value"
                :active="panel.isActive(`model:new:${row.name}`)"
                title="添加为模型"
                :aria-label="`添加 ${row.name} 为模型`"
                class="opacity-55 group-hover:opacity-100 transition-opacity"
                @click="openCreateFromOrphan(row.name)"
              >
                <Icon name="plus" :size="13" />
              </IconButton>
            </li>
          </ul>
        </DataCard>
      </section>
    </template>
  </div>
</template>

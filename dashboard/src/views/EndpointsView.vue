<script setup lang="ts">
import { computed } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useConfirm } from '@/composables/useConfirm'
import type { EndpointView } from '@/api'
import { ENDPOINT_TYPE_LABELS, ENDPOINT_TYPES_MODEL_ROUTED } from '@/api'
import type { EndpointType } from '@/api'
import { deleteEndpoint, invalidateEndpoints, listEndpoints } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import EndpointForm from '@/components/EndpointForm.vue'
import { useSidePanel } from '@/composables/useSidePanel'
import { useSession } from '@/composables/useSession'
import { Button, IconButton, DataCard, DataTable, Th, Td, Tr, StateText, Tag, Icon } from '@/ui'

const panel = useSidePanel()
const session = useSession()
const confirm = useConfirm()
const queryClient = useQueryClient()

const endpointsQuery = useQuery({
  queryKey: queryKeys.endpoints.all,
  queryFn: listEndpoints,
})
const endpoints = computed(() => endpointsQuery.data.value ?? [])
const loading = computed(() => endpointsQuery.isLoading.value)
const count = computed(() => endpoints.value.length)
const deleteEndpointMutation = useMutation({
  mutationFn: deleteEndpoint,
  onSuccess: () => invalidateEndpoints(queryClient),
})

function openCreate() {
  panel.open(EndpointForm, {}, { key: 'endpoint:new' })
}

function openEdit(ep: EndpointView) {
  panel.open(EndpointForm, { endpoint: ep }, { key: `endpoint:${ep.path}` })
}

function endpointTypeVariant(t: EndpointType): 'accent' | 'muted' | 'more' {
  if (ENDPOINT_TYPES_MODEL_ROUTED.includes(t)) return 'accent'
  if (t === 'general') return 'muted'
  if (t === 'exaSearch') return 'more'
  return 'more'
}

function confirmDeleteEndpoint(_event: Event, path: string) {
  confirm.require({
    message: `确定要删除端点「${path}」吗？此操作不可撤销。`,
    accept: async () => {
      await deleteEndpointMutation.mutateAsync(path)
    },
  })
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">{{ count }} 个端点</span>
      <div class="flex items-center gap-2">
        <Button v-if="session.isAdmin.value" @click="openCreate">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>新增端点</span>
        </Button>
      </div>
    </div>
    <StateText v-if="loading">加载中…</StateText>
    <DataCard v-else-if="endpoints.length">
      <DataTable>
        <thead>
          <tr>
            <Th>路径</Th>
            <Th>名称</Th>
            <Th>类型</Th>
            <Th>模型字段</Th>
            <Th>凭证解析</Th>
            <Th actions />
          </tr>
        </thead>
        <tbody>
          <Tr v-for="e in endpoints" :key="e.path" :selected="panel.isActive(`endpoint:${e.path}`)">
            <Td
              ><span class="font-mono font-medium">{{ e.path }}</span></Td
            >
            <Td>{{ e.name }}</Td>
            <Td>
              <Tag :variant="endpointTypeVariant(e.endpointType)">{{
                ENDPOINT_TYPE_LABELS[e.endpointType]
              }}</Tag>
            </Td>
            <Td
              ><span class="font-mono text-ink-faint">{{ e.modelPath || '—' }}</span></Td
            >
            <Td>
              <Tag :variant="e.credentialsResolver === 'generalApiKey' ? 'ok' : 'muted'">
                {{ e.credentialsResolver }}
              </Tag>
            </Td>
            <Td actions>
              <div
                v-if="session.isAdmin.value"
                class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity"
              >
                <IconButton
                  :active="panel.isActive(`endpoint:${e.path}`)"
                  title="编辑"
                  aria-label="编辑"
                  @click="openEdit(e)"
                >
                  <Icon name="edit" :size="13" />
                </IconButton>
                <IconButton
                  variant="danger"
                  title="删除"
                  aria-label="删除"
                  @click="(ev: Event) => confirmDeleteEndpoint(ev, e.path)"
                >
                  <Icon name="trash" :size="13" />
                </IconButton>
              </div>
            </Td>
          </Tr>
        </tbody>
      </DataTable>
    </DataCard>
    <StateText v-else>暂无端点，点击右上角按钮新增</StateText>
  </div>
</template>

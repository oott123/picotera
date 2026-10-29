<script setup lang="ts">
import { ref, computed } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useConfirm } from '@/composables/useConfirm'
import { useSidePanel } from '@/composables/useSidePanel'
import type { ApiKeyView } from '@/api'
import { deleteApiKey, invalidateApiKeys, listApiKeys, updateApiKey } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import ApiKeyForm from '@/components/ApiKeyForm.vue'
import { Button, IconButton, DataCard, DataTable, Th, Td, Tr, StateText, Tag, Icon } from '@/ui'

const panel = useSidePanel()
const confirm = useConfirm()
const queryClient = useQueryClient()

const apiKeysQuery = useQuery({
  queryKey: queryKeys.apiKeys.all,
  queryFn: listApiKeys,
})
const apiKeys = computed(() => apiKeysQuery.data.value ?? [])
const loading = computed(() => apiKeysQuery.isLoading.value)
const count = computed(() => apiKeys.value.length)
const copiedId = ref<number | null>(null)
let copyTimer: ReturnType<typeof setTimeout> | null = null
const deleteApiKeyMutation = useMutation({
  mutationFn: deleteApiKey,
  onSuccess: () => invalidateApiKeys(queryClient),
})
const updateApiKeyMutation = useMutation({
  mutationFn: (k: ApiKeyView) =>
    updateApiKey(k.id, {
      name: k.name,
      key: k.key,
      disabled: !k.disabled,
      annotations: k.annotations ?? {},
    }),
  onSuccess: () => invalidateApiKeys(queryClient),
})

function openCreate() {
  panel.open(ApiKeyForm, {}, { key: 'apiKey:new', width: '560px' })
}

function openEdit(k: ApiKeyView) {
  panel.open(ApiKeyForm, { apiKey: k }, { key: `apiKey:${k.id}`, width: '560px' })
}

function confirmDelete(_event: Event, k: ApiKeyView) {
  confirm.require({
    message: `确定要删除密钥「${k.name || k.id}」吗？此操作不可撤销。`,
    accept: async () => {
      await deleteApiKeyMutation.mutateAsync(k.id)
    },
  })
}

async function toggle(k: ApiKeyView) {
  await updateApiKeyMutation.mutateAsync(k)
}

async function copyKey(k: ApiKeyView) {
  try {
    await navigator.clipboard.writeText(k.key)
    copiedId.value = k.id
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => {
      copiedId.value = null
    }, 1500)
  } catch {
    // clipboard unavailable — silently ignore
  }
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">{{ count }} 个密钥</span>
      <div class="flex items-center gap-2">
        <Button @click="openCreate">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>新增密钥</span>
        </Button>
      </div>
    </div>
    <StateText v-if="loading">加载中…</StateText>
    <DataCard v-else-if="apiKeys.length">
      <DataTable>
        <thead>
          <tr>
            <Th>名称</Th>
            <Th>Key</Th>
            <Th actions />
          </tr>
        </thead>
        <tbody>
          <Tr
            v-for="k in apiKeys"
            :key="k.id"
            :selected="panel.isActive(`apiKey:${k.id}`)"
            :dimmed="k.disabled"
          >
            <Td>
              <span class="font-medium">{{ k.name }}</span>
              <Tag v-if="k.disabled" variant="muted" class="ml-1.5">已禁用</Tag>
            </Td>
            <Td>
              <div class="inline-flex items-center gap-1.5">
                <code class="font-mono text-2xs text-ink-muted break-all">{{ k.key }}</code>
                <IconButton
                  :title="copiedId === k.id ? '已复制' : '复制 Key'"
                  :aria-label="copiedId === k.id ? '已复制' : '复制 Key'"
                  @click="copyKey(k)"
                >
                  <Icon :name="copiedId === k.id ? 'check' : 'copy'" :size="13" />
                </IconButton>
              </div>
            </Td>
            <Td actions>
              <div class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity">
                <IconButton
                  :title="k.disabled ? '启用' : '禁用'"
                  :aria-label="k.disabled ? '启用' : '禁用'"
                  @click="toggle(k)"
                >
                  <Icon :name="k.disabled ? 'puzzle-off' : 'puzzle'" :size="13" />
                </IconButton>
                <IconButton
                  :active="panel.isActive(`apiKey:${k.id}`)"
                  title="编辑"
                  aria-label="编辑"
                  @click="openEdit(k)"
                >
                  <Icon name="edit" :size="13" />
                </IconButton>
                <IconButton
                  variant="danger"
                  title="删除"
                  aria-label="删除"
                  @click="(ev: Event) => confirmDelete(ev, k)"
                >
                  <Icon name="trash" :size="13" />
                </IconButton>
              </div>
            </Td>
          </Tr>
        </tbody>
      </DataTable>
    </DataCard>
    <StateText v-else>暂无密钥，点击右上角按钮新增</StateText>
  </div>
</template>

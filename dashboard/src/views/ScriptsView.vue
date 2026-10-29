<script setup lang="ts">
import { computed } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useConfirm } from '@/composables/useConfirm'
import { useSidePanel } from '@/composables/useSidePanel'
import type { ScriptView } from '@/api'
import { deleteScript, invalidateScripts, listScripts, updateScript } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import ScriptForm from '@/components/ScriptForm.vue'
import { Button, IconButton, DataCard, DataTable, Th, Td, Tr, StateText, Tag, Icon } from '@/ui'

const panel = useSidePanel()
const confirm = useConfirm()
const queryClient = useQueryClient()

const scriptsQuery = useQuery({
  queryKey: queryKeys.scripts.all,
  queryFn: listScripts,
})
const scripts = computed(() => scriptsQuery.data.value ?? [])
const loading = computed(() => scriptsQuery.isLoading.value)
const count = computed(() => scripts.value.length)
const deleteScriptMutation = useMutation({
  mutationFn: deleteScript,
  onSuccess: () => invalidateScripts(queryClient),
})
const updateScriptMutation = useMutation({
  mutationFn: (script: ScriptView) =>
    updateScript(script.id, {
      id: script.id,
      name: script.name,
      source: script.source,
      enabled: !script.enabled,
    }),
  onSuccess: () => invalidateScripts(queryClient),
})

function openCreate() {
  panel.open(ScriptForm, {}, { key: 'script:new', width: '600px' })
}

function openEdit(s: ScriptView) {
  panel.open(ScriptForm, { script: s }, { key: `script:${s.id}`, width: '600px' })
}

function confirmDelete(_event: Event, id: string) {
  confirm.require({
    message: `确定要删除脚本「${id}」吗？此操作不可撤销。`,
    accept: async () => {
      await deleteScriptMutation.mutateAsync(id)
    },
  })
}

async function toggle(s: ScriptView) {
  await updateScriptMutation.mutateAsync(s)
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">{{ count }} 个脚本</span>
      <div class="flex items-center gap-2">
        <Button @click="openCreate">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>新增脚本</span>
        </Button>
      </div>
    </div>
    <StateText v-if="loading">加载中…</StateText>
    <DataCard v-else-if="scripts.length">
      <DataTable>
        <thead>
          <tr>
            <Th>名称</Th>
            <Th actions />
          </tr>
        </thead>
        <tbody>
          <Tr
            v-for="s in scripts"
            :key="s.id"
            :selected="panel.isActive(`script:${s.id}`)"
            :dimmed="!s.enabled"
          >
            <Td>
              <span class="font-medium">{{ s.name }}</span>
              <Tag v-if="!s.enabled" variant="muted" class="ml-1.5">已禁用</Tag>
              <span class="block font-mono text-2xs text-ink-faint">{{ s.id }}</span>
            </Td>
            <Td actions>
              <div class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity">
                <IconButton
                  :title="s.enabled ? '禁用脚本' : '启用脚本'"
                  :aria-label="s.enabled ? '禁用脚本' : '启用脚本'"
                  @click="toggle(s)"
                >
                  <Icon :name="s.enabled ? 'puzzle' : 'puzzle-off'" :size="13" />
                </IconButton>
                <IconButton
                  :active="panel.isActive(`script:${s.id}`)"
                  title="编辑"
                  aria-label="编辑"
                  @click="openEdit(s)"
                >
                  <Icon name="edit" :size="13" />
                </IconButton>
                <IconButton
                  variant="danger"
                  title="删除"
                  aria-label="删除"
                  @click="(ev: Event) => confirmDelete(ev, s.id)"
                >
                  <Icon name="trash" :size="13" />
                </IconButton>
              </div>
            </Td>
          </Tr>
        </tbody>
      </DataTable>
    </DataCard>
    <StateText v-else>暂无脚本，点击右上角按钮新增</StateText>
  </div>
</template>

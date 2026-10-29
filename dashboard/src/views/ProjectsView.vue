<script setup lang="ts">
import { computed } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useConfirm } from '@/composables/useConfirm'
import { useSidePanel } from '@/composables/useSidePanel'
import type { ProjectView } from '@/api'
import { deleteProject, invalidateProjects, listProjects } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import ProjectForm from '@/components/ProjectForm.vue'
import MergeProjectForm from '@/components/MergeProjectForm.vue'
import { Button, IconButton, DataCard, DataTable, Th, Td, Tr, StateText, Tag, Icon } from '@/ui'
import { formatDuration } from '@/utils/duration'

const panel = useSidePanel()
const confirm = useConfirm()
const queryClient = useQueryClient()

const projectsQuery = useQuery({
  queryKey: queryKeys.projects.all,
  queryFn: listProjects,
})
const projects = computed(() => projectsQuery.data.value ?? [])
const loading = computed(() => projectsQuery.isLoading.value)
const count = computed(() => projects.value.length)

const deleteProjectMutation = useMutation({
  mutationFn: deleteProject,
  onSuccess: () => invalidateProjects(queryClient),
})

function openCreate() {
  panel.open(ProjectForm, {}, { key: 'project:new' })
}

function openEdit(p: ProjectView) {
  panel.open(ProjectForm, { project: p }, { key: `project:${p.id}` })
}

function openMerge(p: ProjectView) {
  panel.open(MergeProjectForm, { source: p }, { key: `project:merge:${p.id}`, width: '380px' })
}

function confirmDelete(_event: Event, p: ProjectView) {
  confirm.require({
    message: `确定要删除项目「${p.name || p.id}」吗？此操作不可撤销。`,
    accept: async () => {
      await deleteProjectMutation.mutateAsync(p.id)
    },
  })
}

function fmtTimestamp(ts?: string): string {
  if (!ts) return '—'
  const d = new Date(ts)
  if (isNaN(d.getTime())) return '—'
  return d.toLocaleString()
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">{{ count }} 个项目</span>
      <div class="flex items-center gap-2">
        <Button @click="openCreate">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>新增项目</span>
        </Button>
      </div>
    </div>
    <StateText v-if="loading">加载中…</StateText>
    <DataCard v-else-if="projects.length">
      <DataTable>
        <thead>
          <tr>
            <Th>名称</Th>
            <Th>路径数</Th>
            <Th>首次出现</Th>
            <Th>最近出现</Th>
            <Th>持续时间</Th>
            <Th actions />
          </tr>
        </thead>
        <tbody>
          <Tr v-for="p in projects" :key="p.id" :selected="panel.isActive(`project:${p.id}`)">
            <Td>
              <span class="font-medium">{{ p.name }}</span>
              <Tag v-if="p.autoCreated" variant="muted" class="ml-1.5">自动</Tag>
              <Tag v-if="!(p.paths?.length ?? 0)" variant="muted" class="ml-1.5">无路径</Tag>
            </Td>
            <Td>
              <span class="tabular-nums text-ink-muted">{{ p.paths?.length ?? 0 }}</span>
            </Td>
            <Td>
              <span class="text-2xs text-ink-muted tabular-nums">{{
                fmtTimestamp(p.firstSeenAt)
              }}</span>
            </Td>
            <Td>
              <span class="text-2xs text-ink-muted tabular-nums">{{
                fmtTimestamp(p.lastSeenAt)
              }}</span>
            </Td>
            <Td>
              <span class="text-2xs text-ink-muted tabular-nums">{{
                formatDuration(p.firstSeenAt, p.lastSeenAt)
              }}</span>
            </Td>
            <Td actions>
              <div class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity">
                <IconButton
                  :active="panel.isActive(`project:${p.id}`)"
                  title="编辑"
                  aria-label="编辑"
                  @click="openEdit(p)"
                >
                  <Icon name="edit" :size="13" />
                </IconButton>
                <IconButton
                  :active="panel.isActive(`project:merge:${p.id}`)"
                  title="合并"
                  aria-label="合并"
                  @click="openMerge(p)"
                >
                  <Icon name="git-merge" :size="13" />
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
    <StateText v-else>暂无项目，点击右上角按钮新增</StateText>
  </div>
</template>

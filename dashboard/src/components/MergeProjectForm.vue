<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { SidePanel, Button, Field, Select, StateText } from '@/ui'
import type { ProjectView } from '@/api'
import { invalidateProjects, listProjects, mergeProject } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'

const emit = defineEmits<{ close: [] }>()
const props = defineProps<{ source: ProjectView; onSave?: () => void }>()
const queryClient = useQueryClient()

const projectsQuery = useQuery({
  queryKey: queryKeys.projects.all,
  queryFn: listProjects,
})
const candidates = computed(() =>
  (projectsQuery.data.value ?? []).filter((p) => p.id !== props.source.id),
)

const targetOptions = computed(() => [
  { value: 0, label: '请选择目标项目', disabled: true },
  ...candidates.value.map((c) => ({ value: c.id, label: c.name })),
])

const targetId = ref(0)
const saving = ref(false)
const error = ref('')

const mergeProjectMutation = useMutation({
  mutationFn: (id: number) => mergeProject(props.source.id, id),
  onSuccess: () => {
    invalidateProjects(queryClient)
    props.onSave?.()
    emit('close')
  },
})

async function submit() {
  saving.value = true
  error.value = ''
  try {
    await mergeProjectMutation.mutateAsync(targetId.value)
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '合并失败'
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <SidePanel
    :title="`${source.name || source.id}`"
    kicker="合并项目"
    :subtitle="`所有请求和追踪将合并到目标项目，源项目将被删除。`"
    @close="emit('close')"
  >
    <StateText v-if="projectsQuery.isLoading.value">加载中…</StateText>
    <StateText v-else-if="candidates.length === 0">没有其他项目可合并</StateText>
    <form v-else id="merge-project-form" class="flex flex-col gap-4" @submit.prevent="submit">
      <Field label="目标项目">
        <Select v-model="targetId" required class="w-full" :options="targetOptions" />
      </Field>
    </form>

    <template v-if="error" #error>{{ error }}</template>

    <template #footer>
      <Button variant="ghost" @click="emit('close')">取消</Button>
      <Button type="submit" form="merge-project-form" :disabled="saving || targetId === 0">
        {{ saving ? '合并中…' : '合并' }}
      </Button>
    </template>
  </SidePanel>
</template>

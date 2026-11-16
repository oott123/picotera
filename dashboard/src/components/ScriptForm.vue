<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { SidePanel, Button, Input, Field, CodeEditor, Tabs, StateText } from '@/ui'
import type { ScriptView } from '@/api'
import { createScript, invalidateScripts, updateScript } from '@/api/client'
import { useExtensionsStore } from '@/stores/extensions'

const emit = defineEmits<{ close: [] }>()
const props = defineProps<{ script?: ScriptView; onSave?: () => void }>()
const queryClient = useQueryClient()

const isEdit = !!props.script
const form = ref({
  id: props.script?.id ?? '',
  name: props.script?.name ?? '',
  source: props.script?.source ?? '',
  frontend: props.script?.frontend ?? '',
  enabled: props.script?.enabled ?? true,
})
const saving = ref(false)
const error = ref('')
const sourceTab = ref<'source' | 'frontend'>('source')
const sourceTabs = [
  { value: 'source', label: '服务端' },
  { value: 'frontend', label: '前端' },
]

const extensions = useExtensionsStore()
const frontendLoadError = computed(() => {
  if (!props.script) return ''
  const entry = extensions.entries[props.script.id]
  return entry?.status === 'error' ? entry.error : ''
})

const saveMutation = useMutation({
  mutationFn: (body: {
    id: string
    name: string
    source: string
    frontend: string
    enabled: boolean
  }) => (isEdit ? updateScript(props.script!.id, body) : createScript(body)),
  onSuccess: () => invalidateScripts(queryClient),
})

async function submit() {
  saving.value = true
  error.value = ''
  try {
    await saveMutation.mutateAsync({
      id: form.value.id,
      name: form.value.name,
      source: form.value.source,
      frontend: form.value.frontend,
      enabled: form.value.enabled,
    })
    props.onSave?.()
    emit('close')
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '操作失败'
  }
  saving.value = false
}
</script>

<template>
  <SidePanel
    :title="isEdit ? form.name || '脚本' : '新增脚本'"
    :kicker="isEdit ? '编辑脚本' : '脚本'"
    @close="emit('close')"
  >
    <form id="script-form" class="flex flex-col gap-4" @submit.prevent="submit">
      <Field label="ID">
        <Input v-model="form.id" :placeholder="isEdit ? '' : '留空自动生成'" />
      </Field>
      <Field label="名称">
        <Input v-model="form.name" required placeholder="例如 reverse-providers" />
      </Field>
      <Field label="启用">
        <label class="inline-flex items-center gap-2">
          <input v-model="form.enabled" type="checkbox" class="rounded border-line" />
          <span class="text-sm text-ink-muted">enabled</span>
        </label>
      </Field>
      <Field label="源代码" as="div">
        <div class="flex flex-col gap-2">
          <Tabs
            :model-value="sourceTab"
            :tabs="sourceTabs"
            @update:model-value="(v) => (sourceTab = v as 'source' | 'frontend')"
          />
          <StateText v-if="frontendLoadError" :dashed="false" compact>
            前端加载失败：{{ frontendLoadError }}
          </StateText>
          <CodeEditor
            v-if="sourceTab === 'source'"
            v-model="form.source"
            language="javascript"
            min-height="400px"
            max-height="70vh"
          />
          <CodeEditor
            v-else
            v-model="form.frontend"
            language="javascript"
            min-height="400px"
            max-height="70vh"
          />
        </div>
      </Field>
    </form>

    <template v-if="error" #error>{{ error }}</template>

    <template #footer>
      <Button variant="ghost" @click="emit('close')">取消</Button>
      <Button type="submit" form="script-form" :disabled="saving">
        {{ saving ? '保存中…' : isEdit ? '更新' : '创建' }}
      </Button>
    </template>
  </SidePanel>
</template>

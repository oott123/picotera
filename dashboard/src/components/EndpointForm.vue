<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { SidePanel, Button, Input, Select, Field } from '@/ui'
import type { EndpointView } from '@/api'
import { ENDPOINT_TYPE_LABELS } from '@/api'
import type { EndpointType } from '@/api'
import { invalidateEndpoints, upsertEndpoint } from '@/api/client'

const emit = defineEmits<{ close: [] }>()
const props = defineProps<{ endpoint?: EndpointView; onSave?: () => void }>()
const queryClient = useQueryClient()

const isEdit = !!props.endpoint
const form = ref({
  name: props.endpoint?.name ?? '',
  path: props.endpoint?.path ?? '',
  modelPath: props.endpoint?.modelPath ?? '',
  credentialsResolver: props.endpoint?.credentialsResolver ?? ('followRequest' as const),
  endpointType: (props.endpoint?.endpointType ?? 'general') as EndpointType,
  prefixMatch: props.endpoint?.prefixMatch ?? false,
})
const saving = ref(false)
const error = ref('')
const saveMutation = useMutation({
  mutationFn: upsertEndpoint,
  onSuccess: () => invalidateEndpoints(queryClient),
})

const isModelPathLocked = computed(
  () => form.value.endpointType === 'exaSearch' || form.value.endpointType === 'modelList',
)
// A codex endpoint stands for one Codex upstream's base_url; its sub-paths only
// exist as request path suffixes, so prefix matching is mandatory (the server
// rejects the pair otherwise).
const isPrefixMatchLocked = computed(() => form.value.endpointType === 'codex')
watch(
  () => form.value.endpointType,
  (t) => {
    if (t === 'exaSearch' || t === 'modelList') form.value.modelPath = ''
    if (t === 'codex') form.value.prefixMatch = true
  },
)

const endpointTypeOptions = computed(() => {
  const entries = Object.entries(ENDPOINT_TYPE_LABELS).filter(([k]) => k !== 'unknown') as [
    EndpointType,
    string,
  ][]
  if (form.value.endpointType === 'unknown') entries.push(['unknown', ENDPOINT_TYPE_LABELS.unknown])
  return entries.map(([value, label]) => ({ value, label }))
})

const credentialsResolverOptions = [
  { value: 'followRequest', label: '跟随请求' },
  { value: 'bearerToken', label: 'Bearer Token' },
  { value: 'xApiKey', label: 'X-Api-Key' },
  { value: 'searchKey', label: 'Search Key (?key=)' },
  { value: 'googApiKey', label: 'X-Goog-Api-Key' },
]

async function submit() {
  saving.value = true
  error.value = ''
  try {
    await saveMutation.mutateAsync(form.value)
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
    :title="isEdit ? form.name || form.path || '端点' : '新增端点'"
    :kicker="isEdit ? '编辑端点' : '端点'"
    @close="emit('close')"
  >
    <form id="endpoint-form" class="flex flex-col gap-4" @submit.prevent="submit">
      <Field label="路径">
        <Input
          v-model="form.path"
          required
          :placeholder="form.prefixMatch ? '例如 /api/codex' : '例如 /api/v1/chat/completions'"
          :disabled="isEdit"
        />
      </Field>
      <Field label="名称">
        <Input v-model="form.name" required placeholder="例如 Chat Completions" />
      </Field>
      <Field label="类型">
        <Select v-model="form.endpointType" :options="endpointTypeOptions" />
      </Field>
      <Field label="路径匹配" as="div">
        <label class="inline-flex items-center gap-2 text-sm cursor-pointer">
          <input
            v-model="form.prefixMatch"
            type="checkbox"
            class="cursor-pointer"
            :disabled="isPrefixMatchLocked"
          />
          <span>前缀匹配</span>
        </label>
        <p v-if="isPrefixMatchLocked" class="text-xs text-ink-faint">
          Codex 端点必须使用前缀匹配。
        </p>
        <p v-else-if="form.prefixMatch" class="text-xs text-ink-faint">
          请求路径中前缀之后的部分会原样接到上游 URL 后面；路径不能包含 {} 变量或以 / 结尾。
        </p>
      </Field>
      <Field label="模型字段路径">
        <Input
          v-model="form.modelPath"
          :disabled="isModelPathLocked"
          :placeholder="
            form.endpointType === 'modelList'
              ? '模型列表端点不解析模型'
              : form.endpointType === 'exaSearch'
                ? 'Exa 搜索端点不解析模型'
                : '可选，留空表示该端点不解析模型'
          "
        />
        <p v-if="form.prefixMatch && form.modelPath" class="text-xs text-ink-faint">
          对前缀匹配端点而言，如果模型字段在请求中不存在，将视为不解析模型转发。
        </p>
      </Field>
      <Field label="凭证发送">
        <Select v-model="form.credentialsResolver" :options="credentialsResolverOptions" />
      </Field>
    </form>

    <template v-if="error" #error>{{ error }}</template>

    <template #footer>
      <Button variant="ghost" @click="emit('close')">取消</Button>
      <Button type="submit" form="endpoint-form" :disabled="saving">
        {{ saving ? '保存中…' : isEdit ? '更新' : '创建' }}
      </Button>
    </template>
  </SidePanel>
</template>

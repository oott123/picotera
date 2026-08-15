<script setup lang="ts">
import { ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import AnnotationsEditor from '@/components/AnnotationsEditor.vue'
import { SidePanel, Button, Input, Field, Select } from '@/ui'
import type { ProviderView } from '@/api'
import { invalidateProviders, upsertProvider } from '@/api/client'

const emit = defineEmits<{ close: [] }>()
const props = defineProps<{ provider?: ProviderView; onSave?: () => void }>()
const queryClient = useQueryClient()

const isEdit = !!props.provider

const modelsEndpointResolverOptions = [
  { value: 'followRequest', label: '跟随请求' },
  { value: 'bearerToken', label: 'Bearer Token' },
  { value: 'xApiKey', label: 'x-api-key' },
  { value: 'searchKey', label: 'Search Key' },
  { value: 'googApiKey', label: 'Google API Key' },
]

const form = ref({
  name: props.provider?.name ?? '',
  credentials: props.provider?.credentials ?? '',
  priority: props.provider?.priority ?? 0,
  annotations: { ...props.provider?.annotations } as Record<string, string>,
  disabled: props.provider?.disabled ?? false,
  proxyUrl: props.provider?.proxyUrl ?? '',
  insecureTls: props.provider?.insecureTls ?? false,
  modelsEndpointUrl: props.provider?.modelsEndpointUrl ?? '',
  modelsEndpointResolver: props.provider?.modelsEndpointResolver ?? 'followRequest',
  supportsNativeWebSearch: props.provider?.supportsNativeWebSearch ?? false,
})
const saving = ref(false)
const error = ref('')
const saveMutation = useMutation({
  mutationFn: upsertProvider,
  onSuccess: () => invalidateProviders(queryClient),
})

async function submit() {
  saving.value = true
  error.value = ''
  const body = {
    id: props.provider?.id ?? 0,
    name: form.value.name,
    credentials: form.value.credentials,
    priority: form.value.priority,
    providerModels: props.provider?.providerModels ?? [],
    annotations: form.value.annotations,
    disabled: form.value.disabled,
    ...(form.value.proxyUrl ? { proxyUrl: form.value.proxyUrl } : {}),
    insecureTls: form.value.insecureTls,
    modelsEndpointUrl: form.value.modelsEndpointUrl,
    modelsEndpointResolver: form.value.modelsEndpointResolver,
    supportsNativeWebSearch: form.value.supportsNativeWebSearch,
  }
  try {
    await saveMutation.mutateAsync(body)
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
    :title="isEdit ? form.name || '渠道' : '新增渠道'"
    :kicker="isEdit ? '编辑渠道' : '渠道'"
    @close="emit('close')"
  >
    <form id="provider-form" class="flex flex-col gap-4" @submit.prevent="submit">
      <Field label="名称">
        <Input v-model="form.name" required placeholder="例如 openai" />
      </Field>
      <Field label="凭证">
        <Input v-model="form.credentials" required placeholder="密钥或密钥" />
      </Field>
      <Field label="优先级">
        <Input v-model.number="form.priority" type="number" required />
      </Field>
      <Field label="状态" as="div">
        <label class="inline-flex items-center gap-2 text-sm cursor-pointer">
          <input v-model="form.disabled" type="checkbox" class="cursor-pointer" />
          <span>禁用此渠道（不参与调度）</span>
        </label>
      </Field>
      <Field label="标注" as="div">
        <AnnotationsEditor v-model="form.annotations" />
      </Field>
      <Field label="代理 URL">
        <Input v-model="form.proxyUrl" placeholder="留空使用环境代理，填 direct 禁用代理" />
      </Field>
      <Field label="TLS" as="div">
        <label class="inline-flex items-center gap-2 text-sm cursor-pointer">
          <input v-model="form.insecureTls" type="checkbox" class="cursor-pointer" />
          <span>不校验上游 HTTPS 证书（仅用于自签名证书的私有部署）</span>
        </label>
      </Field>
      <Field label="模型列表 URL">
        <Input v-model="form.modelsEndpointUrl" placeholder="https://api.openai.com/v1/models" />
      </Field>
      <Field label="模型列表凭证发送">
        <Select v-model="form.modelsEndpointResolver" :options="modelsEndpointResolverOptions" />
      </Field>
      <Field label="Web 搜索" as="div">
        <label class="inline-flex items-center gap-2 text-sm cursor-pointer">
          <input v-model="form.supportsNativeWebSearch" type="checkbox" class="cursor-pointer" />
          <span>支持原生 Web 搜索（关闭时由网关使用 Exa 模拟）</span>
        </label>
      </Field>
      <p v-if="!isEdit" class="text-xs text-ink-faint">
        保存后请在「模型」面板配置该渠道的模型列表。
      </p>
    </form>

    <template v-if="error" #error>{{ error }}</template>

    <template #footer>
      <Button variant="ghost" @click="emit('close')">取消</Button>
      <Button type="submit" form="provider-form" :disabled="saving">
        {{ saving ? '保存中…' : isEdit ? '更新' : '创建' }}
      </Button>
    </template>
  </SidePanel>
</template>

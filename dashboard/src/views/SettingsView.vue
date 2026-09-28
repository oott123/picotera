<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { getUserSetting, upsertUserSetting, invalidateUserSettings } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { Button, Field, SegmentedControl, StateText } from '@/ui'

const queryClient = useQueryClient()

type OtrMode = 'none' | 'body' | 'body-and-message'

const otrOptions: { value: OtrMode; label: string; description: string }[] = [
  {
    value: 'none',
    label: '完整记录',
    description: '记录请求体、响应体与用户消息梗概。',
  },
  {
    value: 'body',
    label: '不记录内容',
    description: '记录各类元数据与用户消息梗概，但不记录请求体与响应体。',
  },
  {
    value: 'body-and-message',
    label: '不记录内容和梗概',
    description: '仅记录各类元数据。',
  },
]

const autoCreateProjects = ref(false)
const otr = ref<OtrMode>('none')
const saved = ref(false)

const otrDescription = computed(
  () => otrOptions.find((o) => o.value === otr.value)?.description ?? '',
)

const autoCreateQuery = useQuery({
  queryKey: queryKeys.userSettings.detail('project.autoCreate'),
  queryFn: () => getUserSetting('project.autoCreate'),
  retry: false,
  // If the setting doesn't exist (404), return null instead of throwing.
  throwOnError: false,
})

const otrQuery = useQuery({
  queryKey: queryKeys.userSettings.detail('request.otr'),
  queryFn: () => getUserSetting('request.otr'),
  retry: false,
  throwOnError: false,
})

watch(
  () => autoCreateQuery.data.value,
  (data) => {
    autoCreateProjects.value = data?.value === true
  },
  { immediate: true },
)

watch(
  () => otrQuery.data.value,
  (data) => {
    const value = data?.value
    otr.value = value === 'body' || value === 'body-and-message' ? value : 'none'
  },
  { immediate: true },
)

const saveMutation = useMutation({
  mutationFn: async () => {
    await upsertUserSetting({
      key: 'project.autoCreate',
      value: autoCreateProjects.value,
    })
    await upsertUserSetting({
      key: 'request.otr',
      value: otr.value,
    })
  },
  onSuccess: () => {
    invalidateUserSettings(queryClient)
    saved.value = true
    setTimeout(() => {
      saved.value = false
    }, 2000)
  },
})
</script>

<template>
  <div class="flex flex-col gap-6 max-w-md">
    <StateText v-if="autoCreateQuery.isLoading.value || otrQuery.isLoading.value"
      >加载中…</StateText
    >
    <template v-else>
      <Field label="项目自动创建">
        <label class="flex items-center gap-2 text-sm">
          <input
            v-model="autoCreateProjects"
            type="checkbox"
            class="size-4 rounded border-line text-accent focus:ring-accent"
          />
          <span>允许自动创建项目</span>
        </label>
        <p class="text-xs text-ink-faint mt-1">
          当请求的工作目录未匹配到任何项目时，以该路径自动创建一个项目。
        </p>
      </Field>
      <Field label="数据记录" as="div">
        <SegmentedControl v-model="otr" :options="otrOptions" />
        <p class="text-xs text-ink-faint mt-1">
          {{ otrDescription }}在请求头中传入
          <code>X-PicoTera-OTR: {{ otr }}</code> 可使单个请求覆盖该设置。
        </p>
      </Field>
      <div class="flex items-center gap-3">
        <Button :disabled="saveMutation.isPending.value" @click="saveMutation.mutateAsync()">
          {{ saveMutation.isPending.value ? '保存中…' : '保存' }}
        </Button>
        <span v-if="saved" class="text-sm text-ok">已保存</span>
      </div>
    </template>
  </div>
</template>

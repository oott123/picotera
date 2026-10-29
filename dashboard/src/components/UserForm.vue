<script setup lang="ts">
import { ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import AnnotationsEditor from '@/components/AnnotationsEditor.vue'
import { SidePanel, Button, Input, Field } from '@/ui'
import type { UserView } from '@/api'
import { createUser, invalidateUsers, updateUser } from '@/api/client'

const emit = defineEmits<{ close: [] }>()
const props = defineProps<{ user?: UserView; onSave?: () => void }>()
const queryClient = useQueryClient()

const isEdit = !!props.user
const form = ref({
  displayName: props.user?.displayName ?? '',
  isAdmin: props.user?.isAdmin ?? false,
  disabled: props.user?.disabled ?? false,
  annotations: { ...props.user?.annotations } as Record<string, string>,
})
const saving = ref(false)
const error = ref('')
const saveMutation = useMutation({
  mutationFn: (body: {
    displayName: string
    isAdmin: boolean
    disabled: boolean
    annotations: Record<string, string>
  }) => (isEdit ? updateUser(props.user!.id, body) : createUser(body)),
  onSuccess: () => invalidateUsers(queryClient),
})

async function submit() {
  saving.value = true
  error.value = ''
  try {
    await saveMutation.mutateAsync({
      displayName: form.value.displayName,
      isAdmin: form.value.isAdmin,
      disabled: form.value.disabled,
      annotations: form.value.annotations,
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
    :title="isEdit ? form.displayName || '用户' : '新增用户'"
    :kicker="isEdit ? '编辑用户' : '用户'"
    @close="emit('close')"
  >
    <form id="user-form" class="flex flex-col gap-4" @submit.prevent="submit">
      <Field label="显示名">
        <Input v-model="form.displayName" required placeholder="例如 alice" />
      </Field>
      <Field label="角色" as="div">
        <label class="inline-flex items-center gap-2 text-sm cursor-pointer">
          <input v-model="form.isAdmin" type="checkbox" class="cursor-pointer" />
          <span>管理员</span>
        </label>
      </Field>
      <Field label="状态" as="div">
        <label class="inline-flex items-center gap-2 text-sm cursor-pointer">
          <input v-model="form.disabled" type="checkbox" class="cursor-pointer" />
          <span>禁用</span>
        </label>
      </Field>
      <Field label="标注" as="div">
        <AnnotationsEditor v-model="form.annotations" />
      </Field>
    </form>

    <template v-if="error" #error>{{ error }}</template>

    <template #footer>
      <Button variant="ghost" @click="emit('close')">取消</Button>
      <Button type="submit" form="user-form" :disabled="saving">
        {{ saving ? '保存中…' : isEdit ? '更新' : '创建' }}
      </Button>
    </template>
  </SidePanel>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { SidePanel, Button, Input, Field, Icon } from '@/ui'
import {
  addCredentialBegin,
  addCredentialComplete,
  renameMyCredential,
  invalidateOwnCredentials,
} from '@/api/client'
import { usePasskeyCeremony } from '@/composables/usePasskeyCeremony'
import type { components } from '@/openapi-types'

type CredentialView = components['schemas']['CredentialView']

const emit = defineEmits<{ close: [] }>()
const qc = useQueryClient()

const ceremony = usePasskeyCeremony<CredentialView>({
  begin: () => addCredentialBegin(),
  // No nickname at create time — set it post-success via the rename endpoint.
  complete: (attestation) => addCredentialComplete(attestation),
})

const nickname = ref('')
const renaming = ref(false)

const renameMutation = useMutation({
  mutationFn: ({ id, value }: { id: number; value: string | null }) =>
    renameMyCredential(id, value),
})

// Trigger the ceremony automatically when the dialog opens.
onMounted(() => {
  void ceremony.run()
})

async function onDone() {
  const cred = ceremony.result.value
  if (!cred) {
    emit('close')
    return
  }
  const trimmed = nickname.value.trim()
  if (trimmed) {
    renaming.value = true
    try {
      await renameMutation.mutateAsync({ id: cred.id, value: trimmed })
    } finally {
      renaming.value = false
    }
  }
  // Refresh credentials list regardless (the new row was added during complete).
  invalidateOwnCredentials(qc)
  emit('close')
}

function onRetry() {
  ceremony.reset()
  void ceremony.run()
}

function onCancel() {
  // If a successful credential was created, still invalidate so the table updates.
  if (ceremony.result.value) invalidateOwnCredentials(qc)
  emit('close')
}
</script>

<template>
  <SidePanel title="添加 Passkey" kicker="Passkey" @close="onCancel">
    <!-- Waiting view: ceremony in progress -->
    <div v-if="ceremony.phase.value === 'waiting'" class="flex flex-col items-center gap-4 py-8">
      <div class="w-12 h-12 rounded-full border-2 border-line border-t-accent animate-spin"></div>
      <p class="text-sm text-ink-muted text-center">
        请使用您的 Passkey 设备完成验证…<br />
        <span class="text-xs text-ink-faint">浏览器或 Passkey 管理器将弹出确认窗口</span>
      </p>
    </div>

    <!-- Success view: prompt for nickname -->
    <div v-else-if="ceremony.phase.value === 'success'" class="flex flex-col gap-4">
      <div class="flex items-center gap-2 text-success">
        <Icon name="check" :size="16" />
        <span class="text-sm font-medium">Passkey 创建成功</span>
      </div>
      <p class="text-sm text-ink-muted">为这把 Passkey 起个名字（可选）：</p>
      <Field label="昵称（可选）">
        <Input
          v-model="nickname"
          maxlength="60"
          placeholder="例如 我的 MacBook"
          autofocus
        />
      </Field>
    </div>

    <!-- Error view: dedicated error display + retry -->
    <div v-else-if="ceremony.phase.value === 'error'" class="flex flex-col gap-4">
      <div class="bg-err-faint text-err-ink rounded-md px-3 py-2 text-sm">
        {{ ceremony.errorMessage.value }}
      </div>
    </div>

    <template #footer>
      <template v-if="ceremony.phase.value === 'success'">
        <Button :disabled="renaming" @click="onDone">完成</Button>
      </template>
      <template v-else-if="ceremony.phase.value === 'error'">
        <Button variant="ghost" @click="onCancel">关闭</Button>
        <Button @click="onRetry">重试</Button>
      </template>
      <!-- waiting state: no footer buttons; user is interacting with their authenticator -->
    </template>
  </SidePanel>
</template>

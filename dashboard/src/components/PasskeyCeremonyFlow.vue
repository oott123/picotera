<script setup lang="ts" generic="TResult">
import { onMounted, ref } from 'vue'
import { Button, Input, Field, Icon } from '@/ui'
import { usePasskeyCeremony } from '@/composables/usePasskeyCeremony'

const props = defineProps<{
  /** /begin call — returns CreationOptionsJSON. */
  begin: () => Promise<unknown>
  /** /complete call — returns the success result (e.g. CredentialView or {session, newCredentialId}). */
  complete: (attestation: unknown) => Promise<TResult>
  /** Rename call. Invoked with the credential id + non-empty trimmed nickname AFTER ceremony success. */
  rename: (credentialId: number, nickname: string) => Promise<void>
  /** Pulls the credential id from the success result so the flow can call rename. */
  extractCredentialId: (result: TResult) => number
}>()

const emit = defineEmits<{
  /** Fired after the user clicks 完成 in the success view. Includes the result + (already-applied) nickname. */
  done: [result: TResult]
  /** Fired when the user clicks 关闭 in the error view, or cancels without success. */
  close: []
}>()

const ceremony = usePasskeyCeremony<TResult>({
  begin: () => props.begin(),
  complete: (attestation) => props.complete(attestation),
})

const nickname = ref('')
const renaming = ref(false)

onMounted(() => {
  void ceremony.run()
})

async function onDone() {
  const result = ceremony.result.value
  if (!result) return
  const trimmed = nickname.value.trim()
  if (trimmed) {
    renaming.value = true
    try {
      await props.rename(props.extractCredentialId(result), trimmed)
    } catch {
      // Rename failure is non-fatal — the credential exists, user can rename later.
      // Swallow and proceed; parent receives result on emit('done').
    } finally {
      renaming.value = false
    }
  }
  emit('done', result)
}

function onRetry() {
  ceremony.reset()
  void ceremony.run()
}
</script>

<template>
  <div>
    <!-- Waiting view -->
    <div
      v-if="ceremony.phase.value === 'waiting'"
      class="flex flex-col items-center gap-4 py-8"
    >
      <div class="w-12 h-12 rounded-full border-2 border-line border-t-accent animate-spin"></div>
      <p class="text-sm text-ink-muted text-center">
        请使用您的 Passkey 设备完成验证…<br />
        <span class="text-xs text-ink-faint">浏览器或 Passkey 管理器将弹出确认窗口</span>
      </p>
    </div>

    <!-- Success view -->
    <div
      v-else-if="ceremony.phase.value === 'success'"
      class="flex flex-col gap-4"
    >
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
      <div class="flex justify-end">
        <Button :disabled="renaming" @click="onDone">完成</Button>
      </div>
    </div>

    <!-- Error view -->
    <div
      v-else-if="ceremony.phase.value === 'error'"
      class="flex flex-col gap-4"
    >
      <div class="bg-err-faint text-err-ink rounded-md px-3 py-2 text-sm">
        {{ ceremony.errorMessage.value }}
      </div>
      <div class="flex justify-end gap-2">
        <Button variant="ghost" @click="emit('close')">关闭</Button>
        <Button @click="onRetry">重试</Button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import {
  fetchEnrollment,
  beginEnrollmentRegistration,
  completeEnrollmentRegistration,
  renameMyCredential,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { usePasskeyCeremony } from '@/composables/usePasskeyCeremony'
import { Button, Input, Field, Icon } from '@/ui'
import { fallbackFor } from '@/router/fallback'
import type { components } from '@/openapi-types'

type SessionView = components['schemas']['SessionView']
type CeremonyResult = { session: SessionView; newCredentialId: number }

const route = useRoute()
const router = useRouter()
const qc = useQueryClient()
const token = computed(() => String(route.params.token))

const preview = useQuery({
  queryKey: computed(() => queryKeys.enrollments.detail(token.value)),
  queryFn: () => fetchEnrollment(token.value),
  retry: false,
  // Token is consumed exactly once — no value in refetching while the user fills the form.
  staleTime: Infinity,
})

const bootstrapForm = reactive({ username: '', displayName: '' })
const inviteForm = reactive({ username: '', displayName: '' })
const resetConfirmed = ref(false)

const ceremony = usePasskeyCeremony<CeremonyResult>({
  begin: async () => {
    const intent = preview.data.value!.intent
    const body =
      intent === 'bootstrap'
        ? { username: bootstrapForm.username, displayName: bootstrapForm.displayName }
        : intent === 'invite'
          ? { username: inviteForm.username, displayName: inviteForm.displayName }
          : {}
    return beginEnrollmentRegistration(token.value, body)
  },
  complete: (attestation) => completeEnrollmentRegistration(token.value, attestation),
})

const nickname = ref('')
const renaming = ref(false)

function onSubmit(e: Event) {
  e.preventDefault()
  void ceremony.run()
}

async function onNamingDone() {
  const r = ceremony.result.value
  if (!r) return
  qc.setQueryData(queryKeys.session.current, r.session)
  const trimmed = nickname.value.trim()
  if (trimmed) {
    renaming.value = true
    try {
      await renameMyCredential(r.newCredentialId, trimmed)
    } catch {
      // Rename failure is non-fatal; user can rename later from /me. Don't block navigation.
    } finally {
      renaming.value = false
    }
  }
  router.replace(fallbackFor(r.session))
}

function onRetry() {
  ceremony.reset()
}

const submitDisabled = computed(() => {
  if (ceremony.phase.value === 'waiting') return true
  if (preview.data.value?.intent === 'reset' && !resetConfirmed.value) return true
  return false
})
</script>

<template>
  <div class="bg-surface-0 border border-line rounded-lg shadow-sm p-8 w-full max-w-sm">
    <div v-if="preview.isPending.value" class="text-sm text-ink-faint">加载中…</div>

    <div v-else-if="preview.isError.value" class="flex flex-col gap-3">
      <p class="text-sm text-err">邀请链接无效、已使用或已过期。</p>
      <RouterLink to="/login" class="text-sm text-accent hover:underline">返回登录</RouterLink>
    </div>

    <!-- Form phase (idle): show the appropriate intent form -->
    <template v-else-if="ceremony.phase.value === 'idle'">
      <!-- bootstrap: operator creates the first admin account -->
      <form
        v-if="preview.data.value?.intent === 'bootstrap'"
        class="flex flex-col gap-4"
        @submit="onSubmit"
      >
        <h1 class="text-xl font-semibold text-ink">创建管理员用户</h1>
        <Field label="用户名">
          <Input
            v-model="bootstrapForm.username"
            mono
            required
            pattern="[a-z0-9_\-]{2,32}"
            autocomplete="username"
            placeholder="alice"
          />
        </Field>
        <Field label="显示名">
          <Input
            v-model="bootstrapForm.displayName"
            required
            maxlength="80"
            autocomplete="name"
            placeholder="Alice"
          />
        </Field>
        <Button type="submit" :disabled="submitDisabled">注册 Passkey</Button>
      </form>

      <!-- invite: invitee picks their own username/displayName -->
      <form
        v-else-if="preview.data.value?.intent === 'invite'"
        class="flex flex-col gap-4"
        @submit="onSubmit"
      >
        <h1 class="text-xl font-semibold text-ink">接受邀请</h1>
        <Field label="用户名">
          <Input
            v-model.trim="inviteForm.username"
            mono
            required
            pattern="[a-z0-9_\-]{2,32}"
            autocomplete="username"
          />
        </Field>
        <Field label="显示名">
          <Input
            v-model.trim="inviteForm.displayName"
            required
            maxlength="80"
            autocomplete="name"
          />
        </Field>
        <Button type="submit" :disabled="submitDisabled">注册 Passkey</Button>
      </form>

      <!-- reset: replaces all existing passkeys; warn and require confirmation -->
      <form
        v-else-if="preview.data.value?.intent === 'reset'"
        class="flex flex-col gap-4"
        @submit="onSubmit"
      >
        <h1 class="text-xl font-semibold text-ink">重置 Passkey</h1>
        <Field label="用户">
          <Input :model-value="preview.data.value.target?.username" mono disabled />
        </Field>
        <div class="bg-err-faint text-err-ink text-sm rounded-md px-3 py-2">
          此操作将删除该用户的所有现有 Passkey。
        </div>
        <label class="flex items-start gap-2 text-sm text-ink-muted cursor-pointer select-none">
          <input v-model="resetConfirmed" type="checkbox" class="mt-0.5 accent-accent" />
          <span>我已了解此操作将删除现有所有密钥。</span>
        </label>
        <Button type="submit" :disabled="submitDisabled">注册 Passkey</Button>
      </form>
    </template>

    <!-- Waiting phase -->
    <div v-else-if="ceremony.phase.value === 'waiting'" class="flex flex-col items-center gap-4 py-8">
      <div class="w-12 h-12 rounded-full border-2 border-line border-t-accent animate-spin"></div>
      <p class="text-sm text-ink-muted text-center">
        请使用您的 Passkey 设备完成验证…<br />
        <span class="text-xs text-ink-faint">浏览器或 Passkey 管理器将弹出确认窗口</span>
      </p>
    </div>

    <!-- Success phase: name the new passkey -->
    <div v-else-if="ceremony.phase.value === 'success'" class="flex flex-col gap-4">
      <div class="flex items-center gap-2 text-success">
        <Icon name="check" :size="16" />
        <span class="text-sm font-medium">Passkey 创建成功</span>
      </div>
      <p class="text-sm text-ink-muted">为这把 Passkey 起个名字（可选）：</p>
      <Field label="昵称（可选）">
        <Input v-model="nickname" maxlength="60" placeholder="例如 我的 MacBook" autofocus />
      </Field>
      <Button :disabled="renaming" @click="onNamingDone">完成</Button>
    </div>

    <!-- Error phase -->
    <div v-else-if="ceremony.phase.value === 'error'" class="flex flex-col gap-4">
      <div class="bg-err-faint text-err-ink rounded-md px-3 py-2 text-sm">
        {{ ceremony.errorMessage.value }}
      </div>
      <div class="flex justify-end gap-2">
        <Button @click="onRetry">重试</Button>
      </div>
    </div>
  </div>
</template>

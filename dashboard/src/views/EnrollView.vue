<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import {
  fetchEnrollment,
  beginEnrollmentRegistration,
  completeEnrollmentRegistration,
  ApiRequestError,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { webauthnCreate, WebAuthnUserCancelled } from '@/api/webauthn'
import { Button, Input, Field } from '@/ui'
import { fallbackFor } from '@/router/fallback'

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
const errorMessage = ref<string | null>(null)

const enroll = useMutation({
  mutationFn: async () => {
    const intent = preview.data.value!.intent
    const body =
      intent === 'bootstrap'
        ? { username: bootstrapForm.username, displayName: bootstrapForm.displayName }
        : intent === 'invite'
          ? { username: inviteForm.username, displayName: inviteForm.displayName }
          : {}
    const options = await beginEnrollmentRegistration(token.value, body)
    const attestation = await webauthnCreate(options as Parameters<typeof webauthnCreate>[0])
    return completeEnrollmentRegistration(token.value, attestation)
  },
  onSuccess(session) {
    qc.setQueryData(queryKeys.session.current, session)
    // Route to the best page for this session — non-admins can't access /overview.
    router.replace(fallbackFor(session))
  },
  onError(err: unknown) {
    console.error('[enroll] registration failed', err)
    if (err instanceof WebAuthnUserCancelled) {
      errorMessage.value = '取消或超时，请重试。'
      return
    }
    if (err instanceof ApiRequestError) {
      errorMessage.value = err.message
      return
    }
    if (err instanceof DOMException) {
      errorMessage.value = `注册失败：${err.name}: ${err.message}`
      return
    }
    if (err instanceof Error) {
      errorMessage.value = `注册失败：${err.message}`
      return
    }
    errorMessage.value = '注册失败，请重试。'
  },
})

function onSubmit(e: Event) {
  e.preventDefault()
  errorMessage.value = null
  enroll.mutate()
}

const submitDisabled = computed(() => {
  if (enroll.isPending.value) return true
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

    <!-- bootstrap: operator creates the first admin account -->
    <form
      v-else-if="preview.data.value?.intent === 'bootstrap'"
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
      <p v-if="errorMessage" class="text-sm text-err">{{ errorMessage }}</p>
    </form>

    <!-- invite: invitee picks their own username/displayName (optionally prefilled from admin's template) -->
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
      <p v-if="errorMessage" class="text-sm text-err">{{ errorMessage }}</p>
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
      <p v-if="errorMessage" class="text-sm text-err">{{ errorMessage }}</p>
    </form>
  </div>
</template>

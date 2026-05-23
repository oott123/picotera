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
import { Button, Input, Field } from '@/ui'
import { fallbackFor } from '@/router/fallback'
import PasskeyCeremonyFlow from '@/components/PasskeyCeremonyFlow.vue'
import type { components } from '@/openapi-types'

type SessionView = components['schemas']['SessionView']
type EnrollResult = { session: SessionView; newCredentialId: number }

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

// Captures the begin call for the current ceremony attempt — set when the
// user submits a form, used by PasskeyCeremonyFlow on mount.
const ceremonyBegin = ref<(() => Promise<unknown>) | null>(null)

function startBootstrap(e: Event) {
  e.preventDefault()
  ceremonyBegin.value = () =>
    beginEnrollmentRegistration(token.value, {
      username: bootstrapForm.username,
      displayName: bootstrapForm.displayName,
    })
}

function startInvite(e: Event) {
  e.preventDefault()
  ceremonyBegin.value = () =>
    beginEnrollmentRegistration(token.value, {
      username: inviteForm.username,
      displayName: inviteForm.displayName,
    })
}

function startReset(e: Event) {
  e.preventDefault()
  ceremonyBegin.value = () => beginEnrollmentRegistration(token.value, {})
}

function completeFn(attestation: unknown): Promise<EnrollResult> {
  return completeEnrollmentRegistration(token.value, attestation)
}

function extractCredentialId(r: EnrollResult): number {
  return r.newCredentialId
}

function onDone(result: EnrollResult) {
  qc.setQueryData(queryKeys.session.current, result.session)
  router.replace(fallbackFor(result.session))
}

function onClose() {
  // Reset back to the form view so the user can change inputs and retry.
  ceremonyBegin.value = null
}
</script>

<template>
  <div class="bg-surface-0 border border-line rounded-lg shadow-sm p-8 w-full max-w-sm">
    <div v-if="preview.isPending.value" class="text-sm text-ink-faint">加载中…</div>

    <div v-else-if="preview.isError.value" class="flex flex-col gap-3">
      <p class="text-sm text-err">邀请链接无效、已使用或已过期。</p>
      <RouterLink to="/login" class="text-sm text-accent hover:underline">返回登录</RouterLink>
    </div>

    <!-- Form view: not yet started -->
    <template v-else-if="!ceremonyBegin">
      <!-- bootstrap: operator creates the first admin account -->
      <form
        v-if="preview.data.value?.intent === 'bootstrap'"
        class="flex flex-col gap-4"
        @submit="startBootstrap"
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
        <Button type="submit">注册 Passkey</Button>
      </form>

      <!-- invite: invitee picks their own username/displayName -->
      <form
        v-else-if="preview.data.value?.intent === 'invite'"
        class="flex flex-col gap-4"
        @submit="startInvite"
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
        <Button type="submit">注册 Passkey</Button>
      </form>

      <!-- reset: replaces all existing passkeys; warn and require confirmation -->
      <form
        v-else-if="preview.data.value?.intent === 'reset'"
        class="flex flex-col gap-4"
        @submit="startReset"
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
        <Button type="submit" :disabled="!resetConfirmed">注册 Passkey</Button>
      </form>
    </template>

    <!-- Ceremony flow: form has been submitted; the shared component drives waiting/success/error. -->
    <PasskeyCeremonyFlow
      v-else
      :begin="ceremonyBegin"
      :complete="completeFn"
      :rename="renameMyCredential"
      :extract-credential-id="extractCredentialId"
      @done="onDone"
      @close="onClose"
    />
  </div>
</template>

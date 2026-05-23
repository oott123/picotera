<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useMutation, useQueryClient } from '@tanstack/vue-query'
import { SidePanel, Button, Input, Field, Icon, SegmentedControl } from '@/ui'
import type { components } from '@/openapi-types'
import { createInvitation, updateAccount, invalidateAccounts, invalidateInvitations } from '@/api/client'

type AccountView = components['schemas']['AccountView']
type Permissions = components['schemas']['Permissions']

const emit = defineEmits<{ close: [] }>()
const props = defineProps<{
  account?: AccountView
  // If provided on open, skip straight to the URL-display view (reissue case; URL is reveal-once for resets)
  revealUrl?: string
  revealExpiresAt?: string
}>()
const queryClient = useQueryClient()

const isEdit = !!props.account

const revealData = ref<{ url: string; expiresAt: string } | null>(
  props.revealUrl && props.revealExpiresAt
    ? { url: props.revealUrl, expiresAt: props.revealExpiresAt }
    : null,
)

const defaultPermissions: Permissions = {
  view_own_usage: true,
  manage_own_api_keys: true,
  view_models: true,
  view_own_traces: true,
}

const form = ref({
  username: props.account?.username ?? '',
  displayName: props.account?.displayName ?? '',
  role: props.account?.role ?? 'user',
  permissions: { ...(props.account?.permissions ?? defaultPermissions) } as Permissions,
  disabled: props.account?.disabled ?? false,
})

const isAdmin = computed(() => form.value.role === 'admin')

// Clear disabled flag when promoting to admin — admins can't be disabled.
watch(
  () => form.value.role,
  (role) => {
    if (role === 'admin' && form.value.disabled) {
      form.value.disabled = false
    }
  },
)

// Admin role implies all permissions — lock the checkboxes to all-true so
// the visible state matches what the server will persist (P5.02).
watch(
  () => form.value.role,
  (role) => {
    if (role === 'admin') {
      form.value.permissions = {
        view_own_usage: true,
        manage_own_api_keys: true,
        view_models: true,
        view_own_traces: true,
      }
    }
  },
  { immediate: true },
)

const saving = ref(false)
const error = ref('')
const copied = ref(false)
let copyTimer: ReturnType<typeof setTimeout> | null = null

const roleOptions = [
  { value: 'admin', label: '管理员' },
  { value: 'user', label: '标准用户' },
]

// Permission labels matching MeView.vue vocabulary
const permLabels: Record<keyof Permissions, string> = {
  view_own_usage: '查看自己的用量',
  manage_own_api_keys: '管理自己的 API Key',
  view_models: '查看模型',
  view_own_traces: '查看自己的链路',
}

const panelTitle = computed(() => {
  if (revealData.value) return '邀请已创建'
  if (isEdit) return props.account?.displayName || props.account?.username || '用户'
  return '邀请用户'
})

const panelKicker = computed(() => {
  if (revealData.value) return '邀请链接'
  if (isEdit) return '编辑用户'
  return '用户'
})

const updateMutation = useMutation({
  mutationFn: (body: { displayName: string; role: string; permissions: Permissions; disabled: boolean }) =>
    updateAccount(props.account!.id, body),
  onSuccess: () => invalidateAccounts(queryClient),
})

const inviteMutation = useMutation({
  mutationFn: (body: { role: string; permissions: Permissions }) =>
    createInvitation(body),
  onSuccess: () => {
    invalidateAccounts(queryClient)
    invalidateInvitations(queryClient)
  },
})

async function submit() {
  saving.value = true
  error.value = ''
  try {
    if (isEdit) {
      await updateMutation.mutateAsync({
        displayName: form.value.displayName,
        role: form.value.role,
        permissions: form.value.permissions,
        disabled: form.value.disabled,
      })
      emit('close')
    } else {
      const result = await inviteMutation.mutateAsync({
        role: form.value.role,
        permissions: form.value.permissions,
      })
      // Swap form to URL-display view on successful invite
      revealData.value = { url: result.url, expiresAt: result.expiresAt }
    }
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '操作失败'
  }
  saving.value = false
}

async function copyUrl() {
  if (!revealData.value) return
  try {
    await navigator.clipboard.writeText(revealData.value.url)
    copied.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => {
      copied.value = false
    }, 1500)
  } catch {
    // clipboard unavailable — silently ignore
  }
}

function fmtTime(iso?: string | null): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN')
}

</script>

<template>
  <SidePanel :title="panelTitle" :kicker="panelKicker" @close="emit('close')">
    <!-- URL-display view: shown after invite creation (URL queryable via listInvitations), or when revealUrl prop is provided (reissue; those are reveal-once) -->
    <div v-if="revealData" class="flex flex-col gap-4">
      <p class="text-sm text-ink-muted">
        把下面的链接发给被邀请人。链接也会出现在用户管理页的「待发送邀请」列表中，可以随时复制或撤销。
      </p>
      <p class="text-xs text-ink-faint">
        受邀者注册成功后将出现在用户列表中。
      </p>
      <div class="flex items-stretch gap-2">
        <Input :model-value="revealData.url" readonly class="flex-1 font-mono text-xs" />
        <Button @click="copyUrl">
          <Icon :name="copied ? 'check' : 'copy'" :size="13" />
          <span>{{ copied ? '已复制' : '复制' }}</span>
        </Button>
      </div>
      <p class="text-xs text-ink-faint">过期时间：{{ fmtTime(revealData.expiresAt) }}</p>
    </div>

    <!-- Normal form view -->
    <form v-else id="account-form" class="flex flex-col gap-4" @submit.prevent="submit">
      <template v-if="isEdit">
        <Field label="用户名">
          <Input
            v-model="form.username"
            :disabled="isEdit"
            required
            placeholder="例如 alice"
            pattern="[a-z0-9_\-]{2,32}"
            title="2-32 个小写字母、数字、_ 或 -"
          />
        </Field>
        <Field label="显示名称">
          <Input v-model="form.displayName" required placeholder="例如 Alice Smith" />
        </Field>
      </template>
      <Field label="角色" as="div">
        <SegmentedControl v-model="form.role" :options="roleOptions" :columns="2" />
      </Field>
      <Field label="权限" as="div">
        <div class="flex flex-col gap-1.5">
          <label
            v-for="(label, perm) in permLabels"
            :key="perm"
            class="flex items-center gap-2 text-sm cursor-pointer"
            :class="isAdmin ? 'text-ink-faint' : 'text-ink-muted'"
          >
            <input
              v-model="form.permissions[perm as keyof Permissions]"
              :disabled="isAdmin"
              type="checkbox"
              class="accent-accent"
            />
            <span>{{ label }}</span>
          </label>
          <p v-if="isAdmin" class="text-2xs text-ink-faint mt-1">
            管理员拥有全部权限
          </p>
        </div>
      </Field>
      <Field v-if="isEdit" label="状态" as="div">
        <label class="flex items-center gap-2 text-sm text-ink-muted cursor-pointer">
          <input
            v-model="form.disabled"
            :disabled="form.role === 'admin'"
            type="checkbox"
            class="accent-accent"
          />
          <span :class="form.role === 'admin' ? 'text-ink-faint' : ''">
            禁用用户
            <span v-if="form.role === 'admin'" class="text-2xs">（管理员不可禁用）</span>
          </span>
        </label>
      </Field>
    </form>

    <template v-if="error" #error>{{ error }}</template>

    <template #footer>
      <Button v-if="revealData" variant="ghost" @click="emit('close')">完成</Button>
      <template v-else>
        <Button variant="ghost" @click="emit('close')">取消</Button>
        <Button type="submit" form="account-form" :disabled="saving">
          {{ saving ? '保存中…' : isEdit ? '更新' : '发送邀请' }}
        </Button>
      </template>
    </template>
  </SidePanel>
</template>

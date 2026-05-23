<script setup lang="ts">
import { ref, computed } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useConfirm } from '@/composables/useConfirm'
import { useSidePanel } from '@/composables/useSidePanel'
import { useSession } from '@/composables/useSession'
import type { components } from '@/openapi-types'
import {
  deleteAccount,
  invalidateAccounts,
  invalidateInvitations,
  listAccounts,
  listInvitations,
  reissueEnrollment,
  revokeAccountSessions,
  revokeInvitation,
  updateAccount,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import AccountForm from '@/components/AccountForm.vue'
import { Button, IconButton, DataCard, DataTable, Th, Td, Tr, StateText, Tag, Icon } from '@/ui'

type AccountView = components['schemas']['AccountView']
type InvitationView = components['schemas']['InvitationView']

const panel = useSidePanel()
const confirm = useConfirm()
const queryClient = useQueryClient()
const session = useSession()

// Sentinel -1: account ids are positive serials, so -1 never matches a real id.
const selfId = computed(() => session.user.value?.id ?? -1)

const statusMessage = ref<{ kind: 'success' | 'error'; text: string } | null>(null)
let statusTimer: ReturnType<typeof setTimeout> | null = null

function flashStatus(kind: 'success' | 'error', text: string) {
  statusMessage.value = { kind, text }
  if (statusTimer) clearTimeout(statusTimer)
  statusTimer = setTimeout(() => {
    statusMessage.value = null
  }, 4000)
}

const accountsQuery = useQuery({
  queryKey: queryKeys.accounts.list,
  queryFn: listAccounts,
})
const accounts = computed(() => accountsQuery.data.value ?? [])
const loading = computed(() => accountsQuery.isLoading.value)
const count = computed(() => accounts.value.length)

const invitationsQuery = useQuery({
  queryKey: queryKeys.invitations.all,
  queryFn: listInvitations,
})
const invitations = computed(() => invitationsQuery.data.value ?? [])

const copiedToken = ref<string | null>(null)
let copyTimer: ReturnType<typeof setTimeout> | null = null

async function copyInviteUrl(inv: InvitationView) {
  try {
    await navigator.clipboard.writeText(inv.url)
    copiedToken.value = inv.token
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => {
      copiedToken.value = null
    }, 1500)
  } catch {
    // clipboard unavailable — silently ignore
  }
}

const revokeInvitationMutation = useMutation({
  mutationFn: (token: string) => revokeInvitation(token),
  onSuccess: () => invalidateInvitations(queryClient),
})

function confirmRevokeInvitation(inv: InvitationView) {
  confirm.require({
    message: '确定要撤销这个邀请吗？该链接将立即失效。',
    accept: async () => {
      await revokeInvitationMutation.mutateAsync(inv.token)
    },
  })
}

// Track which row's reissue is in-flight to show a transient loading state
const reissuingId = ref<number | null>(null)
const revokingId = ref<number | null>(null)

const deleteAccountMutation = useMutation({
  mutationFn: (id: number) => deleteAccount(id),
  onSuccess: () => invalidateAccounts(queryClient),
})

const toggleMutation = useMutation({
  mutationFn: (a: AccountView) =>
    updateAccount(a.id, {
      displayName: a.displayName,
      role: a.role,
      permissions: a.permissions,
      disabled: !a.disabled,
    }),
  onSuccess: () => invalidateAccounts(queryClient),
  onError: (e: unknown) => {
    // Surface last-admin 409 as an inline banner; no crash
    flashStatus('error', e instanceof Error ? e.message : '操作失败')
  },
})

function openInvite() {
  panel.open(AccountForm, {}, { key: 'account:new', width: '560px' })
}

function openEdit(a: AccountView) {
  panel.open(AccountForm, { account: a }, { key: `account:${a.id}`, width: '560px' })
}

async function openReissue(a: AccountView) {
  reissuingId.value = a.id
  try {
    const result = await reissueEnrollment(a.id)
    panel.open(
      AccountForm,
      { revealUrl: result.url, revealExpiresAt: result.expiresAt },
      { key: `account:reissue:${a.id}`, width: '560px' },
    )
  } catch (e: unknown) {
    flashStatus('error', e instanceof Error ? e.message : '操作失败')
  } finally {
    reissuingId.value = null
  }
}

function confirmToggleDisabled(a: AccountView) {
  const willDisable = !a.disabled
  confirm.require({
    message: willDisable
      ? `确定要禁用用户「${a.displayName || a.username}」吗？该用户将无法登录。`
      : `确定要启用用户「${a.displayName || a.username}」吗？`,
    accept: async () => {
      await toggleMutation.mutateAsync(a)
    },
  })
}

async function revokeSessionsFor(a: AccountView) {
  revokingId.value = a.id
  try {
    const result = await revokeAccountSessions(a.id)
    flashStatus('success', `已吊销 ${result.revoked} 个会话`)
    invalidateAccounts(queryClient)
  } catch (e: unknown) {
    flashStatus('error', e instanceof Error ? e.message : '操作失败')
  } finally {
    revokingId.value = null
  }
}

function confirmDelete(a: AccountView) {
  confirm.require({
    message: `确定要删除用户「${a.displayName || a.username}」吗？此操作不可撤销。`,
    accept: async () => {
      await deleteAccountMutation.mutateAsync(a.id)
    },
  })
}

function confirmRevokeSessions(a: AccountView) {
  confirm.require({
    message: `确定要吊销「${a.displayName || a.username}」的所有会话吗？该用户将被迫重新登录。`,
    accept: async () => {
      await revokeSessionsFor(a)
    },
  })
}

function fmtTime(iso?: string | null): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN')
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div
      v-if="statusMessage"
      class="rounded-md px-3 py-2 text-sm"
      :class="statusMessage.kind === 'success' ? 'bg-accent-faint text-accent-ink' : 'bg-err-faint text-err-ink'"
    >
      {{ statusMessage.text }}
    </div>
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">{{ count }} 个用户</span>
      <div class="flex items-center gap-2">
        <Button @click="openInvite">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>邀请用户</span>
        </Button>
      </div>
    </div>
    <DataCard v-if="invitations.length > 0">
      <div class="px-4 py-3 border-b border-line">
        <h3 class="text-sm font-medium text-ink">待发送邀请（{{ invitations.length }}）</h3>
      </div>
      <DataTable>
        <thead>
          <tr>
            <Th>角色</Th>
            <Th>权限</Th>
            <Th>创建时间</Th>
            <Th>过期时间</Th>
            <Th actions />
          </tr>
        </thead>
        <tbody>
          <Tr v-for="inv in invitations" :key="inv.token">
            <Td>
              <Tag :variant="inv.role === 'admin' ? 'accent' : 'default'">
                {{ inv.role === 'admin' ? '管理员' : '标准用户' }}
              </Tag>
            </Td>
            <Td>
              <div class="flex flex-wrap gap-1">
                <Tag v-if="inv.role === 'admin'" variant="muted">全部</Tag>
                <template v-else>
                  <Tag v-if="inv.permissions.view_own_usage" variant="muted">用量</Tag>
                  <Tag v-if="inv.permissions.manage_own_api_keys" variant="muted">密钥</Tag>
                  <Tag v-if="inv.permissions.view_models" variant="muted">模型</Tag>
                  <Tag v-if="inv.permissions.view_own_traces" variant="muted">链路</Tag>
                  <Tag v-if="inv.permissions.manage_own_projects" variant="muted">项目</Tag>
                </template>
              </div>
            </Td>
            <Td>
              <span class="text-xs text-ink-muted">{{ fmtTime(inv.createdAt) }}</span>
            </Td>
            <Td>
              <span class="text-xs text-ink-muted">{{ fmtTime(inv.expiresAt) }}</span>
            </Td>
            <Td actions>
              <div class="inline-flex gap-1">
                <IconButton
                  :title="copiedToken === inv.token ? '已复制' : '复制链接'"
                  :aria-label="copiedToken === inv.token ? '已复制' : '复制链接'"
                  @click="copyInviteUrl(inv)"
                >
                  <Icon :name="copiedToken === inv.token ? 'check' : 'copy'" :size="13" />
                </IconButton>
                <IconButton
                  title="撤销邀请"
                  aria-label="撤销邀请"
                  :disabled="revokeInvitationMutation.isPending.value"
                  @click="confirmRevokeInvitation(inv)"
                >
                  <Icon name="trash" :size="13" />
                </IconButton>
              </div>
            </Td>
          </Tr>
        </tbody>
      </DataTable>
    </DataCard>
    <StateText v-if="loading">加载中…</StateText>
    <DataCard v-else-if="accounts.length">
      <DataTable>
        <thead>
          <tr>
            <Th>用户名</Th>
            <Th>显示名称</Th>
            <Th>角色</Th>
            <Th>权限</Th>
            <Th>上次登录</Th>
            <Th actions />
          </tr>
        </thead>
        <tbody>
          <Tr
            v-for="a in accounts"
            :key="a.id"
            :selected="panel.isActive(`account:${a.id}`)"
            :class="a.disabled ? 'opacity-55' : ''"
          >
            <Td>
              <code class="font-mono text-xs text-ink">{{ a.username }}</code>
              <Tag v-if="a.disabled" variant="muted" class="ml-1.5">已禁用</Tag>
            </Td>
            <Td>
              <span class="font-medium">{{ a.displayName }}</span>
            </Td>
            <Td>
              <Tag :variant="a.role === 'admin' ? 'accent' : 'default'">
                {{ a.role === 'admin' ? '管理员' : '标准用户' }}
              </Tag>
            </Td>
            <Td>
              <div class="flex flex-wrap gap-1">
                <Tag v-if="a.permissions.view_own_usage" variant="muted">用量</Tag>
                <Tag v-if="a.permissions.manage_own_api_keys" variant="muted">密钥</Tag>
                <Tag v-if="a.permissions.view_models" variant="muted">模型</Tag>
                <Tag v-if="a.permissions.view_own_traces" variant="muted">链路</Tag>
                <Tag v-if="a.permissions.manage_own_projects" variant="muted">项目</Tag>
                <span
                  v-if="
                    !a.permissions.view_own_usage &&
                    !a.permissions.manage_own_api_keys &&
                    !a.permissions.view_models &&
                    !a.permissions.view_own_traces &&
                    !a.permissions.manage_own_projects
                  "
                  class="text-xs text-ink-faint"
                >—</span>
              </div>
            </Td>
            <Td>
              <span class="text-xs text-ink-muted">{{ fmtTime(a.lastSignInAt) }}</span>
            </Td>
            <Td actions>
              <div class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity">
                <IconButton
                  v-if="a.role !== 'admin'"
                  :title="a.disabled ? '启用' : '禁用'"
                  :aria-label="a.disabled ? '启用' : '禁用'"
                  @click="confirmToggleDisabled(a)"
                >
                  <Icon :name="a.disabled ? 'eye-off' : 'eye'" :size="13" />
                </IconButton>
                <IconButton
                  title="重新发送邀请"
                  aria-label="重新发送邀请"
                  :disabled="reissuingId === a.id"
                  @click="openReissue(a)"
                >
                  <Icon name="refresh" :size="13" />
                </IconButton>
                <IconButton
                  title="吊销会话"
                  aria-label="吊销会话"
                  :disabled="revokingId === a.id"
                  @click="confirmRevokeSessions(a)"
                >
                  <Icon name="bolt" :size="13" />
                </IconButton>
                <IconButton
                  :active="panel.isActive(`account:${a.id}`)"
                  title="编辑"
                  aria-label="编辑"
                  @click="openEdit(a)"
                >
                  <Icon name="edit" :size="13" />
                </IconButton>
                <IconButton
                  v-if="a.id !== selfId"
                  variant="danger"
                  title="删除"
                  aria-label="删除"
                  @click="confirmDelete(a)"
                >
                  <Icon name="trash" :size="13" />
                </IconButton>
              </div>
            </Td>
          </Tr>
        </tbody>
      </DataTable>
    </DataCard>
    <StateText v-else>暂无用户，点击右上角按钮邀请用户</StateText>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useConfirm } from '@/composables/useConfirm'
import { useSidePanel } from '@/composables/useSidePanel'
import type { components } from '@/openapi-types'
import {
  deleteAccount,
  invalidateAccounts,
  listAccounts,
  reissueEnrollment,
  revokeAccountSessions,
  updateAccount,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import AccountForm from '@/components/AccountForm.vue'
import { Button, IconButton, DataCard, DataTable, Th, Td, Tr, StateText, Tag, Icon } from '@/ui'

type AccountView = components['schemas']['AccountView']

const panel = useSidePanel()
const confirm = useConfirm()
const queryClient = useQueryClient()

const accountsQuery = useQuery({
  queryKey: queryKeys.accounts.list,
  queryFn: listAccounts,
})
const accounts = computed(() => accountsQuery.data.value ?? [])
const loading = computed(() => accountsQuery.isLoading.value)
const count = computed(() => accounts.value.length)

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
    // Surface last-admin 409 as a brief alert; no crash
    if (e instanceof Error) alert(e.message)
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
    if (e instanceof Error) alert(e.message)
  } finally {
    reissuingId.value = null
  }
}

function confirmToggleDisabled(a: AccountView) {
  const willDisable = !a.disabled
  confirm.require({
    message: willDisable
      ? `确定要禁用账户「${a.displayName || a.username}」吗？该用户将无法登录。`
      : `确定要启用账户「${a.displayName || a.username}」吗？`,
    accept: async () => {
      await toggleMutation.mutateAsync(a)
    },
  })
}

async function revokeSessionsFor(a: AccountView) {
  revokingId.value = a.id
  try {
    const result = await revokeAccountSessions(a.id)
    // Brief feedback via browser alert — lightweight, no extra state needed
    alert(`已吊销 ${result.revoked} 个会话`)
    invalidateAccounts(queryClient)
  } catch (e: unknown) {
    if (e instanceof Error) alert(e.message)
  } finally {
    revokingId.value = null
  }
}

function confirmDelete(a: AccountView) {
  confirm.require({
    message: `确定要删除账户「${a.displayName || a.username}」吗？此操作不可撤销。`,
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
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">{{ count }} 个账户</span>
      <div class="flex items-center gap-2">
        <Button @click="openInvite">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>邀请用户</span>
        </Button>
      </div>
    </div>
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
                {{ a.role === 'admin' ? '管理员' : '标准' }}
              </Tag>
            </Td>
            <Td>
              <div class="flex flex-wrap gap-1">
                <Tag v-if="a.permissions.view_own_usage" variant="muted">用量</Tag>
                <Tag v-if="a.permissions.manage_own_api_keys" variant="muted">密钥</Tag>
                <Tag v-if="a.permissions.view_models" variant="muted">模型</Tag>
                <Tag v-if="a.permissions.view_own_traces" variant="muted">链路</Tag>
                <span
                  v-if="
                    !a.permissions.view_own_usage &&
                    !a.permissions.manage_own_api_keys &&
                    !a.permissions.view_models &&
                    !a.permissions.view_own_traces
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
    <StateText v-else>暂无账户，点击右上角按钮邀请用户</StateText>
  </div>
</template>

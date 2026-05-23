<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useSession, useSignOut } from '@/composables/useSession'
import { useConfirm } from '@/composables/useConfirm'
import { useSidePanel } from '@/composables/useSidePanel'
import {
  fetchMyCredentials,
  deleteMyCredential,
  renameMyCredential,
  invalidateOwnCredentials,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { Button, IconButton, Input, Badge, DataCard, DataTable, Th, Td, Tr, StateText, Icon } from '@/ui'
import AddPasskeyDialog from '@/components/AddPasskeyDialog.vue'
import type { components } from '@/openapi-types'

type CredentialView = components['schemas']['CredentialView']

const router = useRouter()
const qc = useQueryClient()
const session = useSession()
const signOut = useSignOut()
const confirm = useConfirm()
const sidePanel = useSidePanel()

const credentialsQuery = useQuery({
  queryKey: queryKeys.credentials.mine,
  queryFn: fetchMyCredentials,
})

function openAddDialog() {
  sidePanel.open(AddPasskeyDialog, {}, { key: 'add-passkey', width: '480px' })
}

const deleteMutation = useMutation({
  mutationFn: (id: number) => deleteMyCredential(id),
  onSuccess() {
    invalidateOwnCredentials(qc)
  },
})

// --- inline rename state ---
const editingId = ref<number | null>(null)
const editingValue = ref('')

const renameMutation = useMutation({
  mutationFn: ({ id, nickname }: { id: number; nickname: string | null }) =>
    renameMyCredential(id, nickname),
  onSuccess: () => {
    invalidateOwnCredentials(qc)
  },
})

function startEdit(c: CredentialView) {
  editingId.value = c.id
  editingValue.value = c.nickname ?? ''
}

function cancelEdit() {
  editingId.value = null
  editingValue.value = ''
}

async function saveEdit() {
  if (editingId.value === null) return
  const nickname = editingValue.value.trim() || null
  await renameMutation.mutateAsync({ id: editingId.value, nickname })
  cancelEdit()
}

function onEditKey(e: KeyboardEvent) {
  if (e.key === 'Enter') saveEdit()
  else if (e.key === 'Escape') cancelEdit()
}
// --- end inline rename state ---

const credentials = computed(() => credentialsQuery.data.value ?? [])
const credentialCount = computed(() => credentials.value.length)

const deleteDisabledReason = computed(() =>
  credentialCount.value === 1 ? '至少保留一把密钥' : undefined,
)

function onDelete(id: number) {
  confirm.require({
    message: '删除后此设备将无法用于登录。此操作不可撤销。',
    accept: async () => {
      await deleteMutation.mutateAsync(id)
    },
  })
}

async function onSignOut() {
  await signOut()
  router.replace('/login')
}

const permLabels: Record<string, string> = {
  view_own_usage: '查看自己的用量',
  manage_own_api_keys: '管理自己的 API Key',
  view_models: '查看模型',
  view_own_traces: '查看自己的链路',
  manage_own_projects: '管理自己的项目',
}

function fmtTime(iso?: string | null): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString('zh-CN')
}

function roleLabel(role: string): string {
  return role === 'admin' ? '管理员' : '标准用户'
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <!-- Loading state -->
    <StateText v-if="session.isPending.value">加载中…</StateText>

    <template v-else-if="session.user.value">
      <!-- Identity card -->
      <DataCard>
        <div class="p-6">
          <h2 class="text-sm font-semibold text-ink mb-4">身份</h2>
          <dl class="flex flex-col gap-3">
            <div class="flex items-baseline gap-3">
              <dt class="text-xs text-ink-faint w-24 shrink-0">用户名</dt>
              <dd class="text-sm text-ink font-medium">{{ session.user.value.username }}</dd>
            </div>
            <div class="flex items-baseline gap-3">
              <dt class="text-xs text-ink-faint w-24 shrink-0">显示名称</dt>
              <dd class="text-sm text-ink">{{ session.user.value.displayName }}</dd>
            </div>
            <div class="flex items-baseline gap-3">
              <dt class="text-xs text-ink-faint w-24 shrink-0">角色</dt>
              <dd class="text-sm text-ink">{{ roleLabel(session.user.value.role) }}</dd>
            </div>
          </dl>
        </div>
      </DataCard>

      <!-- Permissions card -->
      <DataCard>
        <div class="p-6">
          <h2 class="text-sm font-semibold text-ink mb-4">权限</h2>
          <ul class="flex flex-col gap-2.5">
            <li
              v-for="(label, perm) in permLabels"
              :key="perm"
              class="flex items-center gap-2.5"
            >
              <input
                type="checkbox"
                :checked="!!session.user.value.permissions[perm as keyof typeof session.user.value.permissions]"
                disabled
                class="rounded border-line disabled:opacity-70 accent-accent"
              />
              <span class="text-sm text-ink-muted">{{ label }}</span>
            </li>
          </ul>
        </div>
      </DataCard>

      <!-- Passkeys card -->
      <DataCard>
        <div>
          <!-- Card header -->
          <div class="px-6 pt-6 pb-4 flex items-center justify-between gap-4">
            <h2 class="text-sm font-semibold text-ink">Passkey</h2>
            <Button @click="openAddDialog">
              <Icon name="plus" :size="14" :stroke-width="2.2" />
              <span>添加 Passkey</span>
            </Button>
          </div>

          <!-- Credentials table -->
          <StateText v-if="credentialsQuery.isPending.value" class="px-6 pb-6">加载中…</StateText>
          <DataTable v-else>
            <thead>
              <tr>
                <Th>昵称</Th>
                <Th>后缀</Th>
                <Th>传输</Th>
                <Th>类型</Th>
                <Th>上次使用</Th>
                <Th>创建</Th>
                <Th actions />
              </tr>
            </thead>
            <tbody>
              <Tr v-for="c in credentials" :key="c.id">
                <Td>
                  <template v-if="editingId === c.id">
                    <div class="flex items-center gap-1">
                      <Input
                        v-model="editingValue"
                        size="sm"
                        maxlength="60"
                        placeholder="(无昵称)"
                        autofocus
                        @keydown="onEditKey"
                      />
                      <IconButton
                        title="保存"
                        :disabled="renameMutation.isPending.value"
                        @click="saveEdit"
                      >
                        <Icon name="check" :size="13" />
                      </IconButton>
                      <IconButton title="取消" @click="cancelEdit">
                        <Icon name="close" :size="13" />
                      </IconButton>
                    </div>
                  </template>
                  <template v-else>
                    {{ c.nickname ?? '—' }}
                  </template>
                </Td>
                <Td>
                  <code class="font-mono text-2xs text-ink-muted">{{ c.credentialIdSuffix }}</code>
                </Td>
                <Td>
                  <div class="flex flex-wrap gap-1">
                    <Badge v-for="t in c.transports" :key="t">{{ t }}</Badge>
                  </div>
                </Td>
                <Td>
                  <Badge>{{ c.backupState ? '已同步' : '设备绑定' }}</Badge>
                </Td>
                <Td>{{ fmtTime(c.lastUsedAt) }}</Td>
                <Td>{{ fmtTime(c.createdAt) }}</Td>
                <Td actions>
                  <div class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity">
                    <IconButton
                      v-if="editingId !== c.id"
                      title="重命名"
                      aria-label="重命名"
                      @click="startEdit(c)"
                    >
                      <Icon name="edit" :size="13" />
                    </IconButton>
                    <IconButton
                      variant="danger"
                      :title="deleteDisabledReason ?? '删除'"
                      aria-label="删除"
                      :disabled="credentialCount === 1 || deleteMutation.isPending.value || editingId === c.id"
                      @click="onDelete(c.id)"
                    >
                      <Icon name="trash" :size="13" />
                    </IconButton>
                  </div>
                </Td>
              </Tr>
            </tbody>
          </DataTable>
        </div>
      </DataCard>
    </template>

    <!-- Sign out -->
    <div class="flex justify-end">
      <Button variant="ghost" @click="onSignOut">退出登录</Button>
    </div>
  </div>
</template>

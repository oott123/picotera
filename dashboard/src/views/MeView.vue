<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useSession, useSignOut } from '@/composables/useSession'
import { useConfirm } from '@/composables/useConfirm'
import {
  fetchMyCredentials,
  addCredentialBegin,
  addCredentialComplete,
  deleteMyCredential,
  invalidateOwnCredentials,
  ApiRequestError,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { webauthnCreate, WebAuthnUserCancelled } from '@/api/webauthn'
import { Button, IconButton, Input, Badge, DataCard, DataTable, Th, Td, Tr, StateText, Icon } from '@/ui'

const router = useRouter()
const qc = useQueryClient()
const session = useSession()
const signOut = useSignOut()
const confirm = useConfirm()

const credentialsQuery = useQuery({
  queryKey: queryKeys.credentials.mine,
  queryFn: fetchMyCredentials,
})

const newNickname = ref('')
const errorMessage = ref<string | null>(null)

const addMutation = useMutation({
  mutationFn: async () => {
    const options = await addCredentialBegin()
    const attestation = await webauthnCreate(options as Parameters<typeof webauthnCreate>[0])
    return addCredentialComplete(attestation, newNickname.value.trim() || undefined)
  },
  onSuccess() {
    newNickname.value = ''
    errorMessage.value = null
    invalidateOwnCredentials(qc)
  },
  onError(err: unknown) {
    if (err instanceof WebAuthnUserCancelled) {
      errorMessage.value = '取消或超时'
      return
    }
    if (err instanceof ApiRequestError) {
      errorMessage.value = err.message
      return
    }
    errorMessage.value = '添加 Passkey 失败'
  },
})

const deleteMutation = useMutation({
  mutationFn: (id: number) => deleteMyCredential(id),
  onSuccess() {
    invalidateOwnCredentials(qc)
  },
})

const credentials = computed(() => credentialsQuery.data.value ?? [])
const credentialCount = computed(() => credentials.value.length)

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
                class="rounded border-line disabled:opacity-70 accent-blue-500"
              />
              <span class="text-sm text-ink-muted">{{ label }}</span>
            </li>
          </ul>
        </div>
      </DataCard>

      <!-- Passkeys card -->
      <DataCard>
        <div>
          <!-- Card header with add-passkey controls -->
          <div class="px-6 pt-6 pb-4 flex items-center justify-between gap-4">
            <h2 class="text-sm font-semibold text-ink">Passkey</h2>
            <div class="flex items-center gap-2">
              <Input
                v-model="newNickname"
                placeholder="昵称（可选）"
                class="w-36"
              />
              <Button
                :disabled="addMutation.isPending.value"
                @click="addMutation.mutate()"
              >
                <Icon name="plus" :size="14" :stroke-width="2.2" />
                <span>{{ addMutation.isPending.value ? '添加中…' : '添加' }}</span>
              </Button>
            </div>
          </div>

          <!-- Error message from add mutation -->
          <p v-if="errorMessage" class="px-6 pb-3 text-sm text-err">{{ errorMessage }}</p>

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
                <Td>{{ c.nickname ?? '—' }}</Td>
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
                      variant="danger"
                      :title="credentialCount === 1 ? '至少保留一把密钥' : '删除'"
                      aria-label="删除"
                      :disabled="credentialCount === 1 || deleteMutation.isPending.value"
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

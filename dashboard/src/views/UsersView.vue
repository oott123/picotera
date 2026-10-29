<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { useConfirm } from '@/composables/useConfirm'
import { useSidePanel } from '@/composables/useSidePanel'
import { useMe } from '@/composables/useMe'
import { useImpersonationStore } from '@/stores/impersonation'
import type { UserView } from '@/api'
import { deleteUser, invalidateUsers, listUsers, updateUser } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import UserForm from '@/components/UserForm.vue'
import UserIdentitiesPanel from '@/components/UserIdentitiesPanel.vue'
import { Button, IconButton, DataCard, DataTable, Th, Td, Tr, StateText, Tag, Icon } from '@/ui'

const panel = useSidePanel()
const confirm = useConfirm()
const queryClient = useQueryClient()
const router = useRouter()
const impersonation = useImpersonationStore()
const { me } = useMe()

const usersQuery = useQuery({
  queryKey: queryKeys.users.all,
  queryFn: listUsers,
})
const users = computed(() => usersQuery.data.value ?? [])
const loading = computed(() => usersQuery.isLoading.value)
const count = computed(() => users.value.length)

const updateUserMutation = useMutation({
  mutationFn: (u: UserView) =>
    updateUser(u.id, { displayName: u.displayName, isAdmin: u.isAdmin, disabled: !u.disabled }),
  onSuccess: () => invalidateUsers(queryClient),
})
const deleteUserMutation = useMutation({
  mutationFn: deleteUser,
  onSuccess: () => invalidateUsers(queryClient),
})

function editKey(id: number) {
  return `user:${id}:edit`
}
function identitiesKey(id: number) {
  return `user:${id}:identities`
}

function openCreate() {
  panel.open(UserForm, {}, { key: 'user:new' })
}

function openEdit(u: UserView) {
  panel.open(UserForm, { user: u }, { key: editKey(u.id) })
}

function toggleIdentities(u: UserView) {
  panel.toggle(
    UserIdentitiesPanel,
    { userId: u.id, userName: u.displayName },
    { key: identitiesKey(u.id) },
  )
}

async function toggleDisabled(u: UserView) {
  await updateUserMutation.mutateAsync(u)
}

async function impersonate(u: UserView) {
  impersonation.start({ id: u.id, displayName: u.displayName })
  // Leave admin pages the impersonated user can't access before me flips.
  await router.push({ name: 'overview' })
  queryClient.clear()
}

function confirmDelete(_event: Event, u: UserView) {
  confirm.require({
    message: `确定要删除用户「${u.displayName || u.id}」吗？其全部身份绑定将一并删除，此操作不可撤销。`,
    accept: async () => {
      await deleteUserMutation.mutateAsync(u.id)
      if (panel.isActive(editKey(u.id)) || panel.isActive(identitiesKey(u.id))) panel.close()
    },
  })
}

function rowSelected(id: number) {
  return panel.isActive(editKey(id)) || panel.isActive(identitiesKey(id))
}
</script>

<template>
  <div class="flex flex-col gap-3.5">
    <div class="flex items-center justify-between gap-3">
      <span class="text-xs text-ink-faint tabular-nums">{{ count }} 个用户</span>
      <div class="flex items-center gap-2">
        <Button @click="openCreate">
          <Icon name="plus" :size="14" :stroke-width="2.2" />
          <span>新增用户</span>
        </Button>
      </div>
    </div>
    <StateText v-if="loading">加载中…</StateText>
    <DataCard v-else-if="users.length">
      <DataTable>
        <thead>
          <tr>
            <Th>ID</Th>
            <Th>显示名</Th>
            <Th>角色</Th>
            <Th actions />
          </tr>
        </thead>
        <tbody>
          <Tr v-for="u in users" :key="u.id" :selected="rowSelected(u.id)" :dimmed="u.disabled">
            <Td
              ><span class="font-mono text-ink-faint">{{ u.id }}</span></Td
            >
            <Td>
              <span class="font-medium">{{ u.displayName }}</span>
              <Tag v-if="u.disabled" variant="muted" class="ml-1.5">已禁用</Tag>
            </Td>
            <Td>
              <Tag v-if="u.isAdmin" variant="accent">
                <Icon name="shield-check" :size="11" class="mr-0.5" />管理员
              </Tag>
              <span v-else class="text-ink-faint text-xs">普通用户</span>
            </Td>
            <Td actions>
              <div class="inline-flex gap-1 opacity-55 group-hover:opacity-100 transition-opacity">
                <IconButton
                  :title="u.disabled ? '启用用户' : '禁用用户'"
                  :aria-label="u.disabled ? '启用用户' : '禁用用户'"
                  @click="toggleDisabled(u)"
                >
                  <Icon :name="u.disabled ? 'puzzle-off' : 'puzzle'" :size="13" />
                </IconButton>
                <IconButton
                  :disabled="u.id === me?.id"
                  title="扮演此用户"
                  aria-label="扮演此用户"
                  @click="impersonate(u)"
                >
                  <Icon name="mask" :size="13" />
                </IconButton>
                <IconButton
                  :active="panel.isActive(identitiesKey(u.id))"
                  title="身份"
                  aria-label="身份"
                  :aria-pressed="panel.isActive(identitiesKey(u.id))"
                  @click="toggleIdentities(u)"
                >
                  <Icon name="link" :size="13" />
                </IconButton>
                <IconButton
                  :active="panel.isActive(editKey(u.id))"
                  title="编辑"
                  aria-label="编辑"
                  @click="openEdit(u)"
                >
                  <Icon name="edit" :size="13" />
                </IconButton>
                <IconButton
                  variant="danger"
                  title="删除"
                  aria-label="删除"
                  @click="(ev: Event) => confirmDelete(ev, u)"
                >
                  <Icon name="trash" :size="13" />
                </IconButton>
              </div>
            </Td>
          </Tr>
        </tbody>
      </DataTable>
    </DataCard>
    <StateText v-else>暂无用户，点击右上角按钮新增</StateText>
  </div>
</template>

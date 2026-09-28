<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { UserIdentityView } from '@/api'
import {
  createUserIdentity,
  deleteUserIdentity,
  invalidateUserIdentities,
  listUserIdentities,
  updateUserIdentity,
} from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { SidePanel, Button, IconButton, Input, Field, StateText, Icon } from '@/ui'

const props = defineProps<{ userId: number; userName: string }>()
const emit = defineEmits<{ close: [] }>()
const queryClient = useQueryClient()

const error = ref('')
const identitiesQuery = useQuery({
  queryKey: computed(() => queryKeys.users.identities(props.userId)),
  queryFn: () => listUserIdentities(props.userId),
})
const identities = computed<UserIdentityView[]>(() => identitiesQuery.data.value ?? [])
const loading = computed(() => identitiesQuery.isLoading.value)

const form = ref<{ provider: string; identity: string }>({ provider: '', identity: '' })
const saving = ref(false)

const editingId = ref<number | null>(null)
const editDraft = ref<{ provider: string; identity: string }>({ provider: '', identity: '' })

const createMutation = useMutation({
  mutationFn: (body: { provider: string; identity: string }) =>
    createUserIdentity(props.userId, body),
  onSuccess: () => invalidateUserIdentities(queryClient, props.userId),
})
const updateMutation = useMutation({
  mutationFn: (vars: { id: number; provider: string; identity: string }) =>
    updateUserIdentity(props.userId, vars.id, {
      provider: vars.provider,
      identity: vars.identity,
    }),
  onSuccess: () => invalidateUserIdentities(queryClient, props.userId),
})
const deleteMutation = useMutation({
  mutationFn: (id: number) => deleteUserIdentity(props.userId, id),
  onSuccess: () => invalidateUserIdentities(queryClient, props.userId),
})

watch(
  () => props.userId,
  () => {
    form.value.provider = ''
    form.value.identity = ''
    editingId.value = null
    error.value = ''
  },
)

async function addIdentity() {
  if (!form.value.provider || !form.value.identity) return
  saving.value = true
  error.value = ''
  try {
    await createMutation.mutateAsync({
      provider: form.value.provider,
      identity: form.value.identity,
    })
    form.value.provider = ''
    form.value.identity = ''
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '添加身份失败'
  } finally {
    saving.value = false
  }
}

function startEdit(i: UserIdentityView) {
  editingId.value = i.id
  editDraft.value = { provider: i.provider, identity: i.identity }
}

function cancelEdit() {
  editingId.value = null
}

function isEditDirty(i: UserIdentityView) {
  if (!editDraft.value.provider || !editDraft.value.identity) return false
  return editDraft.value.provider !== i.provider || editDraft.value.identity !== i.identity
}

async function saveEdit(i: UserIdentityView) {
  if (!editDraft.value.provider || !editDraft.value.identity) return
  if (!isEditDirty(i)) {
    editingId.value = null
    return
  }
  error.value = ''
  try {
    await updateMutation.mutateAsync({
      id: i.id,
      provider: editDraft.value.provider,
      identity: editDraft.value.identity,
    })
    editingId.value = null
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '更新身份失败'
  }
}

async function deleteIdentity(id: number) {
  error.value = ''
  try {
    await deleteMutation.mutateAsync(id)
    if (editingId.value === id) editingId.value = null
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : '删除身份失败'
  }
}

function onEditKeydown(e: KeyboardEvent, i: UserIdentityView) {
  if (e.key === 'Enter') {
    e.preventDefault()
    saveEdit(i)
  } else if (e.key === 'Escape') {
    e.preventDefault()
    cancelEdit()
  }
}
</script>

<template>
  <SidePanel :title="userName" kicker="身份绑定" @close="emit('close')">
    <section class="flex flex-col gap-2">
      <div class="flex items-baseline justify-between">
        <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]">已绑定</span>
        <span class="text-xs text-ink-faint tabular-nums">{{ identities.length }}</span>
      </div>
      <StateText v-if="loading" :dashed="false" compact>加载中…</StateText>
      <StateText v-else-if="!identities.length" compact>暂无身份，下方添加</StateText>
      <ul v-else class="list-none m-0 p-0 flex flex-col gap-2">
        <li
          v-for="i in identities"
          :key="i.id"
          class="px-2.5 py-2 border border-line rounded-md bg-surface-0"
        >
          <div class="flex items-center gap-2 min-w-0">
            <span
              class="flex-1 min-w-0 text-sm font-semibold text-ink truncate"
              :title="i.provider"
            >
              {{ i.provider }}
            </span>
            <div class="flex items-center gap-1 shrink-0">
              <template v-if="editingId === i.id">
                <IconButton
                  size="sm"
                  title="保存修改"
                  :aria-label="`保存身份 ${i.provider}`"
                  :disabled="!isEditDirty(i)"
                  @click="saveEdit(i)"
                >
                  <Icon name="check" :size="13" />
                </IconButton>
                <IconButton
                  size="sm"
                  title="取消编辑"
                  :aria-label="`取消编辑身份 ${i.provider}`"
                  @click="cancelEdit"
                >
                  <Icon name="close" :size="13" />
                </IconButton>
              </template>
              <template v-else>
                <IconButton
                  size="sm"
                  title="编辑身份"
                  :aria-label="`编辑身份 ${i.provider}`"
                  @click="startEdit(i)"
                >
                  <Icon name="edit" :size="13" />
                </IconButton>
                <IconButton
                  size="sm"
                  variant="danger"
                  title="删除身份"
                  :aria-label="`删除身份 ${i.provider}`"
                  @click="deleteIdentity(i.id)"
                >
                  <Icon name="trash" :size="13" />
                </IconButton>
              </template>
            </div>
          </div>

          <template v-if="editingId !== i.id">
            <div class="font-mono text-xs text-ink-muted truncate mt-1" :title="i.identity">
              {{ i.identity }}
            </div>
          </template>

          <template v-else>
            <div class="flex flex-col gap-2 mt-2">
              <Field label="Provider">
                <Input
                  v-model="editDraft.provider"
                  size="sm"
                  placeholder="例如 http-header"
                  autofocus
                  @keydown="onEditKeydown($event, i)"
                />
              </Field>
              <Field label="Identity">
                <Input
                  v-model="editDraft.identity"
                  size="sm"
                  placeholder="该 provider 下的唯一标识"
                  @keydown="onEditKeydown($event, i)"
                />
              </Field>
            </div>
          </template>
        </li>
      </ul>
    </section>

    <section class="flex flex-col gap-2">
      <div class="flex items-baseline justify-between">
        <span class="text-xs font-medium text-ink-muted uppercase tracking-[0.03em]">新增身份</span>
      </div>
      <form class="flex flex-col gap-2" @submit.prevent="addIdentity">
        <Field label="Provider">
          <Input v-model="form.provider" size="sm" placeholder="例如 http-header" />
        </Field>
        <Field label="Identity">
          <Input v-model="form.identity" size="sm" placeholder="该 provider 下的唯一标识" />
        </Field>
        <div class="flex justify-end">
          <Button type="submit" size="sm" :disabled="saving || !form.provider || !form.identity">
            {{ saving ? '添加中…' : '添加' }}
          </Button>
        </div>
      </form>
    </section>

    <template v-if="error" #error>{{ error }}</template>
  </SidePanel>
</template>

<script setup lang="ts">
import { useQueryClient } from '@tanstack/vue-query'
import { SidePanel } from '@/ui'
import {
  addCredentialBegin,
  addCredentialComplete,
  renameMyCredential,
  invalidateOwnCredentials,
} from '@/api/client'
import PasskeyCeremonyFlow from '@/components/PasskeyCeremonyFlow.vue'

const emit = defineEmits<{ close: [] }>()
const qc = useQueryClient()

function onDone() {
  // Refresh the credentials table so the new row appears with its nickname.
  invalidateOwnCredentials(qc)
  emit('close')
}

function onCancel() {
  // Even if the user cancels mid-flow, refresh — a credential may have been
  // created if they got past the ceremony but never clicked 完成.
  invalidateOwnCredentials(qc)
  emit('close')
}
</script>

<template>
  <SidePanel title="添加 Passkey" kicker="Passkey" @close="onCancel">
    <PasskeyCeremonyFlow
      :begin="addCredentialBegin"
      :complete="addCredentialComplete"
      :rename="renameMyCredential"
      :extract-credential-id="(r) => r.id"
      @done="onDone"
      @close="onCancel"
    />
  </SidePanel>
</template>

<script setup lang="ts">
import type { ProviderLabel } from '@/api'
import { SidePanel } from '@/ui'
import RequestDetailsContent from './RequestDetailsContent.vue'

defineProps<{ requestId: string; providers?: ProviderLabel[] }>()
const emit = defineEmits<{
  close: []
  selectedRequest: [requestId: string]
}>()
</script>

<template>
  <SidePanel title="请求详情" :kicker="requestId" @close="emit('close')">
    <RequestDetailsContent
      :request-id="requestId"
      :providers="providers"
      @selected-request="emit('selectedRequest', $event)"
    />
  </SidePanel>
</template>

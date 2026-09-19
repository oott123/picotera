<script setup lang="ts">
import { computed } from 'vue'
import { useArtifact } from '@/composables/useArtifact'
import {
  hasConversationMessages,
  parseRequestConversation,
  parseResponseConversation,
} from '@/composables/conversation'
import { isJsonContentType, parseJsonBody } from './artifactBody'
import type { ArtifactPayload } from './artifactTypes'
import { StateText } from '@/ui'
import ConversationView from './ConversationView.vue'

const props = defineProps<{ requestUrl?: string; responseUrl?: string }>()

const reqQuery = useArtifact(() => props.requestUrl)
const resQuery = useArtifact(() => props.responseUrl)

function requestMessages(payload: ArtifactPayload | undefined) {
  if (!payload || payload.bodyEncoding === 'base64' || !isJsonContentType(payload.headers))
    return null
  const parsed = parseJsonBody(payload.body, payload.bodyEncoding)
  if (!parsed.ok) return null
  const messages = parseRequestConversation(parsed.value)
  return hasConversationMessages(messages) ? messages : null
}

function responseMessages(payload: ArtifactPayload | undefined) {
  if (!payload) return null
  if (payload.aggregated?.body !== undefined && !payload.aggregated.error) {
    const messages = parseResponseConversation(payload.aggregated.body, payload.aggregated.format)
    return hasConversationMessages(messages) ? messages : null
  }
  if (payload.bodyEncoding === 'base64' || !isJsonContentType(payload.headers)) return null
  const parsed = parseJsonBody(payload.body, payload.bodyEncoding)
  if (!parsed.ok) return null
  const messages = parseResponseConversation(parsed.value)
  return hasConversationMessages(messages) ? messages : null
}

const requestConversation = computed(() => requestMessages(reqQuery.data.value))
const responseConversation = computed(() => responseMessages(resQuery.data.value))
const loading = computed(() => reqQuery.isLoading.value || resQuery.isLoading.value)
const merged = computed(() => [
  ...(requestConversation.value ?? []),
  ...(responseConversation.value ?? []),
])
const unparsable = computed(
  () => !loading.value && requestConversation.value === null && responseConversation.value === null,
)
// At least one side has no recorded body — e.g. the OTR body modes strip them
// before upload — so there is nothing to build a conversation from.
const unparsableText = computed(() =>
  !reqQuery.data.value?.body || !resQuery.data.value?.body
    ? '没有请求体和/或响应体'
    : '无法解析为对话，请查看原始请求 / 原始响应',
)
</script>

<template>
  <StateText v-if="loading" :dashed="false" compact>加载中…</StateText>
  <StateText v-else-if="unparsable" :dashed="false" compact>{{ unparsableText }}</StateText>
  <ConversationView v-else :messages="merged" />
</template>

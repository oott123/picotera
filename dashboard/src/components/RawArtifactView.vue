<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { StateText, DataTable, Th, Td, Tr, Field, SegmentedControl, Button, Icon } from '@/ui'
import { isJsonContentType, parseJsonBody, rawBodyText, buildCurlCommand } from './artifactBody'
import JsonArtifactViewer from './JsonArtifactViewer.vue'
import ResponseArtifactView, { type SubView } from './ResponseArtifactView.vue'
import { useArtifact } from '@/composables/useArtifact'

const props = defineProps<{ url?: string; kind: 'request' | 'response'; requestId?: string }>()

const bodyView = defineModel<'raw' | 'json' | SubView>('bodyView', { required: true })
const headersOpen = defineModel<boolean>('headersOpen', { required: true })
const thinkingOpen = defineModel<boolean>('thinkingOpen', { default: false })
const artifactQuery = useArtifact(() => props.url)
const payload = computed(() => artifactQuery.data.value ?? null)
const loading = computed(() => artifactQuery.isLoading.value)
const error = computed(() => artifactQuery.error.value?.message ?? '')

const requestJsonBody = computed(() => {
  if (
    !payload.value ||
    payload.value.bodyEncoding === 'base64' ||
    !isJsonContentType(payload.value.headers)
  ) {
    return { ok: false, value: null, error: '' }
  }
  return parseJsonBody(payload.value.body, payload.value.bodyEncoding)
})

// The OTR body modes strip bodies before upload, which lands as an empty
// string — indistinguishable from a payload that never carried one.
const hasBody = computed(() => !!payload.value?.body)

const requestBodyOptions = computed(() => {
  if (!requestJsonBody.value.ok) return [{ value: 'raw', label: 'Raw' }]
  return [
    { value: 'raw', label: 'Raw' },
    { value: 'json', label: 'JSON' },
  ]
})

const bodyViewForControl = computed({
  get: () => bodyView.value as 'raw' | 'json',
  set: (v) => {
    bodyView.value = v
  },
})

function headerEntries(headers: Record<string, string[]> | undefined) {
  if (!headers) return []
  return Object.entries(headers).map(([k, v]) => ({ key: k, value: v.join(', ') }))
}

function bodyDisplay(body: string | undefined, encoding: string | undefined) {
  return rawBodyText(body, encoding)
}

watch(requestBodyOptions, (opts) => {
  if (props.kind === 'request' && !opts.some((o) => o.value === bodyView.value)) {
    bodyView.value = opts[0]?.value as 'raw' | 'json'
  }
})

const curlCopied = ref(false)
let curlCopyTimer: ReturnType<typeof setTimeout> | null = null
async function copyAsCurl() {
  if (!payload.value || !payload.value.url) return
  const cmd = buildCurlCommand(payload.value)
  if (!cmd) return
  try {
    await navigator.clipboard.writeText(cmd)
    curlCopied.value = true
    if (curlCopyTimer) clearTimeout(curlCopyTimer)
    curlCopyTimer = setTimeout(() => {
      curlCopied.value = false
    }, 1500)
  } catch {
    // clipboard unavailable — silently ignore
  }
}
</script>

<template>
  <StateText v-if="!url" :dashed="false" compact>未启用 artifact 记录</StateText>
  <StateText v-else-if="loading" :dashed="false" compact>加载中…</StateText>
  <StateText v-else-if="error" :dashed="false" compact>{{ error }}</StateText>
  <template v-else-if="payload">
    <template v-if="kind === 'request'">
      <div class="relative flex flex-col gap-3">
        <Button variant="ghost" size="sm" class="absolute top-0 right-0 z-10" @click="copyAsCurl">
          <Icon :name="curlCopied ? 'check' : 'copy'" :size="13" />
          <span>{{ curlCopied ? '已复制' : '复制为 cURL' }}</span>
        </Button>
        <div class="grid grid-cols-2 gap-2.5">
          <Field v-if="payload.method" label="Method" as="div">
            <span class="font-mono text-sm">{{ payload.method }}</span>
          </Field>
          <Field v-if="payload.url" label="URL" as="div" class="col-span-2">
            <span class="font-mono text-xs break-all">{{ payload.url }}</span>
          </Field>
        </div>

        <details
          :open="headersOpen"
          @toggle="headersOpen = ($event.currentTarget as HTMLDetailsElement).open"
          class="group flex flex-col gap-2"
        >
          <summary
            class="flex items-center gap-1.5 cursor-pointer select-none list-none text-2xs font-medium text-ink-muted uppercase tracking-[0.04em] hover:text-ink"
          >
            Headers
            <span
              v-if="headerEntries(payload.headers).length"
              class="text-ink-faint normal-case tracking-normal"
              >({{ headerEntries(payload.headers).length }})</span
            >
            <svg
              class="w-3 h-3 transition-transform group-open:rotate-90"
              viewBox="0 0 16 16"
              fill="currentColor"
            >
              <path d="M6 3.5l5 4.5-5 4.5V3.5z" />
            </svg>
          </summary>
          <div class="mt-2">
            <div v-if="!headerEntries(payload.headers).length" class="text-xs text-ink-faint">
              —
            </div>
            <DataTable v-else>
              <thead>
                <Tr>
                  <Th class="w-44">Header</Th>
                  <Th>Value</Th>
                </Tr>
              </thead>
              <tbody>
                <Tr v-for="h in headerEntries(payload.headers)" :key="h.key">
                  <Td class="font-mono text-2xs whitespace-nowrap">{{ h.key }}</Td>
                  <Td class="font-mono text-2xs break-all">{{ h.value }}</Td>
                </Tr>
              </tbody>
            </DataTable>
          </div>
        </details>

        <section class="flex flex-col gap-2">
          <div class="flex items-center justify-between gap-3">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
              >Body</span
            >
            <SegmentedControl
              v-if="payload.bodyEncoding !== 'base64' && requestBodyOptions.length > 1"
              v-model="bodyViewForControl"
              :options="requestBodyOptions"
            />
          </div>
          <div
            v-if="payload.bodyEncoding === 'base64'"
            class="flex items-center gap-3 text-xs text-ink-faint"
          >
            <span>[binary, {{ payload.body?.length ?? 0 }} bytes]</span>
            <a :href="url" download class="text-accent-ink underline hover:no-underline"
              >下载原始数据</a
            >
          </div>
          <StateText v-else-if="!hasBody" :dashed="false" compact>请求体不存在或未记录</StateText>
          <template v-else-if="bodyView === 'json' && requestJsonBody.ok">
            <JsonArtifactViewer :value="requestJsonBody.value" />
          </template>
          <StateText
            v-else-if="isJsonContentType(payload.headers) && !requestJsonBody.ok"
            :dashed="false"
            compact
          >
            {{ requestJsonBody.error }}
          </StateText>
          <template v-if="bodyView === 'raw' && hasBody">
            <pre
              class="font-mono text-xs whitespace-pre-wrap break-all bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink overflow-auto max-h-[480px]"
              >{{ bodyDisplay(payload.body, payload.bodyEncoding) }}</pre
            >
          </template>
        </section>
      </div>
    </template>
    <ResponseArtifactView
      v-else
      v-model:sub-view="bodyView"
      v-model:headers-open="headersOpen"
      v-model:thinking-open="thinkingOpen"
      :payload="payload"
      :url="url"
      :request-id="requestId"
    />
  </template>
</template>

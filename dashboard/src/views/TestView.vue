<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import {
  Badge,
  Button,
  CodeEditor,
  ComboBox,
  type ComboBoxOption,
  Field,
  Icon,
  Input,
  SegmentedControl,
  Select,
  Textarea,
} from '@/ui'
import {
  listApiKeys,
  listEndpointLabels,
  listModelLabels,
  listProviderEndpoints,
  listProviders,
  postGatewayTest,
  postTestDirect,
} from '@/api/client'
import AnnotationsEditor from '@/components/AnnotationsEditor.vue'
import { queryKeys } from '@/api/queryKeys'
import { useMe } from '@/composables/useMe'
import {
  buildTestBody,
  endpointTypeToFormat,
  isGeminiFormat,
  type TestFields,
  type TestFormat,
} from '@/lib/testBody'
import { aggregateResponse } from '@/lib/testStream'
import { renderMarkdown } from '@/composables/useSSEParser'

type Mode = 'direct' | 'gateway'
type GatewayTargetKind = 'unified' | 'path'

const { isAdmin } = useMe()

// Short-circuit (direct) test is an admin-only capability — it exposes provider
// credentials and routing internals. Non-admins only see the gateway test.
const modeOptions = computed(() =>
  isAdmin.value
    ? [
        { value: 'direct', label: '短路测试' },
        { value: 'gateway', label: '网关测试' },
      ]
    : [{ value: 'gateway', label: '网关测试' }],
)

const UNIFIED_FORMATS: { value: TestFormat; label: string }[] = [
  { value: 'anthropicMessages', label: 'Anthropic Messages' },
  { value: 'openaiChatCompletions', label: 'OpenAI Chat Completions' },
  { value: 'openaiResponses', label: 'OpenAI Responses' },
  { value: 'geminiGenerateContent', label: 'Gemini generateContent' },
  { value: 'geminiStreamGenerateContent', label: 'Gemini streamGenerateContent' },
]

const gatewayTargetOptions = [
  { value: 'unified', label: '统一路由' },
  { value: 'path', label: '配置端点' },
]

const streamOptions = [
  { value: 'stream', label: '流式' },
  { value: 'once', label: '非流式' },
]

// --- shared structured form state ---
const mode = ref<Mode>('direct')
const model = ref('')
const system = ref('You are a helpful assistant.')
const maxTokens = ref(32768)
const userMessage = ref('Hello!')
const streamMode = ref<'stream' | 'once'>('stream')

// --- direct mode selection ---
const directProviderId = ref(0)
const directEndpointPath = ref('')

// --- gateway mode selection ---
const gatewayApiKeyId = ref(0)
const gatewayTargetKind = ref<GatewayTargetKind>('unified')
const gatewayUnifiedFormat = ref<TestFormat>('anthropicMessages')
const gatewayEndpointPath = ref('')

// OTR override sent as X-PicoTera-OTR; '' means follow the user's setting (no header).
type OtrOverride = '' | 'none' | 'body' | 'body-and-message'
const otrOverride = ref<OtrOverride>('')
const otrOptions: { value: OtrOverride; label: string }[] = [
  { value: '', label: '跟随设置' },
  { value: 'none', label: '完整记录' },
  { value: 'body', label: '不记录内容' },
  { value: 'body-and-message', label: '不记录内容和梗概' },
]

// --- custom request headers (advanced) ---
// Generated from the structured form above (the Anthropic version header for
// Anthropic targets, plus the OTR option in gateway mode) and kept in sync with
// the editable map unless the user takes over, mirroring rawBody. Applies to
// both modes — direct test forwards these headers too.
const customHeaders = ref<Record<string, string>>({})
const headersManualOverride = ref(false)

const generatedHeaders = computed<Record<string, string>>(() => {
  const h: Record<string, string> = {}
  if (baseFormat.value === 'anthropicMessages') h['anthropic-version'] = '2023-06-01'
  if (mode.value === 'gateway' && otrOverride.value) h['X-PicoTera-OTR'] = otrOverride.value
  return h
})

const providerOptions = computed(() => [
  { value: 0, label: '请选择渠道', disabled: true },
  ...providers.value.map((p) => ({
    value: p.id,
    label: p.name,
    hint: `#${p.id}${p.disabled ? ' · 已禁用' : ''}`,
  })),
])

const directEndpointOptions = computed(() => [
  { value: '', label: '请选择端点', disabled: true },
  ...providerEndpoints.value.map((pe) => ({
    value: pe.endpointPath,
    label: pe.endpointPath,
    hint: pe.upstreamUrl,
  })),
])

const apiKeyOptions = computed(() => [
  { value: 0, label: '请选择 API Key', disabled: true },
  ...apiKeys.value.map((k) => ({
    value: k.id,
    label: k.name,
    hint: `#${k.id}${k.disabled ? ' · 已禁用' : ''}`,
  })),
])

const endpointPathOptions = computed(() => [
  { value: '', label: '请选择端点', disabled: true },
  ...endpoints.value
    // Unified routes have their own dedicated target kind above; the label API
    // lists them too, so drop them here to avoid duplicate entries.
    .filter((e) => !e.path.startsWith('/api/unified/'))
    .map((e) => ({
      value: e.path,
      label: e.name || e.path,
      hint: e.name ? e.path : undefined,
    })),
])

// path variables (keyed by placeholder name) for whichever target carries {name} tokens
const pathVars = ref<Record<string, string>>({})

// --- advanced raw body ---
const rawBody = ref('')
const manualOverride = ref(false)
const bodyError = ref('')

// --- queries ---
// Direct mode needs full provider data (providerModels, credentials-bearing
// provider rows) and provider-endpoint bindings, both admin-only. They load only
// for an admin in direct mode so a non-admin never triggers a 403. The gateway
// mode relies solely on the open label/api-key endpoints.
const directEnabled = computed(() => isAdmin.value && mode.value === 'direct')
const providersQuery = useQuery({
  queryKey: queryKeys.providers.all,
  queryFn: listProviders,
  enabled: directEnabled,
})
const endpointsQuery = useQuery({
  queryKey: queryKeys.labels.endpoints,
  queryFn: listEndpointLabels,
})
const apiKeysQuery = useQuery({ queryKey: queryKeys.apiKeys.all, queryFn: listApiKeys })
const modelsQuery = useQuery({ queryKey: queryKeys.labels.models, queryFn: listModelLabels })

const providerEndpointsQuery = useQuery({
  queryKey: computed(() =>
    queryKeys.providerEndpoints.list({ providerId: directProviderId.value }),
  ),
  queryFn: () => listProviderEndpoints(directProviderId.value),
  enabled: computed(() => directEnabled.value && directProviderId.value !== 0),
})

const providers = computed(() => providersQuery.data.value ?? [])
const endpoints = computed(() => endpointsQuery.data.value ?? [])
const apiKeys = computed(() => apiKeysQuery.data.value ?? [])
const providerEndpoints = computed(() => providerEndpointsQuery.data.value ?? [])
const models = computed(() => modelsQuery.data.value ?? [])

// --- model selection (ComboBox) ---
const selectedProvider = computed(() =>
  providers.value.find((p) => p.id === directProviderId.value),
)

const modelOptions = computed<ComboBoxOption[]>(() => {
  if (mode.value === 'direct') {
    const entries = selectedProvider.value?.providerModels ?? []
    // Direct mode bypasses routing, so the upstream sees the real upstream model
    // name when one is configured; the route name stays as the display label.
    return entries.map((entry) => {
      const hasUpstream = !!entry.upstreamModelName && entry.upstreamModelName !== entry.model
      return {
        value: hasUpstream ? entry.upstreamModelName! : entry.model,
        label: entry.model,
        hint: hasUpstream ? entry.upstreamModelName : undefined,
      }
    })
  }
  return models.value.map((m) => ({ value: m.name }))
})

const modelAllowCustom = computed(() => mode.value === 'direct')

// path -> endpointType map, used to infer format for direct + gateway-path targets
const endpointTypeByPath = computed(() => {
  const m = new Map<string, ReturnType<typeof endpointTypeToFormat>>()
  for (const e of endpoints.value) m.set(e.path, endpointTypeToFormat(e.endpointType))
  return m
})

// --- format resolution ---
const baseFormat = computed<TestFormat | null>(() => {
  if (mode.value === 'direct') {
    if (!directEndpointPath.value) return null
    return endpointTypeByPath.value.get(directEndpointPath.value) ?? null
  }
  if (gatewayTargetKind.value === 'unified') return gatewayUnifiedFormat.value
  if (!gatewayEndpointPath.value) return null
  return endpointTypeByPath.value.get(gatewayEndpointPath.value) ?? null
})

// For Gemini, streaming is intrinsic to the format; the stream toggle is locked.
const streamLocked = computed(() => baseFormat.value !== null && isGeminiFormat(baseFormat.value))
const effectiveStream = computed(() => {
  if (baseFormat.value === 'geminiStreamGenerateContent') return true
  if (baseFormat.value === 'geminiGenerateContent') return false
  return streamMode.value === 'stream'
})

const fields = computed<TestFields>(() => ({
  model: model.value.trim(),
  system: system.value,
  maxTokens: maxTokens.value,
  userMessage: userMessage.value,
  stream: effectiveStream.value,
}))

const generatedBody = computed<Record<string, unknown> | null>(() => {
  if (!baseFormat.value) return null
  return buildTestBody(baseFormat.value, fields.value)
})

// Keep the raw editor in sync with generated fields unless the user took over.
watch(
  generatedBody,
  (body) => {
    if (manualOverride.value) return
    if (baseFormat.value === null) return
    rawBody.value = body ? JSON.stringify(body, null, 2) : ''
  },
  { immediate: true, deep: true },
)

function onRawBodyInput(v: string) {
  rawBody.value = v
  manualOverride.value = true
  bodyError.value = ''
}

function rebuildBody() {
  manualOverride.value = false
  bodyError.value = ''
  rawBody.value = generatedBody.value ? JSON.stringify(generatedBody.value, null, 2) : ''
}

// Keep the headers editor in sync with generated headers unless the user took over.
watch(
  generatedHeaders,
  (h) => {
    if (headersManualOverride.value) return
    customHeaders.value = { ...h }
  },
  { immediate: true, deep: true },
)

function onHeadersInput(v: Record<string, string>) {
  customHeaders.value = v
  headersManualOverride.value = true
}

function rebuildHeaders() {
  headersManualOverride.value = false
  customHeaders.value = { ...generatedHeaders.value }
}

// --- path variable placeholders ---
const activePath = computed(() => {
  if (mode.value === 'direct') return directEndpointPath.value
  if (gatewayTargetKind.value === 'path') return gatewayEndpointPath.value
  if (isGeminiFormat(gatewayUnifiedFormat.value))
    return unifiedRoutePath(gatewayUnifiedFormat.value)
  return ''
})

const pathVarNames = computed(() => {
  const names: string[] = []
  const re = /\{([A-Za-z_][A-Za-z0-9_]*)\}/g
  let m: RegExpExecArray | null
  while ((m = re.exec(activePath.value)) !== null) {
    if (m[1]) names.push(m[1])
  }
  return names
})

function pathVarValue(name: string): string {
  // `model` defaults to the model field unless explicitly overridden.
  if (pathVars.value[name] !== undefined) return pathVars.value[name]
  if (name === 'model' && hasSupportedFormat.value) return model.value.trim()
  return ''
}

function setPathVar(name: string, value: string) {
  pathVars.value = { ...pathVars.value, [name]: value }
}

function unifiedRoutePath(format: TestFormat): string {
  switch (format) {
    case 'anthropicMessages':
      return '/api/unified/v1/messages'
    case 'openaiResponses':
      return '/api/unified/v1/responses'
    case 'openaiChatCompletions':
      return '/api/unified/v1/chat/completions'
    case 'geminiGenerateContent':
      return '/api/unified/v1beta/models/{model}:generateContent'
    case 'geminiStreamGenerateContent':
      return '/api/unified/v1beta/models/{model}:streamGenerateContent'
  }
}

function substitutePath(path: string): string {
  return path.replace(/\{([A-Za-z_][A-Za-z0-9_]*)\}/g, (_, name: string) =>
    encodeURIComponent(pathVarValue(name)),
  )
}

const selectedApiKey = computed(() => apiKeys.value.find((k) => k.id === gatewayApiKeyId.value))

// --- submission validity ---
const hasSupportedFormat = computed(() => baseFormat.value !== null)
const formatUnsupported = computed(() => activeSelectionMade.value && !hasSupportedFormat.value)

const activeSelectionMade = computed(() => {
  if (mode.value === 'direct') return !!directEndpointPath.value
  if (gatewayTargetKind.value === 'path') return !!gatewayEndpointPath.value
  return true
})

const canSubmit = computed(() => {
  if (sending.value) return false
  if (!activeSelectionMade.value) return false
  if (mode.value === 'direct') {
    if (directProviderId.value === 0) return false
    if (!directEndpointPath.value) return false
  } else {
    if (gatewayApiKeyId.value === 0) return false
    if (gatewayTargetKind.value === 'path' && !gatewayEndpointPath.value) return false
  }
  if (hasSupportedFormat.value) {
    if (!model.value.trim()) return false
  } else {
    if (!rawBody.value.trim()) return false
  }
  return true
})

// --- response state ---
const sending = ref(false)
const statusCode = ref<number | null>(null)
const elapsedMs = ref<number | null>(null)
const ttftMs = ref<number | null>(null)
const rawResponse = ref('')
const responseContentType = ref('')
const sendError = ref('')
let controller: AbortController | null = null

const responseIsError = computed(() => statusCode.value !== null && statusCode.value >= 400)

const aggregated = computed(() => {
  if (!baseFormat.value || !rawResponse.value) return { thinking: '', reply: '' }
  return aggregateResponse(baseFormat.value, rawResponse.value, effectiveStream.value)
})

const replyHtml = computed(() =>
  aggregated.value.reply ? renderMarkdown(aggregated.value.reply) : '',
)

function resetResponse() {
  statusCode.value = null
  elapsedMs.value = null
  ttftMs.value = null
  rawResponse.value = ''
  responseContentType.value = ''
  sendError.value = ''
}

function effectivePathVars(): Record<string, string> | undefined {
  if (pathVarNames.value.length === 0) return undefined
  const used: Record<string, string> = {}
  for (const name of pathVarNames.value) {
    const v = pathVarValue(name)
    if (v) used[name] = v
  }
  return Object.keys(used).length > 0 ? used : undefined
}

function resolveBody(): unknown | null {
  if (!hasSupportedFormat.value || manualOverride.value) {
    try {
      return JSON.parse(rawBody.value)
    } catch (e) {
      bodyError.value = e instanceof Error ? e.message : '请求体不是合法 JSON'
      return null
    }
  }
  return generatedBody.value
}

async function send() {
  bodyError.value = ''
  const body = resolveBody()
  if (body === null) return

  resetResponse()
  sending.value = true
  controller = new AbortController()
  const startedAt = performance.now()

  try {
    let res: Response
    if (mode.value === 'direct') {
      res = await postTestDirect(
        {
          providerId: directProviderId.value!,
          endpointPath: directEndpointPath.value,
          stream: effectiveStream.value,
          pathVars: effectivePathVars(),
          headers: customHeaders.value,
          body,
        },
        controller.signal,
      )
    } else {
      const targetPath =
        gatewayTargetKind.value === 'unified'
          ? unifiedRoutePath(gatewayUnifiedFormat.value)
          : gatewayEndpointPath.value
      res = await postGatewayTest(
        substitutePath(targetPath),
        selectedApiKey.value!.key,
        body,
        customHeaders.value,
        controller.signal,
      )
    }

    statusCode.value = res.status
    responseContentType.value = res.headers.get('Content-Type') ?? ''

    const reader = res.body?.getReader()
    if (!reader) {
      rawResponse.value = await res.text()
    } else {
      const decoder = new TextDecoder()
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        if (ttftMs.value === null) ttftMs.value = Math.round(performance.now() - startedAt)
        rawResponse.value += decoder.decode(value, { stream: true })
      }
      rawResponse.value += decoder.decode()
    }
    elapsedMs.value = Math.round(performance.now() - startedAt)
  } catch (e) {
    if ((e as Error)?.name === 'AbortError') {
      sendError.value = '已停止'
    } else {
      sendError.value = e instanceof Error ? e.message : '请求失败'
    }
    elapsedMs.value = Math.round(performance.now() - startedAt)
  } finally {
    sending.value = false
    controller = null
  }
}

function stop() {
  controller?.abort()
}

onBeforeUnmount(() => {
  controller?.abort()
})

// Reset dependent selections when context changes.
watch(directProviderId, () => {
  directEndpointPath.value = ''
})
watch(mode, () => {
  resetResponse()
})
// Non-admins cannot use direct mode; force gateway once permission resolves.
watch(
  isAdmin,
  (admin) => {
    if (!admin && mode.value === 'direct') mode.value = 'gateway'
  },
  { immediate: true },
)
</script>

<template>
  <div class="grid grid-cols-1 lg:grid-cols-[minmax(0,420px)_minmax(0,1fr)] gap-5 items-start">
    <!-- left: form -->
    <form class="flex flex-col gap-4" @submit.prevent="send">
      <Field label="测试模式" as="div">
        <SegmentedControl v-model="mode" :options="modeOptions" />
      </Field>

      <!-- direct mode targets -->
      <template v-if="mode === 'direct'">
        <Field label="渠道">
          <Select v-model="directProviderId" :options="providerOptions" />
        </Field>

        <Field label="端点绑定">
          <Select
            v-model="directEndpointPath"
            :options="directEndpointOptions"
            :disabled="directProviderId === 0"
          />
        </Field>
      </template>

      <!-- gateway mode targets -->
      <template v-else>
        <Field label="API Key">
          <Select v-model="gatewayApiKeyId" :options="apiKeyOptions" />
        </Field>

        <Field label="目标类型" as="div">
          <SegmentedControl v-model="gatewayTargetKind" :options="gatewayTargetOptions" />
        </Field>

        <Field v-if="gatewayTargetKind === 'unified'" label="统一路由格式">
          <Select v-model="gatewayUnifiedFormat" :options="UNIFIED_FORMATS" />
        </Field>
        <Field v-else label="端点路径">
          <Select v-model="gatewayEndpointPath" :options="endpointPathOptions" />
        </Field>

        <Field label="数据记录（OTR）" as="div">
          <SegmentedControl v-model="otrOverride" :options="otrOptions" />
          <p class="text-2xs text-ink-faint mt-1.5">
            选择「跟随设置」时使用用户默认的数据记录模式；其它选项通过
            <code>X-PicoTera-OTR</code> 头覆盖本次请求。
          </p>
        </Field>
      </template>

      <!-- path variables -->
      <Field v-if="pathVarNames.length > 0" label="路径变量" as="div">
        <div class="flex flex-col gap-2">
          <label v-for="name in pathVarNames" :key="name" class="flex items-center gap-2 text-xs">
            <span class="font-mono text-ink-muted shrink-0 w-24 truncate">{{ name }}</span>
            <Input
              class="flex-1"
              :model-value="pathVarValue(name)"
              :placeholder="name === 'model' && hasSupportedFormat ? '默认取模型字段' : ''"
              @update:model-value="(v: string | number) => setPathVar(name, String(v))"
            />
          </label>
        </div>
      </Field>

      <div
        v-if="formatUnsupported"
        class="border border-warn/30 bg-warn/5 text-warn text-xs rounded-md px-3 py-2"
      >
        所选端点格式没有结构化表单，请直接在下方填写原始请求体进行测试。
      </div>

      <!-- structured fields -->
      <template v-if="hasSupportedFormat">
        <Field label="模型">
          <ComboBox
            v-model="model"
            :options="modelOptions"
            :allow-custom="modelAllowCustom"
            placeholder="例如 claude-sonnet-4-6"
          />
        </Field>

        <Field label="系统提示词">
          <Textarea v-model="system" rows="2" placeholder="可留空" />
        </Field>

        <div class="grid grid-cols-2 gap-3">
          <Field label="最大 Tokens">
            <Input v-model.number="maxTokens" type="number" />
          </Field>
          <Field label="流式" as="div">
            <SegmentedControl
              v-model="streamMode"
              :options="streamOptions"
              :class="streamLocked ? 'opacity-55 pointer-events-none' : ''"
            />
          </Field>
        </div>

        <Field label="用户消息">
          <Textarea v-model="userMessage" rows="4" />
        </Field>
      </template>

      <!-- raw body -->
      <Field :label="hasSupportedFormat ? '原始请求体（高级）' : '原始请求体'" as="div" :error="bodyError">
        <div class="flex flex-col gap-1.5">
          <CodeEditor
            :model-value="rawBody"
            min-height="160px"
            @update:model-value="onRawBodyInput"
          />
          <div class="flex items-center gap-2">
            <Button v-if="hasSupportedFormat" type="button" variant="ghost" size="sm" @click="rebuildBody">由字段重建</Button>
            <span v-if="manualOverride" class="text-2xs text-warn">已手动覆盖</span>
          </div>
        </div>
      </Field>

      <!-- custom request headers (advanced) -->
      <Field label="自定义请求头（高级）" as="div">
        <div class="flex flex-col gap-1.5">
          <AnnotationsEditor
            :model-value="customHeaders"
            @update:model-value="onHeadersInput"
          />
          <div class="flex items-center gap-2">
            <Button type="button" variant="ghost" size="sm" @click="rebuildHeaders">由字段重建</Button>
            <span v-if="headersManualOverride" class="text-2xs text-warn">已手动覆盖</span>
          </div>
        </div>
      </Field>

      <div class="flex justify-end gap-2">
        <Button v-if="sending" type="button" variant="ghost" @click="stop">停止</Button>
        <Button type="submit" :disabled="!canSubmit">
          <Icon name="bolt" :size="14" :stroke-width="2.2" />
          <span>{{ sending ? '请求中…' : '发送' }}</span>
        </Button>
      </div>
    </form>

    <!-- right: response -->
    <div class="flex flex-col gap-3 min-w-0">
      <div class="flex items-center gap-3 flex-wrap">
        <span
          v-if="statusCode !== null"
          class="inline-flex items-center px-2 py-0.5 rounded-md text-xs font-mono font-medium"
          :class="responseIsError ? 'bg-err/10 text-err' : 'bg-ok/10 text-ok'"
        >
          HTTP {{ statusCode }}
        </span>
        <span v-if="elapsedMs !== null" class="text-xs text-ink-muted">
          耗时 <Badge>{{ elapsedMs }}ms</Badge>
        </span>
        <span v-if="ttftMs !== null" class="text-xs text-ink-muted">
          TTFT <Badge>{{ ttftMs }}ms</Badge>
        </span>
        <span v-if="responseContentType" class="text-2xs text-ink-faint font-mono truncate">
          {{ responseContentType }}
        </span>
      </div>

      <div
        v-if="sendError"
        class="border border-err/30 bg-err/5 text-err text-sm rounded-md px-3 py-2 font-mono break-all"
      >
        {{ sendError }}
      </div>

      <div
        v-if="statusCode === null && !sendError"
        class="flex items-center justify-center text-ink-faint bg-surface-0 border border-dashed border-line rounded-xl py-14 px-4 text-sm"
      >
        等待发起请求
      </div>

      <template v-else>
        <div v-if="aggregated.thinking" class="flex flex-col gap-1">
          <div class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">思考</div>
          <pre
            class="text-sm text-ink-muted bg-surface-50 border border-line rounded-md p-3 whitespace-pre-wrap break-words max-h-64 overflow-auto"
            >{{ aggregated.thinking }}</pre
          >
        </div>

        <div v-if="replyHtml" class="flex flex-col gap-1">
          <div class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">回复</div>
          <!-- eslint-disable-next-line vue/no-v-html -->
          <div
            class="prose-sm max-w-none text-sm text-ink bg-surface-0 border border-line rounded-md p-3 max-h-96 overflow-auto"
            v-html="replyHtml"
          />
        </div>

        <div class="flex flex-col gap-1">
          <div class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">
            原始响应
          </div>
          <pre
            class="text-xs text-ink-muted bg-surface-50 border border-line rounded-md p-3 whitespace-pre-wrap break-words max-h-[32rem] overflow-auto font-mono"
            >{{ rawResponse || '(空)' }}</pre
          >
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ConversationPart } from '@/composables/conversation'
import { formatToolValue, toolCallTokens, toolResultTokens, type ToolToken } from '@/lib/toolFormat'
import { Icon } from '@/ui'
import ImageAttachment from './ImageAttachment.vue'
import JsonViewer from './json-viewer/JsonViewer.vue'

const props = defineProps<{
  part: Extract<ConversationPart, { kind: 'toolCall' | 'toolResult' }>
}>()

const emit = defineEmits<{ toggle: [] }>()

const isError = computed(() => props.part.kind === 'toolResult' && props.part.isError)

const tokens = computed(() =>
  props.part.kind === 'toolCall'
    ? toolCallTokens(props.part.name, props.part.input)
    : toolResultTokens(props.part.name, props.part.output),
)

function tokenClass(kind: ToolToken['kind']): string {
  switch (kind) {
    case 'name':
      return 'font-semibold text-ink'
    case 'key':
      return 'text-ink-muted'
    case 'punct':
      return 'text-ink-faint'
    case 'value':
      return 'text-ink'
  }
}

// Only a call whose input is a non-empty plain object is shown argument by argument.
const callArgs = computed(() => {
  if (props.part.kind !== 'toolCall') return null
  const input = props.part.input
  if (input === null || typeof input !== 'object' || Array.isArray(input)) return null
  const entries = Object.entries(input)
  return entries.length ? entries : null
})

function isContainer(value: unknown): boolean {
  return value !== null && typeof value === 'object'
}
</script>

<template>
  <details
    class="group rounded-md border bg-surface-0"
    :class="isError ? 'border-err' : 'border-line-soft'"
    @toggle="emit('toggle')"
  >
    <summary
      class="flex cursor-pointer select-none items-start gap-1.5 px-2.5 py-2 text-xs text-ink-muted hover:text-ink"
    >
      <Icon
        name="chevron-down"
        :size="12"
        class="mt-0.5 shrink-0 -rotate-90 transition-transform group-open:rotate-0"
      />
      <Icon
        :name="part.kind === 'toolCall' ? 'tool' : 'corner-down-right'"
        :size="13"
        class="mt-px shrink-0"
      />
      <span class="min-w-0 break-all font-mono">
        <span v-for="(token, index) in tokens" :key="index" :class="tokenClass(token.kind)">{{
          token.text
        }}</span>
        <span v-if="isError" class="ml-1.5 font-sans font-medium text-err-ink">错误</span>
      </span>
    </summary>

    <div class="border-t border-line-soft p-2.5">
      <div v-if="callArgs" class="flex flex-col gap-2.5">
        <div v-for="[key, value] in callArgs" :key="key" class="flex min-w-0 flex-col gap-1">
          <span class="font-mono text-2xs text-ink-muted">{{ key }}</span>
          <pre
            v-if="typeof value === 'string'"
            class="max-h-96 overflow-auto whitespace-pre-wrap break-words font-mono text-xs text-ink"
            >{{ value }}</pre
          >
          <JsonViewer v-else-if="isContainer(value)" :value="value" />
          <span v-else class="font-mono text-xs text-ink">{{
            formatToolValue(value, Infinity)
          }}</span>
        </div>
      </div>

      <pre
        v-else-if="part.kind === 'toolCall' && typeof part.input === 'string'"
        class="max-h-96 overflow-auto whitespace-pre-wrap break-words font-mono text-xs text-ink"
        >{{ part.input }}</pre
      >

      <JsonViewer v-else-if="part.kind === 'toolCall'" :value="part.input" />

      <div v-else-if="part.content" class="flex flex-col gap-2.5">
        <template v-for="(segment, index) in part.content" :key="index">
          <pre
            v-if="segment.kind === 'text'"
            class="max-h-96 overflow-auto whitespace-pre-wrap break-words font-mono text-xs text-ink"
            >{{ segment.text }}</pre
          >
          <ImageAttachment
            v-else-if="segment.kind === 'media' && segment.image"
            :image="segment.image"
            max-height-class="max-h-[320px]"
            @load="emit('toggle')"
          />
          <span
            v-else-if="segment.kind === 'media'"
            class="inline-flex w-fit items-center rounded-[5px] border border-line-soft bg-surface-100 px-1.5 py-0.5 font-mono text-2xs text-ink-muted"
          >
            {{ segment.label }}
          </span>
          <JsonViewer v-else :value="segment.value" />
        </template>
      </div>

      <JsonViewer v-else :value="part.output" />
    </div>
  </details>
</template>

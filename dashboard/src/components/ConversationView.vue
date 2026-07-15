<script setup lang="ts">
import {
  computed,
  nextTick,
  onBeforeUnmount,
  onMounted,
  ref,
  watch,
  type ComponentPublicInstance,
} from 'vue'
import type {
  ConversationMessage,
  ConversationPart,
  ConversationRole,
} from '@/composables/conversation'
import { renderMarkdown } from '@/composables/useSSEParser'
import { Button, Icon, Tag } from '@/ui'
import JsonArtifactViewer from './JsonArtifactViewer.vue'
import SearchResultsView from './SearchResultsView.vue'

const props = defineProps<{ messages: ConversationMessage[] }>()

const collapsedMaxHeight = 80
const visibleMessages = computed(() => props.messages.filter((message) => message.parts.length > 0))
const messageBodies = ref<HTMLElement[]>([])
const expandedIndexes = ref(new Set<number>())
const overflowingIndexes = ref(new Set<number>())

function setMessageBody(el: Element | ComponentPublicInstance | null, index: number) {
  if (el instanceof HTMLElement) {
    messageBodies.value[index] = el
  }
}

function measureOverflow() {
  const next = new Set<number>()
  messageBodies.value.length = visibleMessages.value.length
  for (const [index, el] of messageBodies.value.entries()) {
    if (el && el.scrollHeight > collapsedMaxHeight + 1) next.add(index)
  }
  overflowingIndexes.value = next
}

function scheduleMeasure() {
  nextTick(() => {
    measureOverflow()
  })
}

function isExpanded(index: number): boolean {
  return expandedIndexes.value.has(index)
}

function isOverflowing(index: number): boolean {
  return overflowingIndexes.value.has(index)
}

function toggleExpanded(index: number) {
  const next = new Set(expandedIndexes.value)
  if (next.has(index)) next.delete(index)
  else next.add(index)
  expandedIndexes.value = next
}

function roleLabel(role: ConversationRole): string {
  switch (role) {
    case 'system':
      return '系统'
    case 'user':
      return '用户'
    case 'assistant':
      return '助手'
    case 'tool':
      return '工具'
  }
}

function roleVariant(role: ConversationRole): 'ok' | 'default' | 'muted' | 'accent' {
  switch (role) {
    case 'system':
      return 'muted'
    case 'user':
      return 'accent'
    case 'assistant':
      return 'ok'
    case 'tool':
      return 'default'
  }
}

function roleBorderClass(role: ConversationRole): string {
  switch (role) {
    case 'system':
      return 'border-line-soft'
    case 'user':
      return 'border-accent'
    case 'assistant':
      return 'border-ok'
    case 'tool':
      return 'border-warn'
  }
}

function roleBlockStyle(role: ConversationRole): Record<string, string> {
  let background = 'var(--color-surface-0)'
  if (role === 'user') {
    background = 'color-mix(in oklch, var(--color-accent-faint) 40%, transparent)'
  } else if (role === 'assistant') {
    background = 'color-mix(in oklch, var(--color-ok-faint) 35%, transparent)'
  } else if (role === 'tool') {
    background = 'color-mix(in oklch, var(--color-warn-faint) 35%, transparent)'
  }
  return {
    background,
    '--conversation-block-bg': background,
  }
}

function toolTitle(part: Extract<ConversationPart, { kind: 'toolCall' | 'toolResult' }>): string {
  if (part.kind === 'toolCall') return part.name
  return part.name ?? '工具结果'
}

watch(
  visibleMessages,
  () => {
    expandedIndexes.value = new Set()
    messageBodies.value = []
    scheduleMeasure()
  },
  { immediate: true },
)

onMounted(() => {
  scheduleMeasure()
  window.addEventListener('resize', scheduleMeasure)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', scheduleMeasure)
})
</script>

<template>
  <div class="flex flex-col gap-3">
    <article
      v-for="(message, messageIndex) in visibleMessages"
      :key="messageIndex"
      class="rounded-md border p-3"
      :class="roleBorderClass(message.role)"
      :style="roleBlockStyle(message.role)"
    >
      <div class="mb-2 flex items-center gap-2">
        <Tag :variant="roleVariant(message.role)">{{ roleLabel(message.role) }}</Tag>
      </div>

      <div class="relative">
        <div
          :ref="(el) => setMessageBody(el, messageIndex)"
          class="flex flex-col gap-2.5 overflow-hidden"
          :style="
            isOverflowing(messageIndex) && !isExpanded(messageIndex)
              ? { maxHeight: `${collapsedMaxHeight}px` }
              : undefined
          "
        >
          <template v-for="(part, partIndex) in message.parts" :key="partIndex">
            <div
              v-if="part.kind === 'text'"
              class="prose prose-sm max-w-none text-ink"
              v-html="renderMarkdown(part.text)"
            />

            <details
              v-else-if="part.kind === 'thinking'"
              class="group rounded-md border border-line-soft bg-surface-0"
            >
              <summary
                class="flex cursor-pointer select-none items-center gap-1.5 px-2.5 py-2 text-xs font-medium text-ink-muted hover:text-ink"
                @click="scheduleMeasure"
              >
                <Icon
                  name="chevron-down"
                  :size="12"
                  class="-rotate-90 transition-transform group-open:rotate-0"
                />
                思考过程
              </summary>
              <div
                class="prose prose-sm max-w-none border-t border-line-soft px-2.5 py-2 text-ink"
                v-html="renderMarkdown(part.text)"
              />
            </details>

            <details
              v-else-if="part.kind === 'toolCall' || part.kind === 'toolResult'"
              class="group rounded-md border bg-surface-0"
              :class="
                part.kind === 'toolResult' && part.isError ? 'border-err' : 'border-line-soft'
              "
            >
              <summary
                class="flex cursor-pointer select-none items-center gap-1.5 px-2.5 py-2 text-xs font-medium"
                :class="
                  part.kind === 'toolResult' && part.isError
                    ? 'text-err-ink hover:text-err-ink'
                    : 'text-ink-muted hover:text-ink'
                "
                @click="scheduleMeasure"
              >
                <Icon
                  name="chevron-down"
                  :size="12"
                  class="-rotate-90 transition-transform group-open:rotate-0"
                />
                <Icon name="braces" :size="13" />
                <span class="min-w-0 truncate">{{ toolTitle(part) }}</span>
              </summary>
              <div class="border-t border-line-soft p-2.5">
                <JsonArtifactViewer :value="part.kind === 'toolCall' ? part.input : part.output" />
              </div>
            </details>

            <SearchResultsView
              v-else-if="part.kind === 'searchResults'"
              :results="part.results"
            />

            <span
              v-else-if="part.kind === 'media'"
              class="inline-flex w-fit items-center rounded-[5px] border border-line-soft bg-surface-100 px-1.5 py-0.5 font-mono text-2xs text-ink-muted"
            >
              {{ part.label }}
            </span>
          </template>
        </div>

        <div
          v-if="isOverflowing(messageIndex) && !isExpanded(messageIndex)"
          class="pointer-events-none absolute inset-x-0 bottom-0 h-14 bg-linear-to-b from-transparent to-(--conversation-block-bg)"
        />
      </div>

      <Button
        v-if="isOverflowing(messageIndex)"
        variant="ghost"
        size="sm"
        class="mt-2"
        @click="toggleExpanded(messageIndex)"
      >
        <Icon
          name="chevron-down"
          :size="12"
          :class="isExpanded(messageIndex) ? 'rotate-180' : ''"
        />
        {{ isExpanded(messageIndex) ? '收起' : '展开' }}
      </Button>
    </article>
  </div>
</template>

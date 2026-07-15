<script setup lang="ts">
import type { SearchResult } from '@/composables/conversation'
import { renderMarkdown } from '@/composables/useSSEParser'
import { Tag } from '@/ui'

defineProps<{ results: SearchResult[] }>()

function hostOf(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return ''
  }
}
</script>

<template>
  <div class="flex flex-col gap-2">
    <div
      v-for="(result, index) in results"
      :key="index"
      class="rounded-md border border-line-soft bg-surface-0 p-2.5"
    >
      <div class="flex flex-col gap-0.5">
        <a
          v-if="result.url"
          :href="result.url"
          target="_blank"
          rel="noopener noreferrer"
          class="text-sm font-medium text-accent hover:underline"
        >
          {{ result.title || result.url }}
        </a>
        <span v-else class="text-sm font-medium text-ink">{{ result.title }}</span>
        <span v-if="result.url && hostOf(result.url)" class="text-2xs text-ink-muted">
          {{ hostOf(result.url) }}
        </span>
      </div>

      <div
        v-if="result.citation || result.published || result.crawled || result.wordlim"
        class="mt-1.5 flex flex-wrap gap-1"
      >
        <Tag v-if="result.citation" variant="accent">Cite: {{ result.citation }}</Tag>
        <Tag v-if="result.published" variant="muted">Published: {{ result.published }}</Tag>
        <Tag v-if="result.crawled" variant="muted">Crawled: {{ result.crawled }}</Tag>
        <Tag v-if="result.wordlim" variant="muted">wordlim: {{ result.wordlim }}</Tag>
      </div>

      <div
        v-if="result.content"
        class="prose prose-sm mt-1.5 max-w-none text-ink"
        v-html="renderMarkdown(result.content)"
      />
    </div>
  </div>
</template>

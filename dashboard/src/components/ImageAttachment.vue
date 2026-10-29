<script setup lang="ts">
import { computed } from 'vue'
import type { ImageSource } from '@/composables/images'
import { Icon } from '@/ui'

const props = withDefaults(
  defineProps<{
    image: ImageSource
    alt?: string
    filename?: string
    maxHeightClass?: string
  }>(),
  { alt: '图片', filename: undefined, maxHeightClass: 'max-h-[640px]' },
)

defineEmits<{ load: [] }>()

function formatSize(byteLength: number): string {
  if (byteLength >= 1024 * 1024) return `${(byteLength / 1024 / 1024).toFixed(1)} MB`
  if (byteLength >= 1024) return `${(byteLength / 1024).toFixed(1)} KB`
  return `${byteLength} B`
}

function subtype(mediaType: string): string {
  return mediaType.slice(mediaType.indexOf('/') + 1)
}

// Remote URLs give us neither a media type nor a size, so the host stands in.
function urlHost(src: string): string {
  try {
    return new URL(src).host
  } catch {
    return ''
  }
}

const caption = computed(() => {
  const bits: string[] = []
  if (props.image.mediaType) bits.push(subtype(props.image.mediaType).toUpperCase())
  else if (!props.image.src.startsWith('data:')) {
    const host = urlHost(props.image.src)
    if (host) bits.push(host)
  }
  if (props.image.byteLength !== null) bits.push(formatSize(props.image.byteLength))
  return bits.join(' · ')
})

const downloadName = computed(() => {
  if (props.filename) return props.filename
  if (!props.image.mediaType) return 'image'
  const ext = subtype(props.image.mediaType)
  return `image.${ext === 'jpeg' ? 'jpg' : ext}`
})
</script>

<template>
  <figure class="m-0 overflow-hidden rounded-md border border-line-soft bg-surface-50">
    <img
      :src="image.src"
      :alt="alt"
      class="block w-full object-contain"
      :class="maxHeightClass"
      loading="lazy"
      decoding="async"
      @load="$emit('load')"
    />
    <figcaption class="flex items-center justify-between gap-2 border-t border-line-soft px-2 py-1">
      <span class="min-w-0 truncate text-2xs text-ink-muted">{{ caption }}</span>
      <a
        :href="image.src"
        :download="downloadName"
        title="下载图片"
        aria-label="下载图片"
        class="inline-flex h-[1.375rem] w-[1.375rem] shrink-0 cursor-pointer items-center justify-center rounded-md border border-transparent bg-transparent text-ink-faint transition-colors hover:border-line hover:bg-surface-100 hover:text-ink"
      >
        <Icon name="cloud-download" :size="13" />
      </a>
    </figcaption>
  </figure>
</template>

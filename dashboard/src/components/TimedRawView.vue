<script setup lang="ts">
import { computed, onMounted, onUpdated, ref, shallowRef } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'

const props = defineProps<{ body: string; timings: number[] }>()

const lines = computed(() => {
  const split = props.body.split('\n')
  return split.map((text, i) => ({
    text,
    timeMs: i < props.timings.length ? props.timings[i] : undefined,
  }))
})

const parentRef = ref<HTMLElement | null>(null)
const virtualItemEls = shallowRef([])

function measureAll() {
  rowVirtualizer.value.measureElement(null)
  virtualItemEls.value.forEach((el) => {
    // console.log(el)
    if (el) rowVirtualizer.value.measureElement(el)
  })
}
onMounted(measureAll)
onUpdated(measureAll)

const rowVirtualizer = useVirtualizer<HTMLElement, HTMLElement>(
  computed(() => ({
    count: lines.value.length,
    getScrollElement: () => parentRef.value,
    estimateSize: () => 45,
    // overscan: 20,
  })),
)

const virtualRows = computed(() => rowVirtualizer.value.getVirtualItems())
const totalSize = computed(() => rowVirtualizer.value.getTotalSize())

function formatTime(ms: number | undefined): string {
  if (ms == null) return ''
  if (ms >= 1000) return `+${(ms / 1000).toFixed(1)}s`
  return `+${Math.round(ms)}ms`
}
</script>

<template>
  <div
    ref="parentRef"
    class="max-h-[480px] overflow-auto rounded-md border border-line-soft bg-surface-50"
    style="contain: content"
  >
    <div class="relative w-full" :style="{ height: `${totalSize}px` }">
      <div
        class="absolute left-0 top-0"
        :style="{ transform: `translateY(${virtualRows[0]?.start ?? 0}px)` }"
      >
        <div
          v-for="row in virtualRows"
          :key="row.index"
          class="flex w-full"
          :class="{ 'bg-surface-100': row.index % 2 }"
          :data-index="row.index"
          ref="virtualItemEls"
        >
          <span
            class="shrink-0 w-20 pr-2 text-right font-mono text-xs tabular text-ink-faint select-none border-r border-line-soft"
          >
            {{ formatTime(lines[row.index]?.timeMs) }}
          </span>
          <span class="flex-1 pl-2 font-mono text-xs whitespace-pre-wrap break-all text-ink">{{
            lines[row.index]?.text
          }}</span>
        </div>
      </div>
    </div>
  </div>
</template>

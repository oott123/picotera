<script setup lang="ts">
import { computed, nextTick, onMounted, onUpdated, ref, watch } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import type { ParsedSSEEvent } from '@/composables/useSSEParser'
import JsonViewer from './json-viewer/JsonViewer.vue'

const props = defineProps<{ events: ParsedSSEEvent[] }>()

const parentRef = ref<HTMLElement | null>(null)
const rowElements = ref(new Map<number, HTMLElement>())

function estimateEventHeight(index: number): number {
  const event = props.events[index]
  if (!event) return 220
  if (event.json !== null) return 360
  return Math.min(420, Math.max(132, 96 + event.data.length / 3))
}

const rowVirtualizer = useVirtualizer<HTMLElement, HTMLElement>(
  computed(() => ({
    count: props.events.length,
    getScrollElement: () => parentRef.value,
    getItemKey: (index) => props.events[index]?.index ?? index,
    estimateSize: estimateEventHeight,
    overscan: 4,
  })),
)

const virtualRows = computed(() => rowVirtualizer.value.getVirtualItems())
const totalSize = computed(() => rowVirtualizer.value.getTotalSize())
const visibleRows = computed(() =>
  virtualRows.value.flatMap((virtualRow) => {
    const event = props.events[virtualRow.index]
    return event ? [{ event, virtualRow }] : []
  }),
)

function setRowElement(index: number, el: unknown) {
  if (el instanceof HTMLElement) {
    rowElements.value.set(index, el)
    rowVirtualizer.value.measureElement(el)
  } else {
    rowElements.value.delete(index)
    rowVirtualizer.value.measureElement(null)
  }
}

async function measureVisibleRows() {
  await nextTick()
  for (const row of virtualRows.value) {
    const el = rowElements.value.get(row.index)
    if (el) rowVirtualizer.value.measureElement(el)
  }
}

watch(virtualRows, measureVisibleRows, { flush: 'post' })

onMounted(measureVisibleRows)
onUpdated(measureVisibleRows)

watch(
  () => props.events,
  async () => {
    rowVirtualizer.value.measure()
    rowVirtualizer.value.scrollToOffset(0)
    await measureVisibleRows()
  },
)
</script>

<template>
  <div ref="parentRef" class="max-h-[720px] overflow-auto pr-1" style="contain: content">
    <div class="relative w-full" :style="{ height: `${totalSize}px` }">
      <div
        v-for="{ event, virtualRow } in visibleRows"
        :key="String(virtualRow.key)"
        :ref="(el) => setRowElement(virtualRow.index, el)"
        class="absolute left-0 top-0 w-full pb-2"
        :data-index="virtualRow.index"
        :style="{ transform: `translateY(${virtualRow.start}px)` }"
      >
        <article class="overflow-hidden rounded-md border border-line-soft bg-surface-0">
          <header
            class="flex flex-wrap items-center gap-2 border-b border-line-soft bg-surface-50 px-3 py-2"
          >
            <span class="font-mono text-xs tabular text-ink">#{{ event.index + 1 }}</span>
            <span v-if="event.timeMs != null" class="font-mono text-2xs tabular text-ink-faint">
              {{
                event.timeMs >= 1000
                  ? `+${(event.timeMs / 1000).toFixed(1)}s`
                  : `+${Math.round(event.timeMs)}ms`
              }}
            </span>
            <span class="font-mono text-2xs text-ink-muted">{{ event.event ?? 'message' }}</span>
            <span
              class="ml-auto rounded-[5px] border border-line-soft bg-surface-0 px-1.5 py-0.5 font-mono text-2xs"
              :class="event.json !== null ? 'text-ok-ink' : 'text-ink-faint'"
            >
              {{ event.json !== null ? 'JSON' : 'Text' }}
            </span>
          </header>
          <div class="p-3">
            <JsonViewer v-if="event.json !== null" :value="event.json" />
            <pre
              v-else
              class="font-mono text-xs whitespace-pre-wrap break-all bg-surface-50 border border-line-soft rounded-md p-3 m-0 text-ink overflow-auto max-h-[360px]"
              >{{ event.data }}</pre
            >
          </div>
        </article>
      </div>
    </div>
  </div>
</template>

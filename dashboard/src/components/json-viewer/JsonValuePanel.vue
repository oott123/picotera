<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { Button, Icon, IconButton, Overlay, Tag } from '@/ui'
import { copyFailureText, writeClipboard } from './clipboard'
import {
  formatChars,
  isContainer,
  kindOf,
  parseJsonText,
  prettyJsonOf,
  rawTextOf,
  type JsonKind,
} from './jsonTree'

/**
 * Full-screen inspection of a single value.
 *
 * This is where the "one very large value" case is handled. A tree row only ever
 * shows a truncated preview, so the honest view of a multi-megabyte string lives
 * here: the text is split into fixed-size segments (on newlines first) and the
 * segments are virtualized. One DOM node per visible segment means neither a
 * wrapped blob nor a single enormous line can drag the page down.
 *
 * The container / parsed-JSON views render a tree through the `tree` slot rather
 * than importing `JsonViewer` — that keeps the dependency one-directional, since
 * the viewer is what opens this panel in the first place.
 */

const props = defineProps<{
  /** The value under inspection, held by reference. */
  value: unknown
  /** Display path of the value, used as the title and the download filename. */
  path: string
}>()

const emit = defineEmits<{ close: [] }>()

defineSlots<{ tree: (props: { value: unknown }) => unknown }>()

/** Characters per rendered segment: large enough that the segment count stays
 *  sane, small enough that one wrapped segment is a few lines, not a screenful. */
const SEGMENT_SIZE = 2000

/** text-xs at the 14px root — the size every row renders at. */
const FONT_SIZE_PX = 10
const LINE_HEIGHT = 17

const kind = computed(() => kindOf(props.value))
const isContainerValue = computed(() => isContainer(kind.value))
const isText = computed(() => kind.value === 'string')
const text = computed(() => (typeof props.value === 'string' ? props.value : ''))
// A number, boolean, or null. Nothing to virtualize and nothing to segment —
// it just needs its one-line form on screen.
const isScalar = computed(() => !isContainerValue.value && !isText.value)
const scalarText = computed(() => (isScalar.value ? String(props.value) : ''))

/** Matches the tree's value colouring so a value reads the same in both places. */
const VALUE_CLASS: Record<JsonKind, string> = {
  string: 'text-warn-ink',
  number: 'text-ok-ink',
  boolean: 'text-accent-ink',
  null: 'text-ink-faint',
  object: 'text-ink-muted',
  array: 'text-ink-muted',
}

const copied = ref('')
let copyTimer: ReturnType<typeof setTimeout> | null = null

async function copy(label: string, payload: string) {
  const outcome = await writeClipboard(payload)
  copied.value = outcome === 'ok' ? label : copyFailureText(outcome)
  if (copyTimer) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copied.value = ''
  }, 1600)
}

const pretty = computed(() => prettyJsonOf(props.value, 2))
const rawText = computed(() => rawTextOf(props.value))

const size = computed(() => {
  if (isText.value) return text.value.length
  if (Array.isArray(props.value)) return props.value.length
  if (props.value && typeof props.value === 'object') return Object.keys(props.value).length
  return 0
})

/** Only containers and strings have a meaningful count; a scalar's "0 项" would
 *  be noise, so the header drops it entirely. */
const showSize = computed(() => isText.value || isContainerValue.value)

function download() {
  // The whole document (`$`) has no key of its own to name the file after.
  const name =
    props.path === '$' ? 'value' : props.path.split(/[.[]/).pop()?.replace(/["\]]/g, '') || 'value'
  const body = isText.value ? text.value : (pretty.value ?? rawText.value)
  const blob = new Blob([body], {
    type: isText.value ? 'text/plain;charset=utf-8' : 'application/json;charset=utf-8',
  })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `${name}.${isText.value ? 'txt' : 'json'}`
  anchor.click()
  URL.revokeObjectURL(url)
}

// --- JSON-in-a-string ------------------------------------------------------

// Parsed on demand only: the tree viewer decides whether a string *looks* like
// JSON with a cheap check, and the parse itself is paid for when asked for.
const parsed = ref<unknown>(null)
const parsedOk = ref(false)
const showParsed = ref(false)
const parseFailed = ref(false)

const canParseJson = computed(() => {
  if (!isText.value) return false
  const first = text.value.trimStart()[0]
  return first === '{' || first === '['
})

function toggleParsed() {
  if (showParsed.value) {
    showParsed.value = false
    return
  }
  const result = parseJsonText(text.value)
  parseFailed.value = !result.ok
  if (!result.ok) return
  parsed.value = result.value
  parsedOk.value = true
  showParsed.value = true
}

const showTree = computed(() => isContainerValue.value || (showParsed.value && parsedOk.value))
const treeValue = computed(() => (isContainerValue.value ? props.value : parsed.value))

// --- text segmentation -----------------------------------------------------

const segments = computed(() => {
  if (!isText.value || showTree.value) return []
  const out: string[] = []
  for (const line of text.value.split('\n')) {
    if (line.length <= SEGMENT_SIZE) {
      out.push(line)
      continue
    }
    for (let start = 0; start < line.length; start += SEGMENT_SIZE) {
      out.push(line.slice(start, start + SEGMENT_SIZE))
    }
  }
  return out
})

const scrollRef = ref<HTMLElement | null>(null)
const rowElements = new Map<number, HTMLElement>()

const wrap = ref(true)

const virtualizer = useVirtualizer<HTMLElement, HTMLElement>(
  computed(() => ({
    count: segments.value.length,
    getScrollElement: () => scrollRef.value,
    // Wrapped segments are measured, so the estimate only has to be in the right
    // ballpark; the no-wrap view is exactly one line per segment.
    estimateSize: () => (wrap.value ? LINE_HEIGHT * 3 : LINE_HEIGHT),
    overscan: 6,
  })),
)

const virtualRows = computed(() => virtualizer.value.getVirtualItems())
const totalSize = computed(() => virtualizer.value.getTotalSize())

function setRowElement(index: number, el: unknown) {
  if (el instanceof HTMLElement) {
    rowElements.set(index, el)
    if (wrap.value) virtualizer.value.measureElement(el)
  } else {
    rowElements.delete(index)
  }
}

// --- measuring -------------------------------------------------------------

// The no-wrap view needs a scroll area as wide as its widest segment. Measuring
// one character through a canvas avoids laying the whole string out to find out.
const charWidth = ref(FONT_SIZE_PX * 0.6)

function measureCharWidth() {
  const canvas = document.createElement('canvas')
  const ctx = canvas.getContext('2d')
  if (!ctx) return
  const mono =
    getComputedStyle(document.documentElement).getPropertyValue('--font-mono').trim() || 'monospace'
  ctx.font = `${FONT_SIZE_PX}px ${mono}`
  const width = ctx.measureText('0'.repeat(100)).width / 100
  if (width > 0) charWidth.value = width
}

const contentWidth = computed(() => {
  if (wrap.value || segments.value.length === 0) return undefined
  let longest = 0
  for (const segment of segments.value) {
    if (segment.length > longest) longest = segment.length
  }
  return `${Math.ceil(longest * charWidth.value + 24)}px`
})

let remeasureTimer: ReturnType<typeof setTimeout> | null = null
let observer: ResizeObserver | null = null

function remeasure() {
  virtualizer.value.measure()
  for (const row of virtualRows.value) {
    const el = rowElements.get(row.index)
    if (el) virtualizer.value.measureElement(el)
  }
}

// Wrapped heights depend on the container width, so a resize invalidates every
// cached measurement at once — debounce, then remeasure what is on screen.
function scheduleRemeasure() {
  if (remeasureTimer) clearTimeout(remeasureTimer)
  remeasureTimer = setTimeout(remeasure, 120)
}

// Switching wrap modes reflows every segment the same way a resize does.
watch(wrap, async (value) => {
  await nextTick()
  if (value) remeasure()
  else virtualizer.value.measure()
})

onMounted(() => {
  measureCharWidth()
  document.fonts?.ready.then(() => {
    measureCharWidth()
    scheduleRemeasure()
  })
  if (scrollRef.value) {
    observer = new ResizeObserver(scheduleRemeasure)
    observer.observe(scrollRef.value)
  }
})

onBeforeUnmount(() => {
  observer?.disconnect()
  if (remeasureTimer) clearTimeout(remeasureTimer)
  if (copyTimer) clearTimeout(copyTimer)
})

function handleKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape') return
  event.preventDefault()
  emit('close')
}

onMounted(() => window.addEventListener('keydown', handleKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', handleKeydown))
</script>

<template>
  <Overlay :open="true" blur @click="emit('close')">
    <div
      class="flex h-[min(82vh,880px)] w-[min(1100px,92vw)] flex-col overflow-hidden rounded-xl border border-line bg-surface-0 shadow-lg"
      @click.stop
    >
      <header
        class="flex flex-none items-start justify-between gap-3 border-b border-line bg-surface-50 px-4 py-3"
      >
        <div class="flex min-w-0 flex-col gap-1">
          <div class="flex items-center gap-2">
            <span class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]">
              单独查看
            </span>
            <Tag variant="muted">{{ kind }}</Tag>
            <span v-if="showSize" class="font-mono text-2xs tabular text-ink-faint">
              {{ formatChars(size) }}{{ isText ? ' 字符' : ' 项' }}
            </span>
          </div>
          <span class="truncate font-mono text-xs text-ink" :title="path">{{ path }}</span>
        </div>
        <IconButton title="关闭" aria-label="关闭" @click="emit('close')">
          <Icon name="close" />
        </IconButton>
      </header>

      <div class="flex flex-none flex-wrap items-center gap-2 border-b border-line-soft px-4 py-2">
        <Button variant="ghost" size="sm" @click="copy('已复制', isText ? text : rawText)">
          <Icon :name="copied === '已复制' ? 'check' : 'copy'" :size="13" />
          <span>复制</span>
        </Button>
        <Button
          v-if="!isText"
          variant="ghost"
          size="sm"
          :disabled="pretty === null"
          @click="copy('已复制 JSON', pretty ?? '')"
        >
          <Icon :name="copied === '已复制 JSON' ? 'check' : 'braces'" :size="13" />
          <span>复制为 JSON</span>
        </Button>
        <Button variant="ghost" size="sm" @click="download">
          <Icon name="download" :size="13" />
          <span>下载</span>
        </Button>

        <template v-if="isText && !showTree">
          <label class="flex cursor-pointer items-center gap-1.5 px-2 text-sm text-ink-muted">
            <input v-model="wrap" type="checkbox" class="cursor-pointer" />
            <span>换行</span>
          </label>
          <Button v-if="canParseJson" variant="ghost" size="sm" @click="toggleParsed">
            <Icon name="braces" :size="13" />
            <span>解析为 JSON</span>
          </Button>
        </template>
        <Button v-else-if="showParsed" variant="ghost" size="sm" @click="toggleParsed">
          <Icon name="lines" :size="13" />
          <span>查看原文</span>
        </Button>

        <span v-if="parseFailed" class="text-sm text-err-ink">不是合法的 JSON</span>
        <span v-if="copied && !copied.startsWith('已复制')" class="text-sm text-err-ink">
          {{ copied }}
        </span>
      </div>

      <div v-if="showTree" class="min-h-0 flex-1 p-3">
        <slot name="tree" :value="treeValue" />
      </div>

      <!-- A bare number / boolean / null: one line, no scroller to virtualize. -->
      <div v-else-if="isScalar" class="min-h-0 flex-1 overflow-auto bg-surface-50 p-3">
        <span class="font-mono text-xs" :class="VALUE_CLASS">{{ scalarText }}</span>
      </div>

      <div
        v-else
        ref="scrollRef"
        class="min-h-0 flex-1 overflow-auto bg-surface-50"
        style="contain: content"
      >
        <div class="relative w-full" :style="{ height: `${totalSize}px`, width: contentWidth }">
          <div
            v-for="virtual in virtualRows"
            :key="virtual.index"
            :ref="(el) => setRowElement(virtual.index, el)"
            class="absolute top-0 left-0 px-3 font-mono text-xs text-ink"
            :class="wrap ? 'w-full whitespace-pre-wrap break-all' : 'whitespace-pre'"
            :style="{ transform: `translateY(${virtual.start}px)` }"
            :data-index="virtual.index"
          >
            {{ segments[virtual.index] }}
          </div>
        </div>
      </div>
    </div>
  </Overlay>
</template>

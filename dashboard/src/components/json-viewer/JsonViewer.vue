<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, triggerRef, watch } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { Icon, IconButton, Input } from '@/ui'
import { usePreferencesStore } from '@/stores/preferences'
import type { ToolToken } from '@/lib/toolFormat'
import type { TagKey } from './jsonSummary'
import JsonContextMenu, { type ContextMenuItem } from './JsonContextMenu.vue'
import JsonValuePanel from './JsonValuePanel.vue'
import { copyFailureText, writeClipboard } from './clipboard'
import {
  PREVIEW_LIMIT,
  ROW_BUDGET,
  WRAP_PREVIEW_LIMIT,
  collectExpandable,
  flatten,
  formatChars,
  isContainer,
  parentPathOf,
  prettyJsonOf,
  rawTextOf,
  searchPaths,
  type JsonRow,
} from './jsonTree'

/**
 * JSON tree viewer with windowed rendering.
 *
 * The payload is flattened to one row per *visible* node, so a collapsed
 * container costs a single row however large its subtree is and only the rows
 * inside the viewport reach the DOM. Rows are one fixed-height line each, which
 * keeps the scroll geometry exact without measuring anything — except in
 * soft-wrap mode, where a long string takes several lines and rows are measured.
 */

const props = withDefaults(
  defineProps<{
    value: unknown
    /** Fill the parent's height instead of growing up to `maxHeight`. */
    fill?: boolean
    /** Cap on the auto-growing mode, in pixels. */
    maxHeight?: number
    /** Offer the toolbar button that opens the whole document in the panel. Off
     *  for the viewer inside the panel, which is already showing it. */
    inspectRoot?: boolean
  }>(),
  { fill: false, maxHeight: 480, inspectRoot: true },
)

/** Without soft wrap every row is a single nowrap line, so this height is exact —
 *  it is what lets the virtualizer skip element measurement entirely. */
const ROW_HEIGHT = 20
const INDENT = 12

// A mutable Set behind a shallowRef: the walk reads it once per row, and
// per-key reactivity would register a dependency for every path. Mutate, then
// `triggerRef` — O(1) per toggle, including after "expand all".
const expanded = shallowRef<Set<string>>(new Set())
const truncated = ref(false)
const expandTruncated = ref(false)
const warnDismissed = ref(false)
const selectedPath = ref<string | null>(null)
const copiedHint = ref('')
// A global preference: whoever wants wrapped strings wants them in every viewer.
const prefs = usePreferencesStore()
const wrap = computed({
  get: () => prefs.jsonWrap,
  set: (value) => {
    prefs.jsonWrap = value
  },
})
let hintTimer: ReturnType<typeof setTimeout> | null = null

// --- search ----------------------------------------------------------------

const queryInput = ref('')
const query = ref('')
let queryTimer: ReturnType<typeof setTimeout> | null = null

watch(queryInput, (value) => {
  if (queryTimer) clearTimeout(queryTimer)
  // The scan is full-depth, so hold it until typing pauses.
  queryTimer = setTimeout(() => {
    query.value = value.trim()
  }, 180)
})

const search = computed(() => (query.value ? searchPaths(props.value, query.value) : null))
const needle = computed(() => query.value.toLowerCase())

// --- rows ------------------------------------------------------------------

const result = computed(() =>
  flatten(props.value, {
    expanded: expanded.value,
    visible: search.value ? search.value.visible : null,
    previewLimit: wrap.value ? WRAP_PREVIEW_LIMIT : PREVIEW_LIMIT,
  }),
)

const rows = computed(() => result.value.rows)

watch(result, (value) => {
  truncated.value = value.limited
})

// A new payload resets everything: expansion, selection, search. Reference
// comparison only, so a re-render carrying the same object keeps the view.
watch(
  () => props.value,
  () => {
    // Only the top level is listed at first — and the root, having no row of
    // its own, is open regardless — so nothing starts expanded.
    expanded.value = new Set()
    selectedPath.value = null
    truncated.value = false
    expandTruncated.value = false
    warnDismissed.value = false
    queryInput.value = ''
    query.value = ''
  },
  { immediate: true },
)

// --- virtualization --------------------------------------------------------

const scrollRef = ref<HTMLElement | null>(null)

const virtualizer = useVirtualizer<HTMLElement, HTMLElement>(
  computed(() => ({
    count: rows.value.length,
    getScrollElement: () => scrollRef.value,
    getItemKey: (index) => rows.value[index]?.path ?? index,
    estimateSize: () => ROW_HEIGHT,
    overscan: 8,
  })),
)

const virtualRows = computed(() => virtualizer.value.getVirtualItems())
const totalSize = computed(() => virtualizer.value.getTotalSize())

// Only wrapped rows vary in height. The virtualizer keeps observing a measured
// row, so a width change reflows the heights without extra bookkeeping.
function measureRow(el: unknown) {
  if (wrap.value && el instanceof HTMLElement) virtualizer.value.measureElement(el)
}

// Measurements taken in one mode are wrong for the other: drop them all.
watch(wrap, () => virtualizer.value.measure())

const visibleRows = computed(() =>
  virtualRows.value.flatMap((virtual) => {
    const row = rows.value[virtual.index]
    return row ? [{ row, virtual }] : []
  }),
)

// --- expansion -------------------------------------------------------------

function toggle(path: string) {
  const set = expanded.value
  if (set.has(path)) set.delete(path)
  else set.add(path)
  triggerRef(expanded)
}

function setSubtree(value: unknown, path: string, open: boolean) {
  if (open) {
    const { paths, limited: cut } = collectExpandable(value, path, ROW_BUDGET)
    for (const p of paths) expanded.value.add(p)
    if (cut) expandTruncated.value = true
  } else {
    // Descendants are path-prefixed by construction, so one pass over the set
    // removes the whole subtree without knowing its shape.
    for (const p of expanded.value) {
      if (p === path || p.startsWith(`${path}.`) || p.startsWith(`${path}[`)) {
        expanded.value.delete(p)
      }
    }
  }
  triggerRef(expanded)
}

function expandAll() {
  const { paths, limited: cut } = collectExpandable(props.value, '$', ROW_BUDGET)
  expanded.value = new Set(paths)
  expandTruncated.value = cut
}

function collapseAll() {
  expanded.value = new Set()
  selectedPath.value = null
}

// --- selection & keyboard --------------------------------------------------

const selectedIndex = computed(() => rows.value.findIndex((row) => row.path === selectedPath.value))

function moveSelection(delta: number) {
  const list = rows.value
  if (list.length === 0) return
  const current = selectedIndex.value
  const next = current < 0 ? 0 : Math.min(list.length - 1, Math.max(0, current + delta))
  selectedPath.value = list[next]!.path
  virtualizer.value.scrollToIndex(next, { align: 'auto' })
}

function goToParent(row: JsonRow) {
  // Ancestors come from the path string, not a parent pointer: the row list is
  // rebuilt on every toggle, so a row cannot hold a stable parent reference.
  const parent = parentPathOf(row.path)
  if (!parent) return
  const index = rows.value.findIndex((r) => r.path === parent)
  if (index < 0) return
  selectedPath.value = parent
  virtualizer.value.scrollToIndex(index, { align: 'auto' })
}

function onKeydown(event: KeyboardEvent) {
  const row = rows.value[selectedIndex.value]
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      moveSelection(1)
      break
    case 'ArrowUp':
      event.preventDefault()
      moveSelection(-1)
      break
    case 'ArrowRight':
      if (!row) return
      event.preventDefault()
      if (row.expandable && !row.expanded) toggle(row.path)
      else moveSelection(1)
      break
    case 'ArrowLeft':
      if (!row) return
      event.preventDefault()
      if (row.expandable && row.expanded) toggle(row.path)
      else goToParent(row)
      break
    case 'Enter':
      if (!row) return
      event.preventDefault()
      openPanel(row)
      break
    case 'c':
      // Ctrl/Cmd+C copies the selection, matching the menu's first item.
      if (!row || !(event.ctrlKey || event.metaKey)) return
      event.preventDefault()
      copy(rawTextOf(row.value), '已复制')
      break
  }
}

// --- copy ------------------------------------------------------------------

async function copy(text: string, label: string) {
  const outcome = await writeClipboard(text)
  copiedHint.value = outcome === 'ok' ? label : copyFailureText(outcome)
  if (hintTimer) clearTimeout(hintTimer)
  hintTimer = setTimeout(() => {
    copiedHint.value = ''
  }, 1500)
}

async function copyWholeDocument() {
  const pretty = prettyJsonOf(props.value, 2)
  if (pretty === null) {
    copiedHint.value = '无法序列化'
    if (hintTimer) clearTimeout(hintTimer)
    hintTimer = setTimeout(() => {
      copiedHint.value = ''
    }, 2500)
    return
  }
  await copy(pretty, '已复制全部')
}

// --- single-value panel ----------------------------------------------------

const panel = ref<{ value: unknown; path: string } | null>(null)

function openPanel(row: JsonRow) {
  panel.value = { value: row.value, path: row.path }
}

// Returning from the panel puts the selection back where the user left it.
watch(panel, async (open) => {
  if (open) return
  await nextTick()
  const index = selectedIndex.value
  if (index >= 0) virtualizer.value.scrollToIndex(index, { align: 'auto' })
})

// --- context menu ----------------------------------------------------------

const menu = ref<{ x: number; y: number; row: JsonRow } | null>(null)

function openMenu(event: MouseEvent, row: JsonRow) {
  selectedPath.value = row.path
  menu.value = { x: event.clientX, y: event.clientY, row }
}

const menuItems = computed<ContextMenuItem[]>(() => {
  const row = menu.value?.row
  if (!row) return []
  const items: ContextMenuItem[] = [
    {
      key: 'copy-value',
      label: '复制值',
      icon: 'copy',
      hint: row.kind === 'string' ? `${formatChars(row.size)} 字符` : undefined,
    },
    { key: 'copy-json', label: '复制为 JSON', icon: 'braces' },
    { key: 'copy-path', label: '复制路径', icon: 'route' },
  ]
  if (row.key !== null) {
    items.push({ key: 'copy-key', label: '复制键名', icon: 'key' })
  }
  items.push({ key: 'inspect', label: '单独查看', icon: 'maximize', separatorBefore: true })
  if (row.expandable) {
    // Both always: they act on everything below the node, so either can apply
    // whatever state the node itself is in.
    items.push(
      { key: 'subtree-expand', label: '展开子树', icon: 'chevrons-down' },
      { key: 'subtree-collapse', label: '折叠子树', icon: 'chevrons-up' },
    )
  }
  return items
})

async function onMenuSelect(key: string) {
  const row = menu.value?.row
  if (!row) return
  switch (key) {
    case 'copy-value':
      await copy(rawTextOf(row.value), '已复制')
      break
    case 'copy-json':
      await copy(prettyJsonOf(row.value, 2) ?? rawTextOf(row.value), '已复制 JSON')
      break
    case 'copy-path':
      await copy(row.path, '已复制路径')
      break
    case 'copy-key':
      if (row.key !== null) await copy(row.key, '已复制键名')
      break
    case 'inspect':
      openPanel(row)
      break
    case 'subtree-expand':
      setSubtree(row.value, row.path, true)
      break
    case 'subtree-collapse':
      setSubtree(row.value, row.path, false)
      break
  }
}

// --- display ---------------------------------------------------------------

const VALUE_CLASS: Record<JsonRow['kind'], string> = {
  string: 'text-warn-ink',
  number: 'text-ok-ink',
  boolean: 'text-accent-ink',
  null: 'text-ink-faint',
  object: 'text-ink-muted',
  array: 'text-ink-muted',
}

/** Same palette as the tool calls on the conversation page. */
const ARG_TOKEN_CLASS: Record<ToolToken['kind'], string> = {
  name: 'font-semibold text-ink',
  key: 'text-ink-muted',
  punct: 'text-ink-faint',
  value: 'text-ink',
}

/** A role reads as the speaker, so it stands out from the kind labels. */
function tagClass(key: TagKey): string {
  return key === 'role' ? 'text-ink' : 'text-ink-muted'
}

/** Color of a container row's last segment. The soft-wrap ellipsis inherits the
 *  row's color rather than that of the text it cuts, and the cut almost always
 *  lands in the last segment, so the row takes that segment's color. */
function containerTailClass(row: JsonRow): string {
  const summary = row.summary
  if (summary?.text) return 'text-ink-faint'
  // The cut in a call nearly always falls inside an argument value.
  if (summary?.args) return ARG_TOKEN_CLASS.value
  if (summary?.name) return 'text-ink'
  const lastTag = summary?.tags.at(-1)
  if (lastTag) return tagClass(lastTag.key)
  return VALUE_CLASS[row.kind]
}

/** Split text on the search needle for highlighting. Runs only for the handful
 *  of rows on screen, so it never touches the whole document. */
function highlight(text: string): Array<{ text: string; hit: boolean }> {
  const needleText = needle.value
  if (!needleText) return [{ text, hit: false }]
  const lower = text.toLowerCase()
  const parts: Array<{ text: string; hit: boolean }> = []
  let cursor = 0
  for (;;) {
    const at = lower.indexOf(needleText, cursor)
    if (at < 0) break
    if (at > cursor) parts.push({ text: text.slice(cursor, at), hit: false })
    parts.push({ text: text.slice(at, at + needleText.length), hit: true })
    cursor = at + needleText.length
  }
  if (cursor < text.length) parts.push({ text: text.slice(cursor), hit: false })
  return parts.length > 0 ? parts : [{ text, hit: false }]
}

const matchedCount = computed(() => search.value?.matched.size ?? 0)
const searchTruncated = computed(() => search.value?.limited ?? false)

function clearSearch() {
  queryInput.value = ''
  query.value = ''
}

onBeforeUnmount(() => {
  if (hintTimer) clearTimeout(hintTimer)
  if (queryTimer) clearTimeout(queryTimer)
})
</script>

<template>
  <div
    class="flex flex-col overflow-hidden rounded-md border border-line-soft bg-surface-50"
    :class="fill ? 'h-full min-h-0' : ''"
  >
    <div class="flex flex-none items-center gap-1 border-b border-line-soft bg-surface-0 px-2 py-1">
      <IconButton size="sm" title="折叠全部" aria-label="折叠全部" @click="collapseAll">
        <Icon name="chevrons-up" :size="12" />
      </IconButton>
      <IconButton size="sm" title="展开全部" aria-label="展开全部" @click="expandAll">
        <Icon name="chevrons-down" :size="12" />
      </IconButton>
      <IconButton
        size="sm"
        title="复制全部 JSON"
        aria-label="复制全部 JSON"
        @click="copyWholeDocument"
      >
        <Icon name="copy" :size="12" />
      </IconButton>
      <IconButton
        size="sm"
        :active="wrap"
        title="软换行"
        aria-label="软换行"
        :aria-pressed="wrap"
        @click="wrap = !wrap"
      >
        <Icon name="text-wrap" :size="12" />
      </IconButton>
      <IconButton
        v-if="inspectRoot"
        size="sm"
        title="单独查看"
        aria-label="单独查看"
        @click="panel = { value, path: '$' }"
      >
        <Icon name="maximize" :size="12" />
      </IconButton>

      <span v-if="copiedHint" class="px-1 text-2xs text-ink-muted">{{ copiedHint }}</span>

      <span v-if="query" class="ml-auto px-1 font-mono text-2xs tabular text-ink-faint">
        {{ matchedCount }} 命中
      </span>
      <div class="relative flex items-center" :class="query ? '' : 'ml-auto'">
        <Icon
          name="search"
          :size="11"
          class="pointer-events-none absolute left-2.5 z-10 text-ink-faint"
        />
        <Input
          v-model="queryInput"
          size="sm"
          class="w-52 pl-7.5"
          placeholder="搜索键名或值…"
          @keydown.escape="clearSearch"
        />
      </div>
    </div>

    <div
      v-if="!warnDismissed && (truncated || expandTruncated || searchTruncated)"
      class="flex flex-none items-start gap-2 border-b border-warn bg-warn-faint px-2 py-1 text-2xs text-warn-ink"
    >
      <span class="flex-1">
        <template v-if="searchTruncated">搜索未扫描完整文档，可能还有未列出的命中。</template>
        <template v-else-if="truncated">
          已达 {{ formatChars(ROW_BUDGET) }} 行上限，其余节点未列出 — 折叠部分节点后再展开。
        </template>
        <template v-else>部分子树过深未展开，避免一次渲染过多节点。</template>
      </span>
      <button
        type="button"
        class="flex-none cursor-pointer border-0 bg-transparent p-0 text-warn-ink hover:opacity-70"
        title="知道了"
        aria-label="知道了"
        @click="warnDismissed = true"
      >
        <Icon name="close" :size="11" />
      </button>
    </div>

    <div
      ref="scrollRef"
      role="tree"
      tabindex="0"
      class="min-h-0 overflow-auto outline-none"
      :class="fill ? 'flex-1' : 'flex-none'"
      :style="fill ? { contain: 'content' } : { contain: 'content', maxHeight: `${maxHeight}px` }"
      @keydown="onKeydown"
    >
      <div class="relative w-full" :style="{ height: `${totalSize}px` }">
        <div
          v-for="{ row, virtual } in visibleRows"
          :key="row.path"
          :ref="measureRow"
          :data-index="virtual.index"
          role="treeitem"
          :aria-level="row.depth + 1"
          :aria-expanded="row.expandable ? row.expanded : undefined"
          :aria-selected="row.path === selectedPath"
          class="group absolute top-0 left-0 flex cursor-default pr-1.5 font-mono text-xs transition-colors"
          :class="[
            wrap ? 'w-full items-start' : 'w-max items-center whitespace-nowrap',
            row.path === selectedPath ? 'bg-accent-faint' : 'hover:bg-surface-100',
          ]"
          :style="{
            transform: `translateY(${virtual.start}px)`,
            [wrap ? 'minHeight' : 'height']: `${ROW_HEIGHT}px`,
            lineHeight: `${ROW_HEIGHT}px`,
            paddingLeft: `${6 + row.depth * INDENT}px`,
          }"
          @mousedown="selectedPath = row.path"
          @click="row.expandable && toggle(row.path)"
          @contextmenu.prevent="openMenu($event, row)"
        >
          <!-- centred on the line box, the chevron reads high against
               the lowercase text, whose visual middle is below the box's. -->
          <span
            class="inline-flex h-5 w-3.5 flex-none translate-y-[2px] -translate-x-px items-center justify-center text-ink-faint"
          >
            <Icon
              v-if="row.expandable"
              name="chevron-right"
              :size="12"
              class="transition-transform"
              :class="row.expanded ? 'rotate-90' : ''"
            />
          </span>

          <!-- One inline run, so in soft-wrap mode the key, value and badge wrap
               as a single paragraph instead of as separate flex columns. A
               container row stays on one line even then: its summary is a hint,
               so whatever overflows the width is cut off with an ellipsis. -->
          <span
            :class="
              wrap
                ? isContainer(row.kind)
                  ? ['min-w-0 flex-1 truncate', containerTailClass(row)]
                  : 'min-w-0 flex-1 break-all whitespace-pre-wrap'
                : ''
            "
          >
            <template v-if="row.key !== null">
              <span class="text-accent-ink">
                <span
                  v-for="(part, i) in highlight(row.key)"
                  :key="i"
                  :class="part.hit ? 'bg-warn-faint' : ''"
                  >{{ part.text }}</span
                >
              </span>
              <span class="text-ink-muted">:&nbsp;</span>
            </template>
            <span v-else-if="row.index !== null" class="text-ink-faint"
              >{{ row.index }}<span class="text-ink-muted">:&nbsp;</span></span
            >

            <span :class="VALUE_CLASS[row.kind]">
              <template v-if="isContainer(row.kind)">
                <!-- Elements rather than bare text: whitespace between tags is
                     dropped, so no stray space lands around the preview. -->
                <span>{{ row.preview }}</span>
                <template v-if="row.summary">
                  <span
                    v-for="(tag, i) in row.summary.tags"
                    :key="i"
                    class="ml-1.5"
                    :class="tagClass(tag.key)"
                    >{{ tag.text }}</span
                  >
                  <span v-if="row.summary.name" class="ml-1.5 font-medium text-ink">{{
                    row.summary.name
                  }}</span>
                  <span
                    v-for="(token, i) in row.summary.args ?? []"
                    :key="`a${i}`"
                    :class="ARG_TOKEN_CLASS[token.kind]"
                    >{{ token.text }}</span
                  >
                  <span v-if="row.summary.text" class="ml-1.5 text-ink-faint"
                    >"{{ row.summary.text }}"</span
                  >
                </template>
              </template>
              <template v-else>
                <span
                  v-for="(part, i) in highlight(row.preview)"
                  :key="i"
                  :class="part.hit ? 'bg-warn-faint' : ''"
                  >{{ part.text }}</span
                >
              </template>
            </span>

            <span
              v-if="row.truncated"
              class="ml-2 rounded-[5px] border border-line-soft bg-surface-0 px-1 text-2xs whitespace-nowrap tabular text-ink-faint"
            >
              {{ formatChars(row.size) }} 字符
            </span>
          </span>

          <button
            type="button"
            class="ml-2 inline-flex h-5 flex-none cursor-pointer items-center border-0 bg-transparent p-0 text-ink-faint opacity-0 transition-opacity group-hover:opacity-100 hover:text-ink focus-visible:opacity-100"
            title="单独查看"
            aria-label="单独查看"
            @click.stop="openPanel(row)"
          >
            <Icon name="maximize" :size="11" />
          </button>
          <button
            type="button"
            class="ml-1.5 inline-flex h-5 flex-none cursor-pointer items-center border-0 bg-transparent p-0 text-ink-faint opacity-0 transition-opacity group-hover:opacity-100 hover:text-ink focus-visible:opacity-100"
            title="复制值"
            aria-label="复制值"
            @click.stop="copy(rawTextOf(row.value), '已复制')"
          >
            <Icon name="copy" :size="11" />
          </button>
        </div>
      </div>
    </div>

    <JsonContextMenu
      :open="menu !== null"
      :x="menu?.x ?? 0"
      :y="menu?.y ?? 0"
      :path="menu?.row.path"
      :items="menuItems"
      @select="onMenuSelect"
      @close="menu = null"
    />

    <JsonValuePanel v-if="panel" :value="panel.value" :path="panel.path" @close="panel = null">
      <!-- Recursive: the panel renders its container / parsed-JSON view through
           this slot, which keeps the panel free of a circular import. `fill`
           makes the inner viewer take the panel's remaining height. -->
      <template #tree="{ value }">
        <JsonViewer :value="value" fill :inspect-root="false" />
      </template>
    </JsonValuePanel>
  </div>
</template>

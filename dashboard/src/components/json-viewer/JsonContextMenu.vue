<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { autoUpdate, flip, offset, shift, useFloating } from '@floating-ui/vue'
import Icon from '@/ui/icons/Icon.vue'
import type { IconName } from '@/ui/icons/paths'

export interface ContextMenuItem {
  key: string
  label: string
  icon?: IconName
  disabled?: boolean
  danger?: boolean
  /** Draw a separator above this item, to group the list into sections. */
  separatorBefore?: boolean
  /** Right-aligned hint, e.g. the copy shortcut or a value preview. */
  hint?: string
}

const props = defineProps<{
  open: boolean
  x: number
  y: number
  items: ContextMenuItem[]
  /** Shown as the menu's header — the path of the row it was opened on. */
  path?: string
}>()

const emit = defineEmits<{ select: [string]; close: [] }>()

const menuRef = ref<HTMLElement | null>(null)
const activeIndex = ref(-1)

// The menu is positioned at the cursor, so its reference element is a rect with
// no size at the click point rather than a real DOM node.
const reference = computed(() => ({
  getBoundingClientRect: () => ({
    x: props.x,
    y: props.y,
    top: props.y,
    left: props.x,
    right: props.x,
    bottom: props.y,
    width: 0,
    height: 0,
  }),
}))

const { floatingStyles } = useFloating(reference, menuRef, {
  placement: 'bottom-start',
  strategy: 'fixed',
  whileElementsMounted: autoUpdate,
  middleware: [offset(2), flip({ padding: 8 }), shift({ padding: 8 })],
})

const enabledIndexes = computed(() =>
  props.items.reduce<number[]>((acc, item, i) => {
    if (!item.disabled) acc.push(i)
    return acc
  }, []),
)

function move(delta: number) {
  const list = enabledIndexes.value
  if (list.length === 0) return
  const current = list.indexOf(activeIndex.value)
  const next = current < 0 ? (delta > 0 ? 0 : list.length - 1) : current + delta
  activeIndex.value = list[(next + list.length) % list.length]!
}

function pick(index: number) {
  const item = props.items[index]
  if (!item || item.disabled) return
  // `select` before `close`: the host reads its row out of `select`, and a
  // `close` first would clear it before the action could run.
  emit('select', item.key)
  emit('close')
}

function handleKeydown(event: KeyboardEvent) {
  if (!props.open) return
  switch (event.key) {
    case 'Escape':
      event.preventDefault()
      emit('close')
      break
    case 'ArrowDown':
      event.preventDefault()
      move(1)
      break
    case 'ArrowUp':
      event.preventDefault()
      move(-1)
      break
    case 'Enter':
      event.preventDefault()
      if (activeIndex.value >= 0) pick(activeIndex.value)
      break
  }
}

// A scroll anywhere moves the row the menu was opened on, so the menu would be
// pointing at nothing — close instead of chasing the anchor.
function handleScroll(event: Event) {
  if (!props.open) return
  if (event.target instanceof Node && menuRef.value?.contains(event.target)) return
  emit('close')
}

function handlePointerDown(event: PointerEvent) {
  if (!props.open) return
  if (event.target instanceof Node && menuRef.value?.contains(event.target)) return
  emit('close')
}

watch(
  () => props.open,
  async (open) => {
    activeIndex.value = -1
    if (open) {
      window.addEventListener('keydown', handleKeydown)
      window.addEventListener('scroll', handleScroll, true)
      window.addEventListener('pointerdown', handlePointerDown, true)
      await nextTick()
    } else {
      removeListeners()
    }
  },
)

function removeListeners() {
  window.removeEventListener('keydown', handleKeydown)
  window.removeEventListener('scroll', handleScroll, true)
  window.removeEventListener('pointerdown', handlePointerDown, true)
}

onBeforeUnmount(removeListeners)
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      ref="menuRef"
      role="menu"
      class="fixed z-[1000] min-w-[13rem] max-w-[22rem] py-1 bg-surface-0 border border-line rounded-lg shadow-lg"
      :style="floatingStyles"
    >
      <div
        v-if="path"
        class="px-2.5 pt-1 pb-1.5 mb-1 border-b border-line-soft font-mono text-2xs text-ink-faint truncate"
        :title="path"
      >
        {{ path }}
      </div>
      <template v-for="(item, index) in items" :key="item.key">
        <div v-if="item.separatorBefore && index > 0" class="my-1 border-t border-line-soft" />
        <button
          type="button"
          role="menuitem"
          :disabled="item.disabled"
          class="flex w-full items-center gap-2 px-2.5 py-1.5 bg-transparent border-0 text-left text-sm cursor-pointer transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          :class="[
            item.danger ? 'text-err-ink' : 'text-ink',
            activeIndex === index ? 'bg-surface-100' : 'hover:bg-surface-50',
          ]"
          @mouseenter="activeIndex = index"
          @click="pick(index)"
        >
          <Icon v-if="item.icon" :name="item.icon" :size="12" class="flex-none text-ink-faint" />
          <span v-else class="w-3 flex-none" />
          <span class="flex-1 truncate">{{ item.label }}</span>
          <span v-if="item.hint" class="flex-none font-mono text-2xs text-ink-faint">
            {{ item.hint }}
          </span>
        </button>
      </template>
    </div>
  </Teleport>
</template>

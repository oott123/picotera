<script setup lang="ts" generic="V extends string | number">
import { ref, computed, useTemplateRef, watch, nextTick, onBeforeUnmount, useId } from 'vue'
import {
  useFloating,
  offset,
  flip,
  shift,
  autoUpdate,
  size as sizeMiddleware,
} from '@floating-ui/vue'
import type { Placement } from '@floating-ui/vue'
import Icon from './icons/Icon.vue'

export interface SelectOption<T extends string | number = string | number> {
  value: T
  label: string
  hint?: string
  disabled?: boolean
}

const props = withDefaults(
  defineProps<{
    modelValue?: V | ''
    options: ReadonlyArray<SelectOption<V>>
    query?: string
    searchable?: boolean
    placeholder?: string
    placement?: Placement
    floatingClass?: string
    disabled?: boolean
  }>(),
  {
    searchable: false,
    placeholder: '',
    placement: 'bottom-start',
    floatingClass: '',
    disabled: false,
  },
)

const emit = defineEmits<{
  'update:modelValue': [V | '']
  'update:query': [string]
  close: ['select' | 'outside' | 'escape']
}>()

const open = ref(false)
const internalQuery = ref('')
const activeIndex = ref(0)
const activeByKeyboard = ref(false)
const listboxId = useId()
const triggerRef = useTemplateRef<HTMLElement>('triggerRef')
const floatingRef = useTemplateRef<HTMLElement>('floatingRef')
const inputRef = useTemplateRef<HTMLInputElement>('inputRef')

const query = computed({
  get: () => (props.query === undefined ? internalQuery.value : props.query),
  set: (v: string) => {
    if (props.query === undefined) {
      internalQuery.value = v
    } else {
      emit('update:query', v)
    }
  },
})

const { floatingStyles } = useFloating(triggerRef, floatingRef, {
  placement: props.placement,
  strategy: 'fixed',
  whileElementsMounted: autoUpdate,
  middleware: [
    offset(4),
    flip({ padding: 8 }),
    shift({ padding: 8 }),
    sizeMiddleware({
      apply({ availableHeight, rects, elements }) {
        elements.floating.style.maxHeight = `${Math.max(180, availableHeight - 8)}px`
        elements.floating.style.minWidth = `${rects.reference.width}px`
      },
      padding: 8,
    }),
  ],
})

const filteredOptions = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return props.options
  return props.options.filter((o) => {
    const hay = `${o.label} ${o.hint ?? ''} ${o.value}`.toLowerCase()
    return hay.includes(q)
  })
})

const isActive = computed(() => props.modelValue !== undefined && props.modelValue !== '')

function focusInput() {
  nextTick(() => {
    inputRef.value?.focus()
    inputRef.value?.select()
  })
}

function nextEnabledIndex(from: number, dir: 1 | -1): number {
  const opts = filteredOptions.value
  let i = from
  while (true) {
    const n = i + dir
    if (n < 0 || n >= opts.length) return from // 边界不回绕
    if (!opts[n]?.disabled) return n
    i = n
  }
}

function initialActiveIndex(): number {
  const opts = filteredOptions.value
  const sel = opts.findIndex((o) => o.value === props.modelValue && !o.disabled)
  if (sel >= 0) return sel
  return opts.findIndex((o) => !o.disabled) // 无则首个可用，全不可用返回 -1
}

function onOptionHover(i: number) {
  activeIndex.value = i
  activeByKeyboard.value = false
}

function show() {
  if (props.disabled) return
  open.value = true
  query.value = ''
  activeIndex.value = initialActiveIndex()
  activeByKeyboard.value = false
  if (props.searchable) focusInput()
}

function close(reason: 'select' | 'outside' | 'escape' = 'outside') {
  emit('close', reason)
  open.value = false
}

function toggle() {
  if (open.value) close()
  else show()
}

function pick(value: V | '') {
  emit('update:modelValue', value)
  close('select')
}

function onDocMouseDown(e: MouseEvent) {
  const t = e.target as Node
  if (floatingRef.value?.contains(t)) return
  if (triggerRef.value?.contains(t)) return
  close()
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    close('escape')
    return
  }
  if (!open.value) return
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    activeByKeyboard.value = true
    activeIndex.value = nextEnabledIndex(activeIndex.value, 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    activeByKeyboard.value = true
    activeIndex.value = nextEnabledIndex(activeIndex.value, -1)
  } else if (e.key === 'Enter') {
    const opt = filteredOptions.value[activeIndex.value]
    if (opt && !opt.disabled) {
      e.preventDefault()
      pick(opt.value)
    }
  }
}

watch(open, (v) => {
  if (v) {
    document.addEventListener('mousedown', onDocMouseDown, true)
    document.addEventListener('keydown', onKeydown)
  } else {
    document.removeEventListener('mousedown', onDocMouseDown, true)
    document.removeEventListener('keydown', onKeydown)
  }
})

watch(
  () => query.value,
  () => {
    activeIndex.value = initialActiveIndex()
    activeByKeyboard.value = false
  },
)

watch(activeIndex, (i) => {
  if (i < 0) return
  nextTick(() => {
    floatingRef.value?.querySelector(`[data-index="${i}"]`)?.scrollIntoView({ block: 'nearest' })
  })
})

const activeDescendant = computed(() =>
  open.value && activeIndex.value >= 0 ? `${listboxId}-opt-${activeIndex.value}` : undefined,
)

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocMouseDown, true)
  document.removeEventListener('keydown', onKeydown)
})

defineExpose({ open, show, close, toggle, focusInput })
</script>

<template>
  <div ref="triggerRef" class="relative">
    <slot
      name="trigger"
      :open="open"
      :is-active="isActive"
      :toggle="toggle"
      :show="show"
      :close="close"
    />

    <Teleport to="body">
      <div
        v-if="open"
        ref="floatingRef"
        data-floating-menu
        class="flex flex-col bg-surface-0 border border-line rounded-xl shadow-lg z-[1000] overflow-hidden"
        :class="floatingClass"
        role="listbox"
        :aria-activedescendant="activeDescendant"
        :style="floatingStyles"
      >
        <div
          v-if="searchable"
          class="flex items-center gap-1.5 px-2.5 py-2 border-b border-line-soft"
        >
          <Icon name="search" :size="12" class="text-ink-faint flex-none" />
          <input
            ref="inputRef"
            v-model="query"
            type="text"
            role="combobox"
            :aria-activedescendant="activeDescendant"
            :placeholder="placeholder"
            class="flex-1 min-w-0 bg-transparent border-0 outline-none text-sm text-ink placeholder:text-ink-faint"
          />
        </div>
        <slot name="header" :close="close" />
        <div class="flex-1 overflow-y-auto py-1">
          <slot name="options">
            <button
              v-for="(opt, i) in filteredOptions"
              :id="`${listboxId}-opt-${i}`"
              :key="String(opt.value)"
              type="button"
              role="option"
              :data-index="i"
              :aria-selected="opt.value === modelValue"
              :disabled="opt.disabled"
              class="flex items-center justify-between gap-2 w-full px-2.5 py-1.5 border-0 text-left text-sm cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              :class="[
                opt.value === modelValue ? 'text-accent-ink font-medium' : 'text-ink',
                activeIndex === i
                  ? 'bg-surface-100'
                  : opt.value === modelValue
                    ? 'bg-accent-faint'
                    : 'bg-transparent',
                activeIndex === i && activeByKeyboard
                  ? 'ring-1 ring-inset ring-accent rounded-md'
                  : '',
              ]"
              @mouseenter="onOptionHover(i)"
              @click="pick(opt.value)"
            >
              <span class="flex flex-col min-w-0 leading-tight">
                <span class="truncate">{{ opt.label }}</span>
                <span v-if="opt.hint" class="font-mono text-2xs text-ink-faint truncate">{{
                  opt.hint
                }}</span>
              </span>
              <span
                v-if="opt.value === modelValue"
                class="inline-block w-1.5 h-1.5 rounded-full bg-accent flex-none"
                aria-hidden="true"
              />
            </button>
            <div
              v-if="filteredOptions.length === 0"
              class="px-2.5 py-3 text-center text-xs text-ink-faint"
            >
              无匹配项
            </div>
          </slot>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, useTemplateRef, watch, nextTick } from 'vue'
import SelectMenu, { type SelectOption } from './SelectMenu.vue'
import Icon from './icons/Icon.vue'

export interface ComboBoxOption {
  value: string
  label?: string
  hint?: string
}

const props = withDefaults(
  defineProps<{
    modelValue: string
    options: ComboBoxOption[]
    allowCustom?: boolean
    placeholder?: string
    disabled?: boolean
    size?: 'sm' | 'md'
  }>(),
  {
    allowCustom: false,
    placeholder: '',
    disabled: false,
    size: 'md',
  },
)

const emit = defineEmits<{ 'update:modelValue': [string] }>()

const query = ref('')
const menuRef = useTemplateRef<{
  open: boolean
  show: () => void
  close: (reason?: 'select' | 'outside' | 'escape') => void
}>('menuRef')
const inputRef = useTemplateRef<HTMLInputElement>('inputRef')

const sizeClass = computed(() =>
  props.size === 'sm' ? 'px-2 py-1.5 text-sm' : 'px-3 py-2 text-sm',
)

const displayLabel = computed(() => {
  const selected = props.options.find((o) => o.value === props.modelValue)
  return selected?.label ?? props.modelValue ?? props.placeholder
})

const customValue = computed(() => {
  if (!props.allowCustom) return null
  const q = query.value
  if (q.trim() === '') return null
  if (props.options.some((o) => o.value === q)) return null
  return q
})

const displayOptions = computed<SelectOption<string>[]>(() => {
  const items: SelectOption<string>[] = []
  if (customValue.value !== null) {
    items.push({ value: customValue.value, label: `使用 "${customValue.value}"` })
  }
  for (const o of props.options) {
    items.push({
      value: o.value,
      label: o.label ?? o.value,
      hint: o.hint,
    })
  }
  return items
})

function focusInput() {
  nextTick(() => {
    inputRef.value?.focus()
    inputRef.value?.select()
  })
}

function show() {
  if (props.disabled) return
  menuRef.value?.show()
}

function commitCustom() {
  if (props.allowCustom && query.value.trim() !== '') {
    if (query.value !== props.modelValue) {
      emit('update:modelValue', query.value)
    }
  }
}

function clearQuery() {
  query.value = ''
  focusInput()
}

function pick(value: string) {
  query.value = value
  emit('update:modelValue', value)
  menuRef.value?.close('select')
}

function onMenuClose(reason: 'select' | 'outside' | 'escape') {
  if (reason === 'outside') {
    commitCustom()
  }
}

const menuOpen = computed(() => menuRef.value?.open ?? false)
watch(menuOpen, (v) => {
  if (v) {
    query.value = ''
    focusInput()
  }
})
</script>

<template>
  <SelectMenu
    ref="menuRef"
    :model-value="modelValue"
    :options="displayOptions"
    :query="query"
    :searchable="false"
    :disabled="disabled"
    class="w-full"
    @update:query="query = $event"
    @update:model-value="pick($event)"
    @close="onMenuClose"
  >
    <template #trigger="{ open, isActive }">
      <button
        v-if="!open"
        type="button"
        :disabled="disabled"
        :aria-expanded="false"
        aria-haspopup="listbox"
        class="flex items-center justify-between gap-2 w-full border border-line rounded-md bg-surface-0 text-ink font-sans text-left transition-colors hover:border-surface-300 focus:outline-none focus:border-accent disabled:opacity-55 disabled:cursor-not-allowed"
        :class="sizeClass"
        @click="show"
      >
        <span class="truncate" :class="isActive ? 'text-ink' : 'text-ink-faint'">
          {{ displayLabel }}
        </span>
        <Icon name="chevron-down" :size="14" class="flex-none text-ink-faint" />
      </button>

      <div
        v-else
        class="flex items-center gap-2 w-full border border-accent rounded-md bg-surface-0 text-ink transition-colors focus-within:ring-[3px] focus-within:ring-accent/20"
        :class="sizeClass"
      >
        <input
          ref="inputRef"
          v-model="query"
          type="text"
          :placeholder="placeholder"
          class="flex-1 min-w-0 bg-transparent border-0 outline-none text-sm text-ink placeholder:text-ink-faint"
        />
        <button
          v-if="query !== ''"
          type="button"
          class="flex-none inline-flex items-center p-0 bg-transparent border-0 cursor-pointer text-ink-faint hover:text-ink"
          aria-label="清空"
          title="清空"
          @click="clearQuery"
        >
          <Icon name="close" :size="14" />
        </button>
        <Icon v-else name="chevron-down" :size="14" class="flex-none text-ink-faint rotate-180" />
      </div>
    </template>
  </SelectMenu>
</template>

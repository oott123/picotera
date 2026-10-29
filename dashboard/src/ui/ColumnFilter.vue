<script setup lang="ts" generic="V extends string | number">
import { computed } from 'vue'
import SelectMenu from './SelectMenu.vue'
import Icon from './icons/Icon.vue'

export interface ColumnFilterOption<T extends string | number = string | number> {
  value: T
  label: string
  hint?: string
}

const props = withDefaults(
  defineProps<{
    label: string
    modelValue: V | ''
    options: ColumnFilterOption<V>[]
    emptyValue?: V | ''
    allLabel?: string
    placeholder?: string
    align?: 'left' | 'right'
    searchable?: boolean
    formatActive?: (value: V) => string
  }>(),
  {
    emptyValue: '',
    allLabel: '全部',
    placeholder: '过滤…',
    align: 'left',
    searchable: true,
  },
)

const emit = defineEmits<{ 'update:modelValue': [V | ''] }>()

const placement = computed(() => (props.align === 'right' ? 'bottom-end' : 'bottom-start'))

const isActive = computed(
  () =>
    props.modelValue !== props.emptyValue &&
    props.modelValue !== '' &&
    props.modelValue !== undefined,
)

const activeLabel = computed(() => {
  if (!isActive.value) return ''
  if (props.formatActive) return props.formatActive(props.modelValue as V)
  const opt = props.options.find((o) => o.value === props.modelValue)
  return opt?.label ?? String(props.modelValue)
})

function pick(value: V | '') {
  emit('update:modelValue', value)
}

function clear(e: MouseEvent) {
  e.stopPropagation()
  emit('update:modelValue', props.emptyValue)
}
</script>

<template>
  <SelectMenu
    :model-value="modelValue"
    :options="options"
    :searchable="searchable"
    :placeholder="placeholder"
    :placement="placement"
    floating-class="w-64"
    class="inline-flex"
    @update:model-value="pick"
  >
    <template #trigger="{ toggle, open }">
      <button
        type="button"
        :aria-expanded="open"
        aria-haspopup="listbox"
        class="group inline-flex items-center gap-1 -mx-1 px-1 py-0.5 max-w-full bg-transparent border-0 rounded-xs text-ink-muted text-xs font-medium uppercase tracking-[0.03em] cursor-pointer transition-colors hover:text-ink hover:bg-surface-100 focus:outline-none focus-visible:outline-2 focus-visible:outline-accent focus-visible:outline-offset-2 mr-1"
        :class="[isActive ? 'text-accent-ink' : '', align === 'right' ? 'flex-row-reverse' : '']"
        @click="toggle"
      >
        <span class="inline-flex items-center gap-1 min-w-0">
          <span class="truncate">{{ label }}</span>
          <span
            v-if="isActive"
            class="inline-flex items-center max-w-[10rem] px-1 rounded-xs bg-accent-faint text-accent-ink font-mono normal-case tracking-normal text-2xs"
            :title="activeLabel"
          >
            <span class="truncate">{{ activeLabel }}</span>
          </span>
        </span>
        <span class="inline-flex items-center text-ink-faint group-hover:text-ink-muted">
          <button
            v-if="isActive"
            type="button"
            class="inline-flex items-center p-0 bg-transparent border-0 cursor-pointer text-ink-faint hover:text-ink"
            :aria-label="`清除${label}筛选`"
            :title="`清除${label}筛选`"
            @click="clear"
          >
            <Icon name="close" :size="11" />
          </button>
          <Icon
            v-else
            name="chevron-down"
            :size="11"
            class="transition-transform"
            :class="open ? 'rotate-180' : ''"
          />
        </span>
      </button>
    </template>

    <template #header>
      <button
        type="button"
        role="option"
        :aria-selected="!isActive"
        class="flex items-center justify-between gap-2 w-full px-2.5 py-1.5 bg-transparent border-0 text-left text-sm text-ink-muted cursor-pointer transition-colors hover:bg-surface-50"
        :class="!isActive ? 'text-accent-ink font-medium' : ''"
        @click="pick(emptyValue)"
      >
        <span>{{ allLabel }}</span>
        <span
          v-if="!isActive"
          class="inline-block w-1.5 h-1.5 rounded-full bg-accent flex-none"
          aria-hidden="true"
        />
      </button>
      <div class="my-1 mx-2.5 h-px bg-line-soft" />
    </template>
  </SelectMenu>
</template>

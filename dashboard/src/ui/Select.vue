<script setup lang="ts">
import { computed } from 'vue'
import SelectMenu, { type SelectOption } from './SelectMenu.vue'
import Icon from './icons/Icon.vue'

const props = withDefaults(
  defineProps<{
    modelValue?: string | number
    options: ReadonlyArray<SelectOption>
    placeholder?: string
    disabled?: boolean
    size?: 'sm' | 'md'
    searchable?: boolean
  }>(),
  {
    placeholder: '',
    disabled: false,
    size: 'md',
    searchable: true,
  },
)

const emit = defineEmits<{ 'update:modelValue': [string | number] }>()

defineOptions({ inheritAttrs: false })

const sizeClass = computed(() =>
  props.size === 'sm' ? 'px-2 py-1.5 text-sm' : 'px-3 py-2 text-sm',
)

const selectedOption = computed(() => props.options.find((o) => o.value === props.modelValue))

const selectedLabel = computed(() => selectedOption.value?.label ?? String(props.modelValue ?? ''))

const hasSelection = computed(
  () => selectedOption.value !== undefined && !selectedOption.value.disabled,
)
</script>

<template>
  <SelectMenu
    :model-value="modelValue"
    :options="options"
    :searchable="searchable"
    :disabled="disabled"
    class="w-full"
    v-bind="$attrs"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #trigger="{ toggle, open }">
      <button
        type="button"
        :disabled="disabled"
        :aria-expanded="open"
        aria-haspopup="listbox"
        class="flex items-center justify-between gap-2 w-full border border-line rounded-md bg-surface-0 text-ink font-sans text-left transition-colors hover:border-surface-300 focus:outline-none focus:border-accent disabled:opacity-55 disabled:cursor-not-allowed"
        :class="sizeClass"
        @click="toggle"
      >
        <span class="truncate" :class="hasSelection ? 'text-ink' : 'text-ink-faint'">
          {{ hasSelection ? selectedLabel : placeholder }}
        </span>
        <Icon
          name="chevron-down"
          :size="14"
          class="flex-none text-ink-faint transition-transform"
          :class="open ? 'rotate-180' : ''"
        />
      </button>
    </template>
  </SelectMenu>
</template>

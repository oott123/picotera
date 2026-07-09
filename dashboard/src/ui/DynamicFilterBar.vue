<script setup lang="ts">
import { computed } from 'vue'
import SelectMenu from './SelectMenu.vue'
import IconButton from './IconButton.vue'
import Icon from './icons/Icon.vue'

export interface FilterDef {
  key: string
  label: string
}

const props = withDefaults(
  defineProps<{
    available: FilterDef[]
    modelValue: string[]
    showAddLabel?: boolean
  }>(),
  {
    showAddLabel: false,
  },
)

const emit = defineEmits<{
  'update:modelValue': [string[]]
  remove: [string]
}>()

const visibleSet = computed(() => new Set(props.modelValue))

const hiddenFilters = computed(() =>
  props.available.filter((f) => !visibleSet.value.has(f.key)),
)

function add(key: string) {
  if (!visibleSet.value.has(key)) {
    emit('update:modelValue', [...props.modelValue, key])
  }
}

function remove(key: string) {
  emit('update:modelValue', props.modelValue.filter((k) => k !== key))
  emit('remove', key)
}

function labelFor(key: string): string {
  return props.available.find((f) => f.key === key)?.label ?? key
}
</script>

<template>
  <div class="flex flex-wrap items-end gap-3">
    <div
      v-for="key in modelValue"
      :key="key"
      class="flex flex-col gap-1 min-w-0"
    >
      <div class="flex items-center gap-1">
        <span
          class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]"
        >
          {{ labelFor(key) }}
        </span>
        <IconButton
          size="xs"
          :aria-label="`清除${labelFor(key)}筛选`"
          :title="`清除${labelFor(key)}筛选`"
          @click="remove(key)"
        >
          <Icon name="close" :size="12" />
        </IconButton>
      </div>
      <slot :name="key" />
    </div>

    <SelectMenu
      v-if="hiddenFilters.length"
      :options="hiddenFilters.map((f) => ({ value: f.key, label: f.label }))"
      :searchable="false"
      floating-class="w-48"
      @update:model-value="add($event as string)"
    >
      <template #trigger="{ toggle }">
        <IconButton
          size="sm"
          class="mb-1"
          :aria-label="'添加筛选'"
          :title="'添加筛选'"
          aria-haspopup="listbox"
          @click="toggle"
        >
          <Icon name="filter-plus" :size="13" />
        </IconButton>
      </template>
    </SelectMenu>
  </div>
</template>

<script setup lang="ts" generic="V extends string | number">
import { computed, ref } from 'vue'
import SelectMenu from './SelectMenu.vue'
import SegmentedControl from './SegmentedControl.vue'
import Icon from './icons/Icon.vue'
import type { ColumnFilterOption } from './ColumnFilter.vue'

type MatchMode = string | number
type MatchModeOption<T extends MatchMode = MatchMode> = { value: T; label: string }

const props = withDefaults(
  defineProps<{
    label: string
    modelValue: V[]
    options: ColumnFilterOption<V>[]
    matchMode?: MatchMode
    matchModeOptions?: MatchModeOption[]
    allLabel?: string
    placeholder?: string
    align?: 'left' | 'right'
    searchable?: boolean
    formatActive?: (values: V[]) => string
  }>(),
  {
    allLabel: '全部',
    placeholder: '过滤…',
    align: 'left',
    searchable: true,
  },
)

const emit = defineEmits<{
  'update:modelValue': [V[]]
  'update:matchMode': [MatchMode]
}>()

const query = ref('')
const placement = computed(() => (props.align === 'right' ? 'bottom-end' : 'bottom-start'))
const isActive = computed(() => props.modelValue.length > 0)
const selectedSet = computed(() => new Set<V>(props.modelValue))
const showMatchMode = computed(
  () =>
    props.modelValue.length > 1 &&
    props.matchMode !== undefined &&
    !!props.matchModeOptions?.length,
)

const filteredOptions = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return props.options
  return props.options.filter((o) => {
    const hay = `${o.label} ${o.hint ?? ''} ${o.value}`.toLowerCase()
    return hay.includes(q)
  })
})

const activeLabel = computed(() => {
  if (!isActive.value) return ''
  if (props.formatActive) return props.formatActive(props.modelValue)
  if (props.modelValue.length === 1) {
    const value = props.modelValue[0]
    const opt = props.options.find((o) => o.value === value)
    return opt?.label ?? String(value)
  }
  return `${props.modelValue.length} 项`
})

function toggleValue(value: V) {
  const next = selectedSet.value.has(value)
    ? props.modelValue.filter((v) => v !== value)
    : [...props.modelValue, value]
  emit('update:modelValue', next)
}

function pickAll() {
  emit('update:modelValue', [])
}

function clear(e: MouseEvent) {
  e.stopPropagation()
  emit('update:modelValue', [])
}

function updateMatchMode(value: string | number) {
  emit('update:matchMode', value)
}
</script>

<template>
  <SelectMenu
    v-model:query="query"
    :model-value="''"
    :options="[]"
    :searchable="searchable"
    :placeholder="placeholder"
    :placement="placement"
    floating-class="w-64"
    class="inline-flex"
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
        @click="pickAll"
      >
        <span>{{ allLabel }}</span>
        <span
          v-if="!isActive"
          class="inline-block w-1.5 h-1.5 rounded-full bg-accent flex-none"
          aria-hidden="true"
        />
      </button>
      <div class="my-1 mx-2.5 h-px bg-line-soft" />
      <div v-if="showMatchMode" class="px-2.5 pb-2">
        <SegmentedControl
          :model-value="matchMode as MatchMode"
          :options="matchModeOptions ?? []"
          @update:model-value="updateMatchMode"
        />
      </div>
    </template>

    <template #options>
      <button
        v-for="(opt, i) in filteredOptions"
        :id="`multi-filter-opt-${String(opt.value)}-${i}`"
        :key="String(opt.value)"
        type="button"
        role="option"
        :aria-selected="selectedSet.has(opt.value)"
        class="flex items-center justify-between gap-2 w-full px-2.5 py-1.5 bg-transparent border-0 text-left text-sm text-ink cursor-pointer transition-colors hover:bg-surface-100"
        :class="selectedSet.has(opt.value) ? 'text-accent-ink font-medium bg-accent-faint' : ''"
        @click="toggleValue(opt.value)"
      >
        <span class="flex flex-col min-w-0 leading-tight">
          <span class="truncate">{{ opt.label }}</span>
          <span v-if="opt.hint" class="font-mono text-2xs text-ink-faint truncate">{{
            opt.hint
          }}</span>
        </span>
        <span
          v-if="selectedSet.has(opt.value)"
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
    </template>
  </SelectMenu>
</template>

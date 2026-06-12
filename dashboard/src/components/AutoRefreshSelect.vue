<script setup lang="ts">
import { ref, computed, useTemplateRef, watch, onBeforeUnmount } from 'vue'
import { useFloating, offset, flip, shift, autoUpdate } from '@floating-ui/vue'
import { Icon } from '@/ui'

const props = defineProps<{ modelValue: number }>()
const emit = defineEmits<{ 'update:modelValue': [number] }>()

const OPTIONS: { value: number; label: string }[] = [
  { value: 0, label: '关闭' },
  { value: 200, label: '实时' },
  { value: 5000, label: '5 秒' },
  { value: 10000, label: '10 秒' },
  { value: 30000, label: '30 秒' },
  { value: 60000, label: '1 分钟' },
]

const open = ref(false)
const triggerRef = useTemplateRef<HTMLElement>('triggerRef')
const floatingRef = useTemplateRef<HTMLElement>('floatingRef')
const activeIndex = ref(-1)

const { floatingStyles } = useFloating(triggerRef, floatingRef, {
  placement: 'bottom-end',
  strategy: 'fixed',
  whileElementsMounted: autoUpdate,
  middleware: [offset(4), flip({ padding: 8 }), shift({ padding: 8 })],
})

const isActive = computed(() => props.modelValue > 0)
const currentLabel = computed(() => OPTIONS.find((o) => o.value === props.modelValue)?.label ?? '关闭')

function toggle() {
  if (open.value) close()
  else show()
}

function show() {
  open.value = true
  activeIndex.value = OPTIONS.findIndex((o) => o.value === props.modelValue)
}

function close() {
  open.value = false
}

function pick(value: number) {
  emit('update:modelValue', value)
  close()
  triggerRef.value?.focus()
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
    close()
    triggerRef.value?.focus()
    return
  }
  if (!open.value) return
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    activeIndex.value = Math.min(OPTIONS.length - 1, activeIndex.value + 1)
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    activeIndex.value = Math.max(0, activeIndex.value - 1)
  } else if (e.key === 'Enter') {
    const opt = OPTIONS[activeIndex.value]
    if (opt) {
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

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocMouseDown, true)
  document.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <button
    ref="triggerRef"
    type="button"
    title="自动刷新"
    aria-label="自动刷新频率"
    aria-haspopup="listbox"
    :aria-expanded="open"
    class="inline-flex items-center justify-between gap-1.5 min-w-[4.75rem] px-2 py-1.5 bg-surface-0 border rounded-md text-sm text-ink font-sans cursor-pointer transition-colors focus:outline-none focus:border-accent"
    :class="isActive ? 'border-accent hover:border-accent' : 'border-line hover:border-surface-300'"
    @click="toggle"
  >
    <span class="tabular-nums">{{ currentLabel }}</span>
    <Icon
      name="chevron-down"
      :size="11"
      class="text-ink-faint transition-transform"
      :class="open ? 'rotate-180' : ''"
    />
  </button>

  <Teleport to="body">
    <div
      v-if="open"
      ref="floatingRef"
      class="flex flex-col min-w-[8rem] py-1 bg-surface-0 border border-line rounded-xl shadow-lg z-[1000] overflow-hidden"
      role="listbox"
      :style="floatingStyles"
    >
      <button
        v-for="(opt, i) in OPTIONS"
        :key="opt.value"
        type="button"
        role="option"
        :aria-selected="opt.value === modelValue"
        class="flex items-center justify-between gap-3 w-full px-2.5 py-1.5 bg-transparent border-0 text-left text-sm cursor-pointer transition-colors"
        :class="[
          opt.value === modelValue
            ? 'bg-accent-faint text-accent-ink font-medium'
            : 'text-ink hover:bg-surface-50',
          activeIndex === i && opt.value !== modelValue ? 'bg-surface-50' : '',
        ]"
        @mouseenter="activeIndex = i"
        @click="pick(opt.value)"
      >
        <span>{{ opt.label }}</span>
        <span
          v-if="opt.value === modelValue"
          class="inline-block w-1.5 h-1.5 rounded-full bg-accent flex-none"
          aria-hidden="true"
        />
      </button>
    </div>
  </Teleport>
</template>

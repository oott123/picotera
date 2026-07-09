<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(
  defineProps<{
    variant?: 'default' | 'danger'
    active?: boolean
    size?: 'xs' | 'sm' | 'md'
    type?: 'button' | 'submit' | 'reset'
    disabled?: boolean
  }>(),
  { variant: 'default', active: false, size: 'md', type: 'button', disabled: false },
)

const sizeClass = computed(() =>
  props.size === 'xs' ? 'w-[0.6rem] h-[0.6rem]' : props.size === 'sm' ? 'w-[1.375rem] h-[1.375rem]' : 'w-[1.625rem] h-[1.625rem]',
)

const stateClass = computed(() => {
  if (props.active) {
    return 'bg-accent-faint text-accent-ink border-transparent hover:bg-accent-faint hover:text-accent-ink'
  }
  if (props.variant === 'danger') {
    return 'bg-transparent text-ink-faint border-transparent hover:bg-err-faint hover:border-err-faint hover:text-err-ink'
  }
  return 'bg-transparent text-ink-faint border-transparent hover:bg-surface-100 hover:border-line hover:text-ink'
})
</script>

<template>
  <button
    :type="type"
    :disabled="disabled"
    class="inline-flex items-center justify-center rounded-md border cursor-pointer transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
    :class="[sizeClass, stateClass]"
  >
    <slot />
  </button>
</template>

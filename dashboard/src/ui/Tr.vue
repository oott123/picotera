<script setup lang="ts">
import { computed, provide } from 'vue'
import { trDimmedKey } from './table-keys'

const props = defineProps<{
  selected?: boolean
  isNew?: boolean
  dimmed?: boolean
}>()

provide(
  trDimmedKey,
  computed(() => props.dimmed ?? false),
)
</script>

<template>
  <tr
    class="group transition-colors"
    :class="[
      'hover:bg-surface-50/75',
      selected
        ? 'bg-surface-50 shadow-[inset_2px_0_0_var(--color-accent)] hover:bg-surface-100'
        : '',
      isNew ? 'animate-slide-up' : '',
    ]"
  >
    <slot />
  </tr>
</template>

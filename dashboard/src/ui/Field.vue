<script setup lang="ts">
defineProps<{
  label?: string
  error?: string
  as?: 'label' | 'div'
}>()
</script>

<template>
  <component :is="as ?? 'label'" class="flex flex-col gap-1 min-w-0">
    <span v-if="label" class="text-2xs font-medium text-ink-muted uppercase tracking-[0.03em]">{{
      label
    }}</span>
    <slot />
    <span v-if="error" class="text-sm text-err">{{ error }}</span>
  </component>
</template>

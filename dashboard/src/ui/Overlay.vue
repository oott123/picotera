<script setup lang="ts">
defineProps<{
  open: boolean
  blur?: boolean
}>()

defineEmits<{ click: [MouseEvent] }>()
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      role="dialog"
      aria-modal="true"
      class="fixed inset-0 z-[900] flex items-center justify-center bg-overlay-bg"
      :class="blur ? 'backdrop-blur-[4px]' : ''"
      @click.self="$emit('click', $event)"
    >
      <slot />
    </div>
  </Teleport>
</template>

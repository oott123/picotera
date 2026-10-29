<script setup lang="ts">
defineProps<{ fixed?: boolean }>()
</script>

<template>
  <div class="overflow-x-auto">
    <table
      class="min-w-full border-separate border-spacing-0 text-sm"
      :class="fixed ? 'table-fixed' : ''"
    >
      <slot />
    </table>
  </div>
</template>

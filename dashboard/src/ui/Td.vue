<script setup lang="ts">
import { computed, inject } from 'vue'
import { trDimmedKey } from './table-keys'

defineProps<{ actions?: boolean }>()

const dimmedRef = inject(
  trDimmedKey,
  computed(() => false),
)
const dimmed = computed(() => dimmedRef.value)
</script>

<template>
  <td
    class="py-3 px-4 border-b border-line-soft align-middle text-ink whitespace-nowrap first:pl-[1.125rem] last:pr-[1.125rem]"
    :class="actions ? 'w-[1%] text-right' : ''"
  >
    <div v-if="dimmed" class="opacity-55">
      <slot />
    </div>
    <slot v-else />
  </td>
</template>

<script setup lang="ts">
import IconButton from './IconButton.vue'
import Icon from './icons/Icon.vue'

defineProps<{
  title: string
  kicker?: string
  subtitle?: string
}>()
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <section
    class="bg-surface-0 border border-line rounded-xl shadow-sm flex flex-col overflow-hidden min-h-0"
  >
    <header
      class="flex items-start justify-between gap-2 px-4 py-3.5 border-b border-line bg-surface-50 flex-none"
    >
      <div class="flex flex-col gap-0.5 min-w-0">
        <span
          v-if="kicker"
          class="text-2xs font-medium text-ink-muted uppercase tracking-[0.04em]"
          >{{ kicker }}</span
        >
        <h2
          class="text-[0.9375rem] font-semibold m-0 text-ink overflow-hidden text-ellipsis whitespace-nowrap"
          :title="title"
        >
          {{ title }}
        </h2>
        <p v-if="subtitle" class="m-0 text-xs text-ink-faint leading-[1.35]">{{ subtitle }}</p>
      </div>
      <IconButton title="关闭" aria-label="关闭侧边栏" @click="emit('close')">
        <Icon name="close" />
      </IconButton>
    </header>
    <div class="px-4 py-3.5 flex flex-col gap-[1.125rem] overflow-y-auto flex-1 min-h-0">
      <slot />
    </div>
    <footer
      v-if="$slots.footer"
      class="flex-none flex gap-2 justify-end px-4 py-3 border-t border-line bg-surface-50"
    >
      <slot name="footer" />
    </footer>
    <div
      v-if="$slots.error"
      class="flex-none px-4 py-2 bg-err-faint text-err-ink text-sm border-t border-line"
    >
      <slot name="error" />
    </div>
  </section>
</template>

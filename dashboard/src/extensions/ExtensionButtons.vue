<script setup lang="ts" generic="S extends ExtensionSlot">
import { computed, ref } from 'vue'
import { useQueryClient } from '@tanstack/vue-query'
import { Button, Icon, IconButton } from '@/ui'
import { useExtensionsStore, type ExtensionButtonItem } from '@/stores/extensions'
import type { ExtensionSlot, ExtensionSlotContext } from '@/extensions/types'

// Not named `slot`: that collides with Vue's deprecated slot attribute.
const props = defineProps<{ extensionSlot: S; context: ExtensionSlotContext[S] }>()

const store = useExtensionsStore()
const queryClient = useQueryClient()

const buttons = computed(() => store.buttonsFor(props.extensionSlot))
// Keys of the buttons whose onClick is still pending.
const running = ref(new Set<string>())

function keyOf(button: ExtensionButtonItem, index: number) {
  return `${button.scriptId}:${index}`
}

async function run(button: ExtensionButtonItem, key: string) {
  running.value.add(key)
  try {
    await (button.onClick as (context: ExtensionSlotContext[S]) => void | Promise<void>)(
      props.context,
    )
  } catch (e) {
    store.buttonError = {
      scriptId: button.scriptId,
      label: button.label,
      message: e instanceof Error ? e.message : String(e),
    }
  } finally {
    running.value.delete(key)
    // A button may have changed any resource.
    void queryClient.invalidateQueries()
  }
}
</script>

<template>
  <template v-if="extensionSlot === 'providers.row'">
    <IconButton
      v-for="(b, i) in buttons"
      :key="keyOf(b, i)"
      :title="b.label"
      :aria-label="b.label"
      :disabled="running.has(keyOf(b, i))"
      @click="run(b, keyOf(b, i))"
    >
      <Icon v-if="b.icon" :name="b.icon" :size="13" />
    </IconButton>
  </template>
  <template v-else>
    <Button
      v-for="(b, i) in buttons"
      :key="keyOf(b, i)"
      variant="ghost"
      :disabled="running.has(keyOf(b, i))"
      @click="run(b, keyOf(b, i))"
    >
      <Icon v-if="b.icon" :name="b.icon" :size="14" />
      <span>{{ b.label }}</span>
    </Button>
  </template>
</template>

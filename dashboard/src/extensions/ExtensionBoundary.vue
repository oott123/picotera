<script setup lang="ts">
import { onErrorCaptured, ref } from 'vue'
import { StateText } from '@/ui'

// Catches whatever an extension's component tree throws so it stays confined
// to the extension. Remount (change the key) to clear the error.
const error = ref<string | null>(null)

onErrorCaptured((e) => {
  error.value = e instanceof Error ? e.message : String(e)
  return false
})
</script>

<template>
  <StateText v-if="error !== null" :dashed="false">扩展渲染出错：{{ error }}</StateText>
  <slot v-else />
</template>

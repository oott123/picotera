<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { StateText } from '@/ui'
import ExtensionBoundary from '@/extensions/ExtensionBoundary.vue'
import { useExtensionsStore } from '@/stores/extensions'

const route = useRoute()
const store = useExtensionsStore()

const scriptId = computed(() => String(route.params.scriptId))
const pageId = computed(() => String(route.params.pageId))
const entry = computed(() => store.entries[scriptId.value])
const page = computed(() => store.findPage(scriptId.value, pageId.value))
</script>

<template>
  <StateText v-if="entry?.status === 'loading'">加载中…</StateText>
  <StateText v-else-if="entry?.status === 'error'" :dashed="false">
    扩展「{{ entry.scriptName }}」加载失败：{{ entry.error }}
  </StateText>
  <ExtensionBoundary v-else-if="entry && page" :key="`${scriptId}:${entry.generation}`">
    <component :is="page.component" />
  </ExtensionBoundary>
  <StateText v-else>扩展页面不存在</StateText>
</template>

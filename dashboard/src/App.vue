<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, RouterView } from 'vue-router'
import AppLayout from '@/layouts/AppLayout.vue'
import MinimalLayout from '@/layouts/MinimalLayout.vue'
import ConfirmDialog from '@/ui/ConfirmDialog.vue'
import { useExchangeRates } from '@/composables/useExchangeRates'
import { provideCurrencyContext } from '@/composables/useCurrencyContext'
import { usePreferencesStore } from '@/stores/preferences'
import { useSession } from '@/composables/useSession'

const route = useRoute()
const prefs = usePreferencesStore()
useExchangeRates()
provideCurrencyContext(computed(() => prefs.displayCurrency ?? null))

const session = useSession()
const layouts = { app: AppLayout, minimal: MinimalLayout }
const currentLayout = computed(() => layouts[route.meta.layout])
</script>

<template>
  <div v-if="session.isPending.value" class="min-h-[100dvh] flex items-center justify-center text-ink-faint">
    加载中…
  </div>
  <component v-else :is="currentLayout">
    <RouterView />
  </component>
  <ConfirmDialog />
</template>

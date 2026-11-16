import * as Vue from 'vue'
import * as VueRouter from 'vue-router'
import * as VueQuery from '@tanstack/vue-query'
import * as UI from '@/ui'
import * as client from '@/api/client'
import type { MeView } from '@/api'
import { api } from '@/api/plugin'
import { queryClient } from '@/api/queryClient'
import { queryKeys } from '@/api/queryKeys'
import router from '@/router'
import { useConfirm } from '@/composables/useConfirm'
import { useSidePanel } from '@/composables/useSidePanel'
import { useMe } from '@/composables/useMe'
import { useProvidersMap } from '@/composables/useProvidersMap'
import { useProjectsMap } from '@/composables/useProjectsMap'
import { useCurrencyContext } from '@/composables/useCurrencyContext'
import type { ExtensionHost } from '@/extensions/types'

// createExtensionHost builds the argument handed to a frontend module's default
// export. `me` is the user the module is loaded for (the impersonated one while
// impersonating), which is why the loader reloads everything when it changes.
export function createExtensionHost(me: MeView): ExtensionHost {
  return {
    vue: Vue,
    vueRouter: VueRouter,
    vueQuery: VueQuery,
    ui: UI,
    api,
    client,
    queryKeys,
    queryClient,
    router,
    composables: {
      useConfirm,
      useSidePanel,
      useMe,
      useProvidersMap,
      useProjectsMap,
      useCurrencyContext,
    },
    me,
  }
}

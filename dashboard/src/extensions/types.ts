import type * as Vue from 'vue'
import type * as VueRouter from 'vue-router'
import type * as VueQuery from '@tanstack/vue-query'
import type * as UI from '@/ui'
import type * as Client from '@/api/client'
import type { Component } from 'vue'
import type { ApiClient } from '@/api/plugin'
import type { queryKeys } from '@/api/queryKeys'
import type { MeView, ProviderView } from '@/api'
import type { IconName } from '@/ui/icons/paths'
import type { useConfirm } from '@/composables/useConfirm'
import type { useSidePanel } from '@/composables/useSidePanel'
import type { useMe } from '@/composables/useMe'
import type { useProvidersMap } from '@/composables/useProvidersMap'
import type { useProjectsMap } from '@/composables/useProjectsMap'
import type { useCurrencyContext } from '@/composables/useCurrencyContext'

// The `host` object a script's frontend module receives as the argument of its
// default export. The three libraries are the dashboard's own instances, so a
// component calling useQuery / useRoute shares the dashboard's QueryClient and
// router.
export interface ExtensionHost {
  vue: typeof Vue
  vueRouter: typeof VueRouter
  vueQuery: typeof VueQuery
  ui: typeof UI
  api: ApiClient
  client: typeof Client
  queryKeys: typeof queryKeys
  queryClient: VueQuery.QueryClient
  router: VueRouter.Router
  composables: {
    useConfirm: typeof useConfirm
    useSidePanel: typeof useSidePanel
    useMe: typeof useMe // setup only
    useProvidersMap: typeof useProvidersMap // setup only
    useProjectsMap: typeof useProjectsMap // setup only
    useCurrencyContext: typeof useCurrencyContext // setup only
  }
  me: MeView
}

export type ExtensionSlot = 'providers.toolbar' | 'providers.row' | 'models.toolbar'

export interface ExtensionSlotContext {
  'providers.toolbar': Record<string, never>
  'providers.row': { provider: ProviderView }
  'models.toolbar': Record<string, never>
}

export interface ExtensionPage {
  id: string // ^[a-zA-Z0-9_-]+$, 1–64, unique within the script
  title: string // non-empty
  icon?: IconName // sidebar icon, defaults to 'puzzle'
  component: Component
}

export type ExtensionButton = {
  [S in ExtensionSlot]: {
    slot: S
    label: string // non-empty; tooltip on 'providers.row'
    icon?: IconName // required on 'providers.row'
    onClick: (context: ExtensionSlotContext[S]) => void | Promise<void>
  }
}[ExtensionSlot]

export interface ExtensionDescriptor {
  pages?: ExtensionPage[]
  buttons?: ExtensionButton[]
}

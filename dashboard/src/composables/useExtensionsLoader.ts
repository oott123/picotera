import { watch } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { listScriptFrontends } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import { useMe } from '@/composables/useMe'
import { useExtensionsStore } from '@/stores/extensions'

// useExtensionsLoader keeps the extensions store in step with the enabled
// scripts' frontend modules and the current user. Called once, from App.vue.
export function useExtensionsLoader() {
  const store = useExtensionsStore()
  const { me } = useMe()
  const frontends = useQuery({
    queryKey: queryKeys.scriptFrontends.all,
    queryFn: listScriptFrontends,
  })
  watch(
    [me, () => frontends.data.value],
    ([currentMe, list]) => {
      if (currentMe && list) store.sync(list, currentMe)
    },
    { immediate: true },
  )
}

import { ref } from 'vue'
import type { SubView as ResponseSubView } from '@/components/ResponseArtifactView.vue'

export type DetailTab = 'overview' | 'request' | 'response' | 'conversation' | 'logs' | 'usage'

const detailTab = ref<DetailTab>('overview')
const requestBodyView = ref<'raw' | 'json'>('json')
const requestHeadersOpen = ref(false)
const responseSubView = ref<ResponseSubView>('json')
const responseHeadersOpen = ref(false)
const responseThinkingOpen = ref(false)
const responseRawShowTimings = ref(false)
const liveShowTimings = ref(false)

export function useRequestDetailUiState() {
  return {
    detailTab,
    requestBodyView,
    requestHeadersOpen,
    responseSubView,
    responseHeadersOpen,
    responseThinkingOpen,
    responseRawShowTimings,
    liveShowTimings,
  }
}

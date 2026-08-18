import type { components } from '@/openapi-types'

export type ProviderView = components['schemas']['ProviderView']
export type ProviderModelEntry = components['schemas']['ProviderModelEntry']
export type CreateProviderRequestBody = components['schemas']['CreateProviderRequestBody']
export type ModelView = components['schemas']['ModelView']
export type EndpointView = components['schemas']['EndpointView']
export type ProviderEndpointView = components['schemas']['ProviderEndpointView']
export type RequestView = components['schemas']['RequestView']
export type RequestLiveView = components['schemas']['RequestLiveView']
export type RequestTraceView = components['schemas']['RequestTraceView']
export type TraceCostView = components['schemas']['TraceCostView']
export type ScriptView = components['schemas']['ScriptView']
export type ApiKeyView = components['schemas']['ApiKeyView']
export type ApiKeyMutateBody = components['schemas']['ApiKeyMutateBody']
export type ProjectView = components['schemas']['ProjectView']
export type UpsertProjectRequestBody = components['schemas']['UpsertProjectRequestBody']
export type MergeProjectRequestBody = components['schemas']['MergeProjectRequestBody']
export type FetchModelsRequestBody = components['schemas']['FetchModelsRequestBody']
export type FetchModelsResponseBody = components['schemas']['FetchModelsResponseBody']
export type ExchangeRateView = components['schemas']['ExchangeRateView']
export type Pricing = components['schemas']['Pricing']
export type PricingTier = components['schemas']['PricingTier']
export type PricingMatchCandidate = components['schemas']['PricingMatchCandidate']
export type OverviewSummaryView = components['schemas']['OverviewSummaryView']
export type OverviewDistributionView = components['schemas']['OverviewDistributionView']
export type OverviewDistributionRowView = components['schemas']['OverviewDistributionRowView']
export type OverviewSeriesView = components['schemas']['OverviewSeriesView']
export type OverviewSeriesGroupView = components['schemas']['OverviewSeriesGroupView']
export type OverviewSeriesPointView = components['schemas']['OverviewSeriesPointView']
export type OverviewOutcomeSeriesView = components['schemas']['OverviewOutcomeSeriesView']
export type OverviewOutcomePointView = components['schemas']['OverviewOutcomePointView']
export type OverviewSuccessRateView = components['schemas']['OverviewSuccessRateView']
export type OverviewSpeedBoxplotView = components['schemas']['OverviewSpeedBoxplotView']
export type OverviewSpeedBoxplotItemView = components['schemas']['OverviewSpeedBoxplotItemView']
export type OverviewWindowView = components['schemas']['OverviewWindowView']
export type OverviewCostView = components['schemas']['OverviewCostView']
export type OverviewBreakdownRowView = components['schemas']['OverviewBreakdownRowView']
export type AdminOverviewSummaryView = components['schemas']['AdminOverviewSummaryView']
export type AdminOverviewBreakdownRowView = components['schemas']['AdminOverviewBreakdownRowView']
export type KvEntryView = components['schemas']['KvEntryView']
export type KvMutateBody = components['schemas']['KvMutateBody']
export type UserSettingView = components['schemas']['UserSettingView']
export type UpsertUserSettingRequestBody = components['schemas']['UpsertUserSettingRequestBody']
export type ConfigView = components['schemas']['ConfigView']
export type MeView = components['schemas']['MeView']
export type UserView = components['schemas']['UserView']
export type UserMutateBody = components['schemas']['UserMutateBody']
export type UserIdentityView = components['schemas']['UserIdentityView']
export type UserIdentityMutateBody = components['schemas']['UserIdentityMutateBody']
export type ProviderLabel = components['schemas']['ProviderLabel']
export type ModelLabel = components['schemas']['ModelLabel']
export type EndpointLabel = components['schemas']['EndpointLabel']
export type ProjectLabel = components['schemas']['ProjectLabel']

export type OverviewRange = '1d' | '7d' | '1m' | 'custom'
export type OverviewDimension = 'apiKey' | 'model' | 'upstreamModel' | 'provider' | 'project'
export type OverviewSeriesDimension = 'none' | OverviewDimension
export type OverviewMetric = 'tokens' | 'cost' | 'requests' | 'traces'
export type OverviewOutcomeMetric =
  | 'upstreamSuccessRate'
  | 'downstreamSuccessRate'
  | 'emptyResponseRate'
  | 'finishReasonShare'
export type AdminOverviewDimension = 'user' | 'model' | 'upstreamModel' | 'provider'
export type AdminOverviewSeriesDimension = 'none' | AdminOverviewDimension

export type EndpointType = NonNullable<EndpointView['endpointType']>
export const ENDPOINT_TYPES_MODEL_ROUTED: EndpointType[] = [
  'openaiChatCompletions',
  'openaiResponses',
  'anthropicMessages',
  'anthropicCountTokens',
  'geminiGenerateContent',
  'geminiStreamGenerateContent',
  'codexCompact',
  'codexSearchV1Alpha',
]
export const ENDPOINT_TYPE_LABELS: Record<EndpointType, string> = {
  general: '通用',
  openaiChatCompletions: 'OpenAI 聊天补全',
  openaiResponses: 'OpenAI 响应',
  anthropicMessages: 'Anthropic 消息',
  anthropicCountTokens: 'Anthropic Tokens 计数',
  geminiGenerateContent: 'Gemini 生成内容',
  geminiStreamGenerateContent: 'Gemini 流式生成内容',
  exaSearch: 'Exa 搜索',
  modelList: '模型列表',
  codexCompact: 'Codex 压缩',
  codexSearchV1Alpha: 'Codex 搜索 v1alpha',
  unknown: '未知',
}

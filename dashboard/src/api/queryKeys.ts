import type {
  OverviewDimension,
  OverviewRange,
  OverviewSeriesDimension,
  AdminOverviewDimension,
  AdminOverviewSeriesDimension,
} from './index'

export type ProviderEndpointFilters = Readonly<{ providerId?: number }>

export type OverviewGranularity = 'auto' | '10m' | '1h' | '6h' | '12h' | '24h'

export type OverviewFilters = Readonly<{
  range: OverviewRange
  apiKeyId?: number
  model?: string
  upstreamModel?: string
  providerId?: number
  projectId?: number
  startAt?: string
  endAt?: string
}>
export type AdminOverviewFilters = Readonly<{
  range: OverviewRange
  userId?: number
  model?: string
  upstreamModel?: string
  providerId?: number
  startAt?: string
  endAt?: string
}>

export type RequestsFilters = Readonly<{
  type?: number
  providerId?: number
  endpointPath?: string
  model?: string
  upstreamModel?: string
  traceId?: string
  requestId?: string
  projectId?: number
  startAt?: string
  endAt?: string
  emptyResponse?: boolean
  finishReason?: number
  annotations?: string
}>

export type KvListFilters = Readonly<{ pattern?: string; cursor?: number }>

export type CursorFilters = Readonly<{ limit: number; cursor?: string }>
export type RequestListFilters = RequestsFilters & Partial<CursorFilters>

export const queryKeys = {
  me: ['me'] as const,
  labels: {
    providers: ['labels', 'providers'] as const,
    models: ['labels', 'models'] as const,
    endpoints: ['labels', 'endpoints'] as const,
    projects: ['labels', 'projects'] as const,
    upstreamModels: ['labels', 'upstreamModels'] as const,
  },
  providers: {
    all: ['providers'] as const,
    detail: (id: number) => ['providers', id] as const,
  },
  endpoints: {
    all: ['endpoints'] as const,
  },
  models: {
    all: ['models'] as const,
    detail: (name: string) => ['models', name] as const,
  },
  providerEndpoints: {
    all: ['providerEndpoints'] as const,
    list: (filters: ProviderEndpointFilters = {}) => ['providerEndpoints', { ...filters }] as const,
  },
  scripts: {
    all: ['scripts'] as const,
    detail: (id: string) => ['scripts', id] as const,
  },
  apiKeys: {
    all: ['apiKeys'] as const,
    detail: (id: number) => ['apiKeys', id] as const,
  },
  users: {
    all: ['users'] as const,
    detail: (id: number) => ['users', id] as const,
    identities: (userId: number) => ['users', userId, 'identities'] as const,
  },
  projects: {
    all: ['projects'] as const,
    detail: (id: number) => ['projects', id] as const,
  },
  exchangeRates: {
    all: ['exchangeRates'] as const,
    detail: (code: string) => ['exchangeRates', code] as const,
  },
  requests: {
    all: ['requests'] as const,
    list: (filters: RequestListFilters = {}) => ['requests', { ...filters }] as const,
    detail: (id: string) => ['requests', id] as const,
  },
  requestTraces: {
    all: ['requestTraces'] as const,
    list: (filters: CursorFilters & { startAt?: string; endAt?: string }) =>
      ['requestTraces', { ...filters }] as const,
  },
  requestSpans: {
    all: ['requestSpans'] as const,
    detail: (requestId: string) => ['requestSpans', requestId] as const,
  },
  requestLive: {
    all: ['requestLive'] as const,
    detail: (id: string) => ['requestLive', id] as const,
  },
  pricingMatches: {
    all: ['pricingMatches'] as const,
    model: (model: string) => ['pricingMatches', model] as const,
  },
  fetchModels: {
    all: ['fetchModels'] as const,
    source: (providerId: number) => ['fetchModels', { providerId }] as const,
  },
  kv: {
    all: ['kv'] as const,
    list: (filters: KvListFilters = {}) => ['kv', { ...filters }] as const,
    detail: (key: string) => ['kv', key] as const,
  },
  artifacts: {
    all: ['artifacts'] as const,
    detail: (url: string) => ['artifacts', url] as const,
  },
  userSettings: {
    all: ['userSettings'] as const,
    detail: (key: string) => ['userSettings', key] as const,
  },
  config: ['config'] as const,
  overview: {
    all: ['overview'] as const,
    summary: (f: OverviewFilters) => ['overview', 'summary', { ...f }] as const,
    distribution: (f: OverviewFilters, dim: OverviewDimension) =>
      ['overview', 'distribution', dim, { ...f }] as const,
    series: (f: OverviewFilters, dim: OverviewSeriesDimension, bucket: OverviewGranularity) =>
      ['overview', 'series', dim, bucket, { ...f }] as const,
    speed: (f: OverviewFilters, dim: OverviewSeriesDimension, bucket: OverviewGranularity) =>
      ['overview', 'speed', dim, bucket, { ...f }] as const,
    speedBoxplot: (f: OverviewFilters, dim: OverviewSeriesDimension) =>
      ['overview', 'speedBoxplot', dim, { ...f }] as const,
    cacheHitRate: (f: OverviewFilters, dim: OverviewSeriesDimension, bucket: OverviewGranularity) =>
      ['overview', 'cacheHitRate', dim, bucket, { ...f }] as const,
    outcome: (f: OverviewFilters, dim: OverviewSeriesDimension, bucket: OverviewGranularity) =>
      ['overview', 'outcome', dim, bucket, { ...f }] as const,
  },
  adminOverview: {
    all: ['adminOverview'] as const,
    summary: (f: AdminOverviewFilters) => ['adminOverview', 'summary', { ...f }] as const,
    distribution: (f: AdminOverviewFilters, dim: AdminOverviewDimension) =>
      ['adminOverview', 'distribution', dim, { ...f }] as const,
    series: (f: AdminOverviewFilters, dim: AdminOverviewSeriesDimension, bucket: OverviewGranularity) =>
      ['adminOverview', 'series', dim, bucket, { ...f }] as const,
    speed: (f: AdminOverviewFilters, dim: AdminOverviewSeriesDimension, bucket: OverviewGranularity) =>
      ['adminOverview', 'speed', dim, bucket, { ...f }] as const,
    speedBoxplot: (f: AdminOverviewFilters, dim: AdminOverviewSeriesDimension) =>
      ['adminOverview', 'speedBoxplot', dim, { ...f }] as const,
    cacheHitRate: (f: AdminOverviewFilters, dim: AdminOverviewSeriesDimension, bucket: OverviewGranularity) =>
      ['adminOverview', 'cacheHitRate', dim, bucket, { ...f }] as const,
  },
}

import type { OverviewDimension, OverviewSeriesDimension } from './index'

export type ProviderEndpointFilters = Readonly<{ providerId?: number }>

export type OverviewFilters = Readonly<{
  range: '1d' | '7d' | '1m'
  apiKeyId?: number
  model?: string
  upstreamModel?: string
  providerId?: number
  projectId?: number
}>

export type RequestsFilters = Readonly<{
  type?: number
  providerId?: number
  endpointPath?: string
  model?: string
  upstreamModel?: string
  traceId?: string
  projectId?: number
}>

export type KvListFilters = Readonly<{ pattern?: string; cursor?: number }>

export type CursorFilters = Readonly<{ limit: number; cursor?: string }>
export type RequestListFilters = RequestsFilters & Partial<CursorFilters>

export const queryKeys = {
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
    list: (filters: CursorFilters) => ['requestTraces', { ...filters }] as const,
  },
  requestSpans: {
    all: ['requestSpans'] as const,
    detail: (requestId: string) => ['requestSpans', requestId] as const,
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
  overview: {
    all: ['overview'] as const,
    summary: (f: OverviewFilters) => ['overview', 'summary', { ...f }] as const,
    distribution: (f: OverviewFilters, dim: OverviewDimension) =>
      ['overview', 'distribution', dim, { ...f }] as const,
    series: (f: OverviewFilters, dim: OverviewSeriesDimension) =>
      ['overview', 'series', dim, { ...f }] as const,
    speed: (f: OverviewFilters, dim: OverviewSeriesDimension) =>
      ['overview', 'speed', dim, { ...f }] as const,
  },
  session: {
    all: ['session'] as const,
    current: ['session', 'current'] as const,
  },
  authStatus: {
    all: ['authStatus'] as const,
  },
  accounts: {
    all: ['accounts'] as const,
    list: ['accounts', 'list'] as const,
    detail: (id: number) => ['accounts', 'detail', id] as const,
  },
  credentials: {
    all: ['credentials'] as const,
    mine: ['credentials', 'mine'] as const,
  },
  enrollments: {
    all: ['enrollments'] as const,
    detail: (token: string) => ['enrollments', 'detail', token] as const,
  },
  invitations: {
    all: ['invitations'] as const,
  },
}

import type { QueryClient } from '@tanstack/vue-query'
import { api } from '@/api/plugin'
import type {
  ApiKeyMutateBody,
  ApiKeyView,
  EndpointView,
  ExchangeRateView,
  FetchModelsRequestBody,
  FetchModelsResponseBody,
  ConfigView,
  UserSettingView,
  KvEntryView,
  KvMutateBody,
  MeView,
  ModelView,
  ProviderLabel,
  ModelLabel,
  EndpointLabel,
  ProjectLabel,
  OverviewDimension,
  OverviewDistributionView,
  OverviewSpeedBoxplotView,
  OverviewSeriesDimension,
  OverviewSeriesView,
  OverviewSummaryView,
  AdminOverviewDimension,
  AdminOverviewSeriesDimension,
  AdminOverviewSummaryView,
  PricingMatchCandidate,
  ProjectView,
  ProviderEndpointView,
  ProviderView,
  RequestLiveView,
  RequestView,
  ScriptView,
  UpsertUserSettingRequestBody,
  UpsertProjectRequestBody,
  UserView,
  UserMutateBody,
  UserIdentityView,
  UserIdentityMutateBody,
} from '@/api'
import type { components } from '@/openapi-types'
import {
  queryKeys,
  type KvListFilters,
  type OverviewFilters,
  type AdminOverviewFilters,
  type OverviewGranularity,
  type RequestsFilters,
} from '@/api/queryKeys'

type ApiErrorShape = Partial<components['schemas']['PicoTeraError']>

export class ApiRequestError extends Error {
  readonly code?: string
  readonly details?: string[] | null

  constructor(error: unknown, fallback = '请求失败') {
    const shape = typeof error === 'object' && error !== null ? (error as ApiErrorShape) : {}
    super(shape.message ?? fallback)
    this.name = 'ApiRequestError'
    this.code = shape.code
    this.details = shape.details
  }
}

function fail(error: unknown, fallback?: string): never {
  throw new ApiRequestError(error, fallback)
}

export async function listProviders(): Promise<ProviderView[]> {
  const { data, error } = await api.GET('/api/picotera/providers')
  if (error) fail(error, '加载渠道失败')
  return data ?? []
}

export async function getProvider(id: number): Promise<ProviderView> {
  const { data, error } = await api.GET('/api/picotera/providers/{id}', {
    params: { path: { id } },
  })
  if (error) fail(error, '加载渠道失败')
  return data
}

export async function upsertProvider(
  body: components['schemas']['UpsertProviderRequestBody'],
): Promise<ProviderView> {
  const { data, error } = await api.PUT('/api/picotera/providers', { body })
  if (error) fail(error, '保存渠道失败')
  return data
}

export async function updateProviderModels(
  id: number,
  providerModels: ProviderView['providerModels'],
): Promise<ProviderView> {
  const { data, error } = await api.PUT('/api/picotera/providers/{id}/models', {
    params: { path: { id } },
    body: { providerModels },
  })
  if (error) fail(error, '保存模型失败')
  return data
}

export async function deleteProvider(id: number): Promise<void> {
  const { error } = await api.POST('/api/picotera/providers/delete', { body: { id } })
  if (error) fail(error, '删除渠道失败')
}

export async function listEndpoints(): Promise<EndpointView[]> {
  const { data, error } = await api.GET('/api/picotera/endpoints')
  if (error) fail(error, '加载端点失败')
  return data ?? []
}

export async function upsertEndpoint(body: EndpointView): Promise<EndpointView> {
  const { data, error } = await api.PUT('/api/picotera/endpoints', { body })
  if (error) fail(error, '保存端点失败')
  return data
}

export async function deleteEndpoint(path: string): Promise<void> {
  const { error } = await api.POST('/api/picotera/endpoints/delete', { body: { path } })
  if (error) fail(error, '删除端点失败')
}

export async function listModels(): Promise<ModelView[]> {
  const { data, error } = await api.GET('/api/picotera/models')
  if (error) fail(error, '加载模型失败')
  return data ?? []
}

export async function upsertModel(body: ModelView): Promise<ModelView> {
  const { data, error } = await api.PUT('/api/picotera/models', { body })
  if (error) fail(error, '保存模型失败')
  return data
}

export async function deleteModel(name: string): Promise<void> {
  const { error } = await api.POST('/api/picotera/models/delete', { body: { name } })
  if (error) fail(error, '删除模型失败')
}

export async function listProviderEndpoints(providerId?: number): Promise<ProviderEndpointView[]> {
  const { data, error } = await api.GET('/api/picotera/provider-endpoints', {
    params: providerId === undefined ? undefined : { query: { providerId } },
  })
  if (error) fail(error, '加载绑定失败')
  return data ?? []
}

export async function upsertProviderEndpoint(
  body: ProviderEndpointView,
): Promise<ProviderEndpointView> {
  const { data, error } = await api.PUT('/api/picotera/provider-endpoints', { body })
  if (error) fail(error, '保存绑定失败')
  return data
}

export async function deleteProviderEndpoint(
  body: components['schemas']['DeleteProviderEndpointRequestBody'],
): Promise<void> {
  const { error } = await api.POST('/api/picotera/provider-endpoints/delete', { body })
  if (error) fail(error, '删除绑定失败')
}

export async function fetchProviderModels(
  body: FetchModelsRequestBody,
): Promise<FetchModelsResponseBody> {
  const { data, error } = await api.POST('/api/picotera/providers/fetch-models', { body })
  if (error) fail(error, '拉取模型失败')
  return data
}

export async function matchPricing(targetModel: string): Promise<PricingMatchCandidate[]> {
  const { data, error } = await api.POST('/api/picotera/pricing/matches', {
    body: { targetModel },
  })
  if (error) fail(error, '匹配价格失败')
  return data.candidates ?? []
}

export async function listScripts(): Promise<ScriptView[]> {
  const { data, error } = await api.GET('/api/picotera/scripts')
  if (error) fail(error, '加载脚本失败')
  return data ?? []
}

export async function createScript(
  body: components['schemas']['ScriptMutateBody'],
): Promise<ScriptView> {
  const { data, error } = await api.POST('/api/picotera/scripts', { body })
  if (error) fail(error, '创建脚本失败')
  return data
}

export async function updateScript(
  id: string,
  body: components['schemas']['ScriptMutateBody'],
): Promise<ScriptView> {
  const { data, error } = await api.PUT('/api/picotera/scripts/{id}', {
    params: { path: { id } },
    body,
  })
  if (error) fail(error, '保存脚本失败')
  return data
}

export async function deleteScript(id: string): Promise<void> {
  const { error } = await api.POST('/api/picotera/scripts/delete', { body: { id } })
  if (error) fail(error, '删除脚本失败')
}

export async function listApiKeys(): Promise<ApiKeyView[]> {
  const { data, error } = await api.GET('/api/picotera/api-keys')
  if (error) fail(error, '加载 API Key 失败')
  return data ?? []
}

export async function createApiKey(body: ApiKeyMutateBody): Promise<ApiKeyView> {
  const { data, error } = await api.POST('/api/picotera/api-keys', { body })
  if (error) fail(error, '创建 API Key 失败')
  return data
}

export async function updateApiKey(id: number, body: ApiKeyMutateBody): Promise<ApiKeyView> {
  const { data, error } = await api.PUT('/api/picotera/api-keys/{id}', {
    params: { path: { id } },
    body,
  })
  if (error) fail(error, '保存 API Key 失败')
  return data
}

export async function deleteApiKey(id: number): Promise<void> {
  const { error } = await api.POST('/api/picotera/api-keys/delete', { body: { id } })
  if (error) fail(error, '删除 API Key 失败')
}

export async function listUsers(): Promise<UserView[]> {
  const { data, error } = await api.GET('/api/picotera/users')
  if (error) fail(error, '加载用户失败')
  return data ?? []
}

export async function createUser(body: UserMutateBody): Promise<UserView> {
  const { data, error } = await api.POST('/api/picotera/users', { body })
  if (error) fail(error, '创建用户失败')
  return data
}

export async function updateUser(id: number, body: UserMutateBody): Promise<UserView> {
  const { data, error } = await api.PUT('/api/picotera/users/{id}', {
    params: { path: { id } },
    body,
  })
  if (error) fail(error, '保存用户失败')
  return data
}

export async function deleteUser(id: number): Promise<void> {
  const { error } = await api.POST('/api/picotera/users/delete', { body: { id } })
  if (error) fail(error, '删除用户失败')
}

export async function listUserIdentities(userId: number): Promise<UserIdentityView[]> {
  const { data, error } = await api.GET('/api/picotera/users/{userId}/identities', {
    params: { path: { userId } },
  })
  if (error) fail(error, '加载身份失败')
  return data ?? []
}

export async function createUserIdentity(
  userId: number,
  body: UserIdentityMutateBody,
): Promise<UserIdentityView> {
  const { data, error } = await api.POST('/api/picotera/users/{userId}/identities', {
    params: { path: { userId } },
    body,
  })
  if (error) fail(error, '添加身份失败')
  return data
}

export async function updateUserIdentity(
  userId: number,
  id: number,
  body: UserIdentityMutateBody,
): Promise<UserIdentityView> {
  const { data, error } = await api.PUT('/api/picotera/users/{userId}/identities/{id}', {
    params: { path: { userId, id } },
    body,
  })
  if (error) fail(error, '保存身份失败')
  return data
}

export async function deleteUserIdentity(userId: number, id: number): Promise<void> {
  const { error } = await api.POST('/api/picotera/users/{userId}/identities/delete', {
    params: { path: { userId } },
    body: { id },
  })
  if (error) fail(error, '删除身份失败')
}

export async function listProjects(): Promise<ProjectView[]> {
  const { data, error } = await api.GET('/api/picotera/projects')
  if (error) fail(error, '加载项目失败')
  return data ?? []
}

export async function getProject(id: number): Promise<ProjectView> {
  const { data, error } = await api.GET('/api/picotera/projects/{id}', {
    params: { path: { id } },
  })
  if (error) fail(error, '加载项目失败')
  return data
}

export async function upsertProject(body: UpsertProjectRequestBody): Promise<ProjectView> {
  const { data, error } = await api.PUT('/api/picotera/projects', { body })
  if (error) fail(error, '保存项目失败')
  return data
}

export async function deleteProject(id: number): Promise<void> {
  const { error } = await api.POST('/api/picotera/projects/delete', { body: { id } })
  if (error) fail(error, '删除项目失败')
}

export async function mergeProject(sourceId: number, targetId: number): Promise<ProjectView> {
  const { data, error } = await api.POST('/api/picotera/projects/merge', {
    body: { sourceId, targetId },
  })
  if (error) fail(error, '合并项目失败')
  return data
}

export async function listExchangeRates(): Promise<ExchangeRateView[]> {
  const { data, error } = await api.GET('/api/picotera/exchange-rates')
  if (error) fail(error, '加载汇率失败')
  return data ?? []
}

export async function upsertExchangeRate(body: ExchangeRateView): Promise<ExchangeRateView> {
  const { data, error } = await api.PUT('/api/picotera/exchange-rates', { body })
  if (error) fail(error, '保存汇率失败')
  return data
}

export async function deleteExchangeRate(code: string): Promise<void> {
  const { error } = await api.POST('/api/picotera/exchange-rates/delete', { body: { code } })
  if (error) fail(error, '删除汇率失败')
}

export async function listKvEntries(
  filters: KvListFilters = {},
): Promise<{ items: KvEntryView[]; nextCursor?: string; hasMore: boolean }> {
  const { data, error } = await api.GET('/api/picotera/kv', {
    params: { query: { pattern: filters.pattern, cursor: filters.cursor } },
  })
  if (error) fail(error, '加载 KV 条目失败')
  return {
    items: data?.items ?? [],
    nextCursor: data?.pagination?.nextCursor,
    hasMore: data?.pagination?.hasMore ?? false,
  }
}

export async function getKvEntry(key: string): Promise<KvEntryView> {
  const { data, error } = await api.GET('/api/picotera/kv/{key}', {
    params: { path: { key } },
  })
  if (error) fail(error, '加载 KV 条目失败')
  return data
}

export async function upsertKvEntry(key: string, body: KvMutateBody): Promise<KvEntryView> {
  const { data, error } = await api.PUT('/api/picotera/kv/{key}', {
    params: { path: { key } },
    body,
  })
  if (error) fail(error, '保存 KV 条目失败')
  return data
}

export async function deleteKvEntry(key: string): Promise<void> {
  const { error } = await api.POST('/api/picotera/kv/delete', { body: { key } })
  if (error) fail(error, '删除 KV 条目失败')
}

export function invalidateKv(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.kv.all })
}

export async function listRequests(filters: RequestsFilters & { limit: number; cursor?: string }) {
  const { data, error } = await api.GET('/api/picotera/requests', {
    params: { query: filters },
  })
  if (error) fail(error, '加载请求失败')
  return data
}

export async function getRequest(id: string): Promise<RequestView> {
  const { data, error } = await api.GET('/api/picotera/requests/{id}', {
    params: { path: { id } },
  })
  if (error) fail(error, '加载请求失败')
  return data
}

export async function listRequestSpans(id: string): Promise<RequestView[]> {
  const { data, error } = await api.GET('/api/picotera/requests/{id}/spans', {
    params: { path: { id } },
  })
  if (error) fail(error, '加载请求链路失败')
  return data ?? []
}

export async function getRequestLive(id: string): Promise<RequestLiveView> {
  const { data, error } = await api.GET('/api/picotera/requests/{id}/live', {
    params: { path: { id } },
  })
  if (error) fail(error, '加载实时状态失败')
  return data
}

export async function interruptRequest(id: string): Promise<boolean> {
  const { data, error } = await api.POST('/api/picotera/requests/{id}/interrupt', {
    params: { path: { id } },
  })
  if (error) fail(error, '打断请求失败')
  return data?.interrupted ?? false
}

export async function listRequestTraces(
  filters: { limit: number; cursor?: string; startAt?: string; endAt?: string },
) {
  const { data, error } = await api.GET('/api/picotera/request-traces', {
    params: { query: filters },
  })
  if (error) fail(error, '加载追踪失败')
  return data
}

export function invalidateProviders(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.providers.all })
}

export function invalidateEndpoints(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.endpoints.all })
  client.invalidateQueries({ queryKey: queryKeys.providerEndpoints.all })
}

export function invalidateModels(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.models.all })
  client.invalidateQueries({ queryKey: queryKeys.requests.all })
}

export function invalidateProviderEndpoints(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.providerEndpoints.all })
  client.invalidateQueries({ queryKey: queryKeys.providers.all })
  client.invalidateQueries({ queryKey: queryKeys.models.all })
}

export function invalidateScripts(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.scripts.all })
}

export function invalidateApiKeys(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.apiKeys.all })
}

export function invalidateUsers(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.users.all })
}

export function invalidateUserIdentities(client: QueryClient, userId: number) {
  client.invalidateQueries({ queryKey: queryKeys.users.identities(userId) })
}

export function invalidateProjects(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.projects.all })
  client.invalidateQueries({ queryKey: queryKeys.requests.all })
  client.invalidateQueries({ queryKey: queryKeys.requestTraces.all })
}

export function invalidateExchangeRates(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.exchangeRates.all })
}

export async function listUserSettings(): Promise<UserSettingView[]> {
  const { data, error } = await api.GET('/api/picotera/settings')
  if (error) fail(error, '加载设置失败')
  return data ?? []
}

export async function getUserSetting(key: string): Promise<UserSettingView> {
  const { data, error } = await api.GET('/api/picotera/settings/{key}', {
    params: { path: { key } },
  })
  if (error) fail(error, '加载设置失败')
  return data
}

export async function upsertUserSetting(
  body: UpsertUserSettingRequestBody,
): Promise<UserSettingView> {
  const { data, error } = await api.PUT('/api/picotera/settings', { body })
  if (error) fail(error, '保存设置失败')
  return data
}

export async function deleteUserSetting(key: string): Promise<void> {
  const { error } = await api.DELETE('/api/picotera/settings/{key}', {
    params: { path: { key } },
  })
  if (error) fail(error, '删除设置失败')
}

export function invalidateUserSettings(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.userSettings.all })
}

export async function getConfig(): Promise<ConfigView> {
  const { data, error } = await api.GET('/api/picotera/config')
  if (error) fail(error, '加载应用配置失败')
  return data
}

export async function fetchMe(): Promise<MeView> {
  const { data, error } = await api.GET('/api/picotera/me')
  if (error) fail(error, '加载用户信息失败')
  return data
}

// --- Label fetchers ---
//
// Lightweight, read-only projections of the shared config resources, open to
// every authenticated user. Used by the user-facing views (overview, requests,
// traces, gateway test) which only need id→name / path / endpointType and must
// not read full config (notably provider credentials, which are admin-only).

export async function listProviderLabels(): Promise<ProviderLabel[]> {
  const { data, error } = await api.GET('/api/picotera/labels/providers')
  if (error) fail(error, '加载渠道失败')
  return data ?? []
}

export async function listModelLabels(): Promise<ModelLabel[]> {
  const { data, error } = await api.GET('/api/picotera/labels/models')
  if (error) fail(error, '加载模型失败')
  return data ?? []
}

export async function listEndpointLabels(): Promise<EndpointLabel[]> {
  const { data, error } = await api.GET('/api/picotera/labels/endpoints')
  if (error) fail(error, '加载端点失败')
  return data ?? []
}

export async function listProjectLabels(): Promise<ProjectLabel[]> {
  const { data, error } = await api.GET('/api/picotera/labels/projects')
  if (error) fail(error, '加载项目失败')
  return data ?? []
}

export async function listUpstreamModelLabels(): Promise<string[]> {
  const { data, error } = await api.GET('/api/picotera/labels/upstream-models')
  if (error) fail(error, '加载上游模型失败')
  return data ?? []
}

function overviewQuery(filters: OverviewFilters) {
  const query: Record<string, unknown> = { range: filters.range }
  if (filters.apiKeyId !== undefined) query.apiKeyId = filters.apiKeyId
  if (filters.model !== undefined) query.model = filters.model
  if (filters.upstreamModel !== undefined) query.upstreamModel = filters.upstreamModel
  if (filters.providerId !== undefined) query.providerId = filters.providerId
  if (filters.projectId !== undefined) query.projectId = filters.projectId
  return query
}

export async function getOverviewSummary(filters: OverviewFilters): Promise<OverviewSummaryView> {
  const { data, error } = await api.GET('/api/picotera/overview/summary', {
    params: { query: overviewQuery(filters) as never },
  })
  if (error) fail(error, '加载概览失败')
  return data
}

export async function getOverviewDistribution(
  filters: OverviewFilters,
  dimension: OverviewDimension,
): Promise<OverviewDistributionView> {
  const { data, error } = await api.GET('/api/picotera/overview/distribution', {
    params: { query: { ...overviewQuery(filters), dimension } as never },
  })
  if (error) fail(error, '加载分布失败')
  return data
}

export async function getOverviewSeries(
  filters: OverviewFilters,
  dimension: OverviewSeriesDimension,
  bucket: OverviewGranularity,
): Promise<OverviewSeriesView> {
  const { data, error } = await api.GET('/api/picotera/overview/series', {
    params: { query: { ...overviewQuery(filters), dimension, bucket } as never },
  })
  if (error) fail(error, '加载趋势失败')
  return data
}

export async function getOverviewSpeedBoxplot(
  filters: OverviewFilters,
  dimension: OverviewSeriesDimension,
): Promise<OverviewSpeedBoxplotView> {
  const { data, error } = await api.GET('/api/picotera/overview/speed-boxplot', {
    params: { query: { ...overviewQuery(filters), dimension } as never },
  })
  if (error) fail(error, '加载速度分布失败')
  return data
}

export function invalidateOverview(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.overview.all })
}

function adminOverviewQuery(filters: AdminOverviewFilters) {
  const query: Record<string, unknown> = { range: filters.range }
  if (filters.userId !== undefined) query.userId = filters.userId
  if (filters.model !== undefined) query.model = filters.model
  if (filters.upstreamModel !== undefined) query.upstreamModel = filters.upstreamModel
  if (filters.providerId !== undefined) query.providerId = filters.providerId
  return query
}

export async function getAdminOverviewSummary(
  filters: AdminOverviewFilters,
): Promise<AdminOverviewSummaryView> {
  const { data, error } = await api.GET('/api/picotera/admin/overview/summary', {
    params: { query: adminOverviewQuery(filters) as never },
  })
  if (error) fail(error, '加载概览失败')
  return data
}

export async function getAdminOverviewDistribution(
  filters: AdminOverviewFilters,
  dimension: AdminOverviewDimension,
): Promise<OverviewDistributionView> {
  const { data, error } = await api.GET('/api/picotera/admin/overview/distribution', {
    params: { query: { ...adminOverviewQuery(filters), dimension } as never },
  })
  if (error) fail(error, '加载分布失败')
  return data
}

export async function getAdminOverviewSeries(
  filters: AdminOverviewFilters,
  dimension: AdminOverviewSeriesDimension,
  bucket: OverviewGranularity,
): Promise<OverviewSeriesView> {
  const { data, error } = await api.GET('/api/picotera/admin/overview/series', {
    params: { query: { ...adminOverviewQuery(filters), dimension, bucket } as never },
  })
  if (error) fail(error, '加载趋势失败')
  return data
}

export async function getAdminOverviewSpeedBoxplot(
  filters: AdminOverviewFilters,
  dimension: AdminOverviewSeriesDimension,
): Promise<OverviewSpeedBoxplotView> {
  const { data, error } = await api.GET('/api/picotera/admin/overview/speed-boxplot', {
    params: { query: { ...adminOverviewQuery(filters), dimension } as never },
  })
  if (error) fail(error, '加载速度分布失败')
  return data
}

export function invalidateAdminOverview(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.adminOverview.all })
}

// --- Endpoint testing (streaming, raw fetch) ---
//
// The two test "send" actions stream their responses, so they bypass
// openapi-fetch and return the raw Response for the caller to read incrementally
// (via the body stream) or as JSON. Neither throws on non-2xx — the UI surfaces
// the upstream status and body.

export interface TestDirectPayload {
  providerId: number
  endpointPath: string
  stream: boolean
  pathVars?: Record<string, string>
  headers?: Record<string, string>
  body: unknown
}

// postTestDirect calls the short-circuit test route. The backend injects the
// provider's credentials and forwards `body` verbatim to the upstream.
export function postTestDirect(payload: TestDirectPayload, signal?: AbortSignal): Promise<Response> {
  return fetch('/api/picotera/test/direct', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
    signal,
  })
}

// postGatewayTest sends a full gateway request to `targetUrl` authenticated with
// the API key's plaintext value as a Bearer token.
export function postGatewayTest(
  targetUrl: string,
  apiKey: string,
  body: unknown,
  headers?: Record<string, string>,
  signal?: AbortSignal,
): Promise<Response> {
  return fetch(targetUrl, {
    method: 'POST',
    // Omit cookies: the gateway authenticates via the API key Bearer token, so
    // the request should mirror a real external client and not leak the
    // dashboard session.
    credentials: 'omit',
    // Content-Type is a default the caller may override; Authorization is forced
    // last so a custom header can never break API-key authentication.
    headers: {
      'Content-Type': 'application/json',
      ...headers,
      Authorization: `Bearer ${apiKey}`,
    },
    body: JSON.stringify(body),
    signal,
  })
}

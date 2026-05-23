import type { QueryClient } from '@tanstack/vue-query'
import { api } from '@/api/plugin'
import type {
  ApiKeyMutateBody,
  ApiKeyView,
  EndpointView,
  ExchangeRateView,
  FetchModelsRequestBody,
  FetchModelsResponseBody,
  KvEntryView,
  KvMutateBody,
  ModelView,
  OverviewDimension,
  OverviewDistributionView,
  OverviewSeriesDimension,
  OverviewSeriesView,
  OverviewSummaryView,
  PricingMatchCandidate,
  ProjectView,
  ProviderEndpointView,
  ProviderView,
  RequestView,
  ScriptView,
  SimulateDispatchRequestBody,
  SimulateDispatchResponseBody,
  UpsertProjectRequestBody,
} from '@/api'
import type { components } from '@/openapi-types'
import {
  queryKeys,
  type KvListFilters,
  type OverviewFilters,
  type RequestsFilters,
} from '@/api/queryKeys'

type SessionView = components['schemas']['SessionView']
type AuthStatus = components['schemas']['AuthStatus']
type CredentialView = components['schemas']['CredentialView']
type EnrollmentPreview = components['schemas']['EnrollmentPreview']

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

export async function listRequestTraces(filters: { limit: number; cursor?: string }) {
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

export function invalidateProjects(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.projects.all })
  client.invalidateQueries({ queryKey: queryKeys.requests.all })
  client.invalidateQueries({ queryKey: queryKeys.requestTraces.all })
}

export function invalidateExchangeRates(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.exchangeRates.all })
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
): Promise<OverviewSeriesView> {
  const { data, error } = await api.GET('/api/picotera/overview/series', {
    params: { query: { ...overviewQuery(filters), dimension } as never },
  })
  if (error) fail(error, '加载趋势失败')
  return data
}

export function invalidateOverview(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.overview.all })
}

export async function simulateDispatch(
  body: SimulateDispatchRequestBody,
): Promise<SimulateDispatchResponseBody> {
  const { data, error } = await api.POST('/api/picotera/simulate/dispatch', { body })
  if (error) fail(error, '模拟调度失败')
  return data
}

// --- Auth ---

export async function fetchAuthStatus(): Promise<AuthStatus> {
  const { data, error } = await api.GET('/api/picotera/auth/status')
  if (error) fail(error, '加载认证状态失败')
  return data
}

export async function fetchMe(): Promise<SessionView> {
  const { data, error } = await api.GET('/api/picotera/me')
  if (error) fail(error, '加载会话失败')
  return data
}

/**
 * Call /auth/logout — a raw chi route that returns 204 (not registered in the
 * OpenAPI spec). Use raw fetch so we don't need a typed path entry.
 */
export async function logout(): Promise<void> {
  await fetch('/api/picotera/auth/logout', {
    method: 'POST',
    credentials: 'include',
  })
}

// --- WebAuthn ceremony endpoints (server returns raw protocol JSON) ---

/**
 * Begin a passkey login ceremony. Returns the raw WebAuthn PublicKeyCredentialRequestOptions
 * JSON from the server (not reflected in the OpenAPI spec).
 */
export async function beginLogin(): Promise<unknown> {
  const res = await fetch('/api/picotera/auth/login/begin', {
    method: 'POST',
    credentials: 'include',
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new ApiRequestError(body)
  }
  return res.json()
}

/**
 * Complete a passkey login ceremony with the assertion from the browser.
 */
export async function completeLogin(body: unknown): Promise<SessionView> {
  const res = await fetch('/api/picotera/auth/login/complete', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({}))
    throw new ApiRequestError(errBody)
  }
  return res.json() as Promise<SessionView>
}

// --- Enrollment ---

export async function fetchEnrollment(token: string): Promise<EnrollmentPreview> {
  const { data, error } = await api.GET('/api/picotera/enrollments/{token}', {
    params: { path: { token } },
  })
  if (error) fail(error, '加载邀请失败')
  return data
}

/**
 * Begin a WebAuthn registration ceremony for an enrollment token. Returns raw
 * PublicKeyCredentialCreationOptions JSON (not in OpenAPI spec).
 */
export async function beginEnrollmentRegistration(
  token: string,
  body: { username?: string; displayName?: string },
): Promise<unknown> {
  const res = await fetch(`/api/picotera/enrollments/${encodeURIComponent(token)}/register/begin`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({}))
    throw new ApiRequestError(errBody)
  }
  return res.json()
}

/**
 * Complete a WebAuthn registration ceremony for an enrollment token.
 */
export async function completeEnrollmentRegistration(
  token: string,
  attestation: unknown,
): Promise<SessionView> {
  const res = await fetch(
    `/api/picotera/enrollments/${encodeURIComponent(token)}/register/complete`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify(attestation),
    },
  )
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({}))
    throw new ApiRequestError(errBody)
  }
  return res.json() as Promise<SessionView>
}

// --- /me/credentials ---

export async function fetchMyCredentials(): Promise<CredentialView[]> {
  const { data, error } = await api.GET('/api/picotera/me/credentials')
  if (error) fail(error, '加载凭证失败')
  return data ?? []
}

/**
 * Begin a WebAuthn registration ceremony to add a new credential. Returns raw
 * PublicKeyCredentialCreationOptions JSON (not in OpenAPI spec).
 */
export async function addCredentialBegin(): Promise<unknown> {
  const res = await fetch('/api/picotera/me/credentials/register/begin', {
    method: 'POST',
    credentials: 'include',
  })
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({}))
    throw new ApiRequestError(errBody)
  }
  return res.json()
}

/**
 * Complete a WebAuthn registration ceremony to add a new credential.
 * The optional nickname is passed as a query parameter.
 */
export async function addCredentialComplete(
  attestation: unknown,
  nickname?: string,
): Promise<CredentialView> {
  const url =
    nickname && nickname.length > 0
      ? `/api/picotera/me/credentials/register/complete?nickname=${encodeURIComponent(nickname)}`
      : `/api/picotera/me/credentials/register/complete`
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    credentials: 'include',
    body: JSON.stringify(attestation),
  })
  if (!res.ok) {
    const errBody = await res.json().catch(() => ({}))
    throw new ApiRequestError(errBody)
  }
  return res.json() as Promise<CredentialView>
}

export async function deleteMyCredential(id: number): Promise<void> {
  const { error } = await api.POST('/api/picotera/me/credentials/delete', { body: { id } })
  if (error) fail(error, '删除凭证失败')
}

// --- Accounts ---

type AccountView = components['schemas']['AccountView']
type Permissions = components['schemas']['Permissions']
type InvitationResponse = components['schemas']['InvitationResponse']
type EnrollmentURLResponse = components['schemas']['EnrollmentURLResponse']

export async function listAccounts(): Promise<AccountView[]> {
  const { data, error } = await api.GET('/api/picotera/accounts')
  if (error) fail(error, '加载用户失败')
  return data ?? []
}

export async function getAccount(id: number): Promise<AccountView> {
  const { data, error } = await api.GET('/api/picotera/accounts/{id}', {
    params: { path: { id } },
  })
  if (error) fail(error, '加载用户失败')
  return data
}

export async function updateAccount(
  id: number,
  body: { displayName: string; role: string; permissions: Permissions; disabled: boolean },
): Promise<AccountView> {
  const { data, error } = await api.PUT('/api/picotera/accounts/{id}', {
    params: { path: { id } },
    body,
  })
  if (error) fail(error, '保存用户失败')
  return data
}

export async function deleteAccount(id: number): Promise<void> {
  const { error } = await api.POST('/api/picotera/accounts/delete', { body: { id } })
  if (error) fail(error, '删除用户失败')
}

export async function deleteAccountCredential(
  accountId: number,
  credentialId: number,
): Promise<void> {
  const { error } = await api.POST('/api/picotera/accounts/credentials/delete', {
    body: { accountId, credentialId },
  })
  if (error) fail(error, '删除凭证失败')
}

export async function revokeAccountSessions(id: number): Promise<{ revoked: number }> {
  const { data, error } = await api.POST('/api/picotera/accounts/revoke-sessions', {
    body: { id },
  })
  if (error) fail(error, '吊销会话失败')
  return data
}

export async function reissueEnrollment(id: number): Promise<EnrollmentURLResponse> {
  const { data, error } = await api.POST('/api/picotera/accounts/reissue-enrollment', {
    body: { id },
  })
  if (error) fail(error, '重新发送邀请失败')
  return data
}

export async function createInvitation(body: {
  role: string
  permissions: Permissions
}): Promise<InvitationResponse> {
  const { data, error } = await api.POST('/api/picotera/invitations', { body })
  if (error) fail(error, '创建邀请失败')
  return data
}

// --- Invitations ---

type InvitationView = components['schemas']['InvitationView']

export async function listInvitations(): Promise<InvitationView[]> {
  const { data, error } = await api.GET('/api/picotera/invitations')
  if (error) fail(error, '加载邀请失败')
  return data ?? []
}

export async function revokeInvitation(token: string): Promise<void> {
  const { error } = await api.POST('/api/picotera/invitations/revoke', {
    body: { token },
  })
  if (error) fail(error, '撤销邀请失败')
}

// --- Invalidation helpers ---

export function invalidateSession(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.session.all })
}

export function invalidateAuthStatus(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.authStatus.all })
}

export function invalidateOwnCredentials(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.credentials.mine })
}

export function invalidateAccounts(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.accounts.all })
}

export function invalidateEnrollment(client: QueryClient, token: string) {
  client.invalidateQueries({ queryKey: queryKeys.enrollments.detail(token) })
}

export function invalidateInvitations(client: QueryClient) {
  client.invalidateQueries({ queryKey: queryKeys.invitations.all })
}

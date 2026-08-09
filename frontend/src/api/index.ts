import useSWR from 'swr'

export const fetcher = (url: string) =>
  fetch(url).then((r) => {
    if (!r.ok) throw new Error(`${r.status} ${r.statusText}`)
    return r.json()
  })

export interface OverviewResponse {
  sessions_count: number
  users_count: number
  total_cost_usd: number
  total_input_tokens: number
  total_output_tokens: number
  total_cache_tokens: number
  daily_costs: { date: string; cost_usd: number }[]
  top_models: { model: string; span_count: number }[]
  top_tools: { tool_name: string; call_count: number }[]
}

export interface SessionItem {
  session_id: string
  user_id: string
  first_seen: string
  last_seen: string
  model: string
  cost_usd: number
  input_tokens: number
  output_tokens: number
  tool_calls: number
  status: string
}

export interface SessionsResponse {
  items: SessionItem[]
  total: number
  page: number
  limit: number
}

export interface SpanDetail {
  start_time: string
  duration_ms: number
  name: string
  tool_name?: string
  model?: string
  input_tokens?: number
  output_tokens?: number
  status: string
  attributes: string
}

export interface SessionDetailResponse {
  session_id: string
  first_seen: string
  last_seen: string
  model: string
  cost_usd: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_write_tokens: number
  spans: SpanDetail[]
}

export interface CostsResponse {
  daily: { date: string; cost_usd: number }[]
  by_model: { model: string; cost_usd: number }[]
  top_sessions: { session_id: string; cost_usd: number; first_seen: string }[]
}

export interface ToolItem {
  name: string
  calls: number
  avg_duration_ms: number
  fail_count: number
  fail_rate: number
}

export interface BashCommandItem {
  command: string
  calls: number
  avg_duration_ms: number
  fail_count: number
  fail_rate: number
}

export interface ModelItem {
  model: string
  span_count: number
  total_cost_usd: number
  total_input_tokens: number
  total_output_tokens: number
}

export interface UserRow {
  user_id: string
  is_default: boolean
  sessions: number
  total_cost_usd: number
  total_input_tokens: number
  total_output_tokens: number
  span_count: number
  last_seen: string
}

export interface UsersResponse {
  items: UserRow[]
}

// New user management types
export interface User {
  id: string
  name: string
  token: string
  created_at: string
  cost: number
  sessions: number
  last_seen: string | null
}

export interface UsersListResponse {
  users: User[]
}

export interface UsersPageResponse {
  users: User[]
  total: number
  page: number
  limit: number
  range: string
  sort: string
  order: string
}

export interface UsersPageParams {
  range: string
  q?: string
  sort: string
  order: string
  page: number
  limit: number
}

export interface SettingsResponse {
  allow_anonymous: boolean
}

// useUsers fetches every user unpaginated (limit=0) — used by the UserSearch typeahead.
export function useUsers() {
  return useSWR<UsersListResponse>('/api/v1/users', fetcher)
}

export function useUsersPage(params: UsersPageParams) {
  const qs = new URLSearchParams({
    range: params.range,
    sort: params.sort,
    order: params.order,
    page: String(params.page),
    limit: String(params.limit),
  })
  if (params.q) qs.set('q', params.q)
  return useSWR<UsersPageResponse>(`/api/v1/users?${qs.toString()}`, fetcher, { keepPreviousData: true })
}

export function useUser(id: string | undefined, range: string) {
  const qs = new URLSearchParams({ range })
  return useSWR<User>(id ? `/api/v1/users/${encodeURIComponent(id)}?${qs.toString()}` : null, fetcher)
}

export function useSettings() {
  return useSWR<SettingsResponse>('/api/v1/settings', fetcher)
}

export async function createUser(name: string): Promise<User> {
  const res = await fetch('/api/v1/users', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(text || `${res.status}`)
  }
  return res.json()
}

export async function rotateUserToken(id: string): Promise<User> {
  const res = await fetch(`/api/v1/users/${id}/rotate-token`, { method: 'POST' })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(text || `${res.status}`)
  }
  return res.json()
}

export type DeleteMode = 'user_only' | 'user_and_history'

export async function deleteUser(id: string, mode?: DeleteMode): Promise<void> {
  const url = mode ? `/api/v1/users/${id}?mode=${mode}` : `/api/v1/users/${id}`
  const res = await fetch(url, { method: 'DELETE' })
  if (!res.ok && res.status !== 204) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(text || `${res.status}`)
  }
}

export async function updateSettings(settings: Partial<SettingsResponse>): Promise<SettingsResponse> {
  const res = await fetch('/api/v1/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(settings),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(text || `${res.status}`)
  }
  return res.json()
}

export function useOverview(refreshInterval = 30_000, userId?: string) {
  const qs = userId ? `?user_id=${encodeURIComponent(userId)}` : ''
  return useSWR<OverviewResponse>(`/api/v1/overview${qs}`, fetcher, { refreshInterval })
}

export function useSessions(page = 1, limit = 50, sort = 'start_time', order = 'desc', userId?: string) {
  const params = new URLSearchParams({ page: String(page), limit: String(limit), sort, order })
  if (userId) params.set('user_id', userId)
  return useSWR<SessionsResponse>(`/api/v1/sessions?${params.toString()}`, fetcher)
}

export function useSession(id: string) {
  return useSWR<SessionDetailResponse>(id ? `/api/v1/sessions/${id}` : null, fetcher)
}

export function useCosts(from?: string, to?: string, userId?: string) {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  if (userId) params.set('user_id', userId)
  const qs = params.toString()
  return useSWR<CostsResponse>(`/api/v1/costs${qs ? `?${qs}` : ''}`, fetcher)
}

export function useTools(userId?: string) {
  const qs = userId ? `?user_id=${encodeURIComponent(userId)}` : ''
  return useSWR<{ items: ToolItem[] }>(`/api/v1/tools${qs}`, fetcher)
}

export function useBashCommands() {
  return useSWR<{ items: BashCommandItem[] }>('/api/v1/bash-commands', fetcher)
}

export function useModels(userId?: string) {
  const qs = userId ? `?user_id=${encodeURIComponent(userId)}` : ''
  return useSWR<{ items: ModelItem[] }>(`/api/v1/models${qs}`, fetcher)
}

export interface HistoryBucket {
  bucket: string
  sessions: number
  spans: number
  cost_usd: number
  input_tokens: number
  output_tokens: number
}

export interface HistoryModelRow {
  bucket: string
  model: string
  cost_usd: number
  spans: number
}

export interface HeatmapCell {
  date: string
  hour: number
  count: number
  cost_usd: number
}

export interface HistoryResponse {
  granularity: string
  from: string
  to: string
  buckets: HistoryBucket[]
  by_model: HistoryModelRow[]
  heatmap: HeatmapCell[]
}

export function useHistory(granularity: string, from?: string, to?: string, userId?: string) {
  const params = new URLSearchParams({ granularity })
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  if (userId) params.set('user_id', userId)
  return useSWR<HistoryResponse>(`/api/v1/history?${params.toString()}`, fetcher)
}

export interface TokenItem {
  id: string
  name: string
  prefix: string
  created_at: string
  last_used_at: string | null
}

export interface TokensResponse {
  items: TokenItem[]
}

export interface CreateTokenResponse {
  id: string
  name: string
  prefix: string
  token: string
  created_at: string
}

export function useTokens() {
  return useSWR<TokensResponse>('/api/v1/tokens', fetcher)
}

export async function createToken(name: string): Promise<CreateTokenResponse> {
  const res = await fetch('/api/v1/tokens', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(text || `${res.status}`)
  }
  return res.json()
}

export async function rotateToken(id: string): Promise<CreateTokenResponse> {
  const res = await fetch(`/api/v1/tokens/${id}/rotate`, { method: 'POST' })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(text || `${res.status}`)
  }
  return res.json()
}

export async function revokeToken(id: string): Promise<void> {
  const res = await fetch(`/api/v1/tokens/${id}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 204) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(text || `${res.status}`)
  }
}

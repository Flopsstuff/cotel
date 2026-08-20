import useSWR from 'swr'

export const fetcher = (url: string) =>
  fetch(url).then((r) => {
    if (!r.ok) throw new Error(`${r.status} ${r.statusText}`)
    return r.json()
  })

export interface OverviewResponse {
  range: string
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
  range: string
  // Set when the range reaches further back than raw spans go: a session row
  // needs a start time, model and status, none of which the roll-up keeps, so
  // the list answers for a shorter window than asked.
  covered_since: string | null
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
  // The range key that scoped the response, or null when explicit from/to
  // bounds superseded it.
  range: string | null
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

// ListParams is the range/search/sort/paging contract the tools and
// bash-commands lists share with the users list (ADR-0011, ADR-0012).
export interface ListParams {
  range: string
  q?: string
  sort: string
  order: string
  page: number
  limit: number
  user_id?: string
}

export type ToolsParams = ListParams

interface ListEcho {
  total: number
  page: number
  limit: number
  range: string
  sort: string
  order: string
}

export interface ToolsResponse extends ListEcho {
  items: ToolItem[]
  // Set when the duration and error figures start later than the selected
  // range, because aggregate rows predating schema v9 carry no such sums.
  duration_stats_since: string | null
}

export interface BashCommandsResponse extends ListEcho {
  items: BashCommandItem[]
  // Set when the range reaches further back than raw spans go: the breakdown is
  // computed from spans alone, so it answers for a shorter window than asked.
  covered_since: string | null
}

function listQuery(p: ListParams): string {
  const qs = new URLSearchParams({
    range: p.range,
    sort: p.sort,
    order: p.order,
    page: String(p.page),
    limit: String(p.limit),
  })
  if (p.q) qs.set('q', p.q)
  if (p.user_id) qs.set('user_id', p.user_id)
  return qs.toString()
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

// The four hooks below take range last and omit the parameter when it is
// undefined, so a caller that does not pass one keeps the server-side default
// for that endpoint (ADR-0014).
export function useOverview(refreshInterval = 30_000, userId?: string, range?: string) {
  const params = new URLSearchParams()
  if (userId) params.set('user_id', userId)
  if (range) params.set('range', range)
  const qs = params.toString()
  return useSWR<OverviewResponse>(`/api/v1/overview${qs ? `?${qs}` : ''}`, fetcher, {
    refreshInterval,
    keepPreviousData: true,
  })
}

export function useSessions(
  page = 1,
  limit = 50,
  sort = 'start_time',
  order = 'desc',
  userId?: string,
  range?: string,
) {
  const params = new URLSearchParams({ page: String(page), limit: String(limit), sort, order })
  if (userId) params.set('user_id', userId)
  if (range) params.set('range', range)
  return useSWR<SessionsResponse>(`/api/v1/sessions?${params.toString()}`, fetcher, {
    keepPreviousData: true,
  })
}

export function useSession(id: string) {
  return useSWR<SessionDetailResponse>(id ? `/api/v1/sessions/${id}` : null, fetcher)
}

export function useCosts(from?: string, to?: string, userId?: string, range?: string) {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  if (userId) params.set('user_id', userId)
  if (range) params.set('range', range)
  const qs = params.toString()
  return useSWR<CostsResponse>(`/api/v1/costs${qs ? `?${qs}` : ''}`, fetcher, {
    keepPreviousData: true,
  })
}

export function useTools(params: ToolsParams) {
  return useSWR<ToolsResponse>(`/api/v1/tools?${listQuery(params)}`, fetcher, { keepPreviousData: true })
}

export function useBashCommands(params: ListParams) {
  return useSWR<BashCommandsResponse>(`/api/v1/bash-commands?${listQuery(params)}`, fetcher, {
    keepPreviousData: true,
  })
}

export interface ModelsResponse {
  items: ModelItem[]
  range: string
}

export function useModels(userId?: string, range?: string) {
  const params = new URLSearchParams()
  if (userId) params.set('user_id', userId)
  if (range) params.set('range', range)
  const qs = params.toString()
  return useSWR<ModelsResponse>(`/api/v1/models${qs ? `?${qs}` : ''}`, fetcher, {
    keepPreviousData: true,
  })
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

import useSWR from 'swr'

export const fetcher = (url: string) =>
  fetch(url).then((r) => {
    if (!r.ok) throw new Error(`${r.status} ${r.statusText}`)
    return r.json()
  })

export interface OverviewResponse {
  sessions_count: number
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

export interface ModelItem {
  model: string
  span_count: number
  total_cost_usd: number
  total_input_tokens: number
  total_output_tokens: number
}

export function useOverview(refreshInterval = 30_000) {
  return useSWR<OverviewResponse>('/api/v1/overview', fetcher, { refreshInterval })
}

export function useSessions(page = 1, limit = 50, sort = 'start_time', order = 'desc') {
  return useSWR<SessionsResponse>(
    `/api/v1/sessions?page=${page}&limit=${limit}&sort=${sort}&order=${order}`,
    fetcher,
  )
}

export function useSession(id: string) {
  return useSWR<SessionDetailResponse>(id ? `/api/v1/sessions/${id}` : null, fetcher)
}

export function useCosts(from?: string, to?: string) {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  const qs = params.toString()
  return useSWR<CostsResponse>(`/api/v1/costs${qs ? `?${qs}` : ''}`, fetcher)
}

export function useTools() {
  return useSWR<{ items: ToolItem[] }>('/api/v1/tools', fetcher)
}

export function useModels() {
  return useSWR<{ items: ModelItem[] }>('/api/v1/models', fetcher)
}

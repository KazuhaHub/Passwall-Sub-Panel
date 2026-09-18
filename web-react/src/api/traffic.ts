import { client } from './client'

export interface UsageReport {
  user_id: number
  permanent_total_bytes: number
  period_used_bytes: number
  today_used_bytes: number
}

export interface TrafficRow extends UsageReport {
  upn: string
}

export type TrafficHistoryPeriod = 'hour' | 'day' | 'week' | 'month'

export interface TrafficHistoryItem {
  date: string
  up_bytes: number
  down_bytes: number
  total_bytes: number
}

export interface TrafficHistoryResponse {
  scope: 'all' | 'user'
  user_id?: number
  period: TrafficHistoryPeriod
  since: string
  until: string
  users_count?: number
  items: TrafficHistoryItem[]
}

export interface TrafficHistoryParams {
  user_id?: number
  period?: TrafficHistoryPeriod
  since?: string
  until?: string
  // IANA timezone name (e.g. "Asia/Shanghai", "America/Los_Angeles"). The
  // backend uses this to parse `since` / `until` and to bucket snapshots —
  // without it a browser in PT asking for "today" against a UTC server
  // drops the last 7+ hours of data. Auto-injected by `withTz` below
  // when the caller doesn't pass one, so most call sites can ignore this.
  tz?: string
}

// browser's current IANA tz name. Reads it on demand (not at module load)
// so the value reflects any user-level timezone changes between calls.
function browserTz(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  } catch {
    return 'UTC'
  }
}

function withTz<T extends TrafficHistoryParams>(params: T): T {
  return { ...params, tz: params.tz ?? browserTz() }
}

export async function topTraffic(
  limit = 20,
  opts: { silent?: boolean; signal?: AbortSignal } = {},
) {
  // `silent` flips the axios global error toast off for callers that
  // treat this as best-effort enrichment (DashboardView's top-5 widget,
  // UsersView's per-row usage column). Without it, a transient failure
  // on these auxiliary fetches surfaces "Network error" toasts on
  // pages whose primary data loaded fine, confusing the admin.
  const { data } = await client.get<{ items: TrafficRow[] }>('/admin/traffic/top', {
    params: { limit },
    _skipErrorToast: opts.silent,
    signal: opts.signal,
  })
  return data.items
}

export async function trafficHistory(params: TrafficHistoryParams = {}, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<TrafficHistoryResponse>('/admin/traffic/history', {
    params: withTz(params),
    signal: opts.signal,
  })
  return data
}

export async function userTrafficHistory(userId: number, params: TrafficHistoryParams = {}, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<TrafficHistoryResponse>(`/admin/traffic/user/${userId}/history`, {
    params: withTz(params),
    signal: opts.signal,
  })
  return data
}

export async function userTraffic(userId: number) {
  const { data } = await client.get<UsageReport>(`/admin/traffic/user/${userId}`)
  return data
}

export async function setUserTraffic(userId: number, periodUsedGB: number) {
  const { data } = await client.put<UsageReport>(`/admin/traffic/user/${userId}`, {
    period_used_gb: periodUsedGB,
  })
  return data
}

export async function pollTrafficNow() {
  await client.post('/admin/traffic/poll')
}

export interface NodeTrafficRow {
  node_id: number
  display_name: string
  panel_name: string
  region: string
  tags: string[]
  permanent_total_bytes: number
  period_used_bytes: number
  today_used_bytes: number
}

export async function topNodes(limit = 20, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<{ items: NodeTrafficRow[] }>('/admin/traffic/nodes/top', {
    params: { limit },
    signal: opts.signal,
  })
  return data.items
}

export async function nodeTrafficHistory(
  params: TrafficHistoryParams & { node_id?: number } = {},
  opts: { signal?: AbortSignal } = {},
) {
  const { data } = await client.get<TrafficHistoryResponse>('/admin/traffic/nodes/history', {
    params: withTz(params),
    signal: opts.signal,
  })
  return data
}

// UserNodeUsageRow is one (user, node) usage line — lifetime / current-period
// / today, each split up+down. Returned sorted by period usage desc.
export interface UserNodeUsageRow {
  node_id: number
  display_name: string
  panel_name: string
  region: string
  client_email: string
  lifetime_up_bytes: number
  lifetime_down_bytes: number
  lifetime_total_bytes: number
  period_up_bytes: number
  period_down_bytes: number
  period_total_bytes: number
  today_up_bytes: number
  today_down_bytes: number
  today_total_bytes: number
}

export async function getUserNodeUsage(userId: number, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<{ items: UserNodeUsageRow[] }>(`/admin/traffic/user/${userId}/nodes`, {
    signal: opts.signal,
  })
  return data.items ?? []
}

// UserServerUsageRow is one (user, server/panel) usage line — the per-node rows
// aggregated by 3X-UI server. Returned sorted by period usage desc.
export interface UserServerUsageRow {
  panel_id: number
  server_name: string
  node_count: number
  lifetime_up_bytes: number
  lifetime_down_bytes: number
  lifetime_total_bytes: number
  period_up_bytes: number
  period_down_bytes: number
  period_total_bytes: number
  today_up_bytes: number
  today_down_bytes: number
  today_total_bytes: number
}

export async function getUserServerUsage(userId: number, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<{ items: UserServerUsageRow[] }>(`/admin/traffic/user/${userId}/servers`, {
    signal: opts.signal,
  })
  return data.items ?? []
}

export async function getMyUsage(opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<UsageReport>('/user/me/traffic', { signal: opts.signal })
  return data
}

export async function getMyTrafficHistory(params: TrafficHistoryParams = {}) {
  const { data } = await client.get<TrafficHistoryResponse>('/user/me/traffic/history', { params: withTz(params) })
  return data
}

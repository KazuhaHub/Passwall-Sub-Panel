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

export type TrafficHistoryPeriod = 'day' | 'week' | 'month'

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
}

export async function getMyUsage() {
  const { data } = await client.get<UsageReport>('/user/me/traffic')
  return data
}

export async function getMyTrafficHistory(params: TrafficHistoryParams = {}) {
  const { data } = await client.get<TrafficHistoryResponse>('/user/me/traffic/history', { params })
  return data
}

export async function getMyProfile() {
  const { data } = await client.get('/user/me')
  return data
}

export async function topTraffic(limit = 20) {
  const { data } = await client.get<{ items: TrafficRow[] }>('/admin/traffic/top', {
    params: { limit },
  })
  return data.items
}

export async function trafficHistory(params: TrafficHistoryParams = {}) {
  const { data } = await client.get<TrafficHistoryResponse>('/admin/traffic/history', { params })
  return data
}

export async function pollTrafficNow() {
  await client.post('/admin/traffic/poll')
}

export async function userTrafficHistory(userId: number, params: TrafficHistoryParams = {}) {
  const { data } = await client.get<TrafficHistoryResponse>(`/admin/traffic/user/${userId}/history`, { params })
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

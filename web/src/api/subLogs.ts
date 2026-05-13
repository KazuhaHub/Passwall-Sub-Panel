import { client } from './client'

export interface SubLog {
  id: number
  user_id: number
  user_upn?: string
  user_display?: string
  user_group_id?: number
  ip: string
  ua: string
  client_type: string
  accessed_at: string
}

export interface SubLogListResponse {
  items: SubLog[]
  total: number
}

export interface SubLogFilter {
  page?: number
  page_size?: number
  user_id?: number
  since?: string
  until?: string
}

export async function getSubLogs(filter?: SubLogFilter) {
  const params = new URLSearchParams()
  if (filter?.page) params.set('page', String(filter.page))
  if (filter?.page_size) params.set('page_size', String(filter.page_size))
  if (filter?.user_id) params.set('user_id', String(filter.user_id))
  if (filter?.since) params.set('since', filter.since)
  if (filter?.until) params.set('until', filter.until)
  const { data } = await client.get<SubLogListResponse>(`/admin/sub-logs?${params.toString()}`)
  return data
}

export async function clearSubLogs() {
  await client.delete('/admin/sub-logs')
}

export async function purgeSubLogs() {
  const { data } = await client.post<{ deleted: number }>('/admin/sub-logs/purge')
  return data
}

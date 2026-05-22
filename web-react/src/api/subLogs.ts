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
  /** Case-insensitive substring across ip / ua / client_type / upn / display. */
  search?: string
  since?: string
  until?: string
}

export async function getSubLogs(filter: SubLogFilter = {}) {
  const { data } = await client.get<SubLogListResponse>('/admin/sub-logs', { params: filter })
  return data
}

export async function clearSubLogs() {
  await client.delete('/admin/sub-logs')
}

export async function purgeSubLogs() {
  const { data } = await client.post<{ deleted: number }>('/admin/sub-logs/purge')
  return data
}

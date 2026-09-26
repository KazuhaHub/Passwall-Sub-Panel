import { client } from './client'

/** Resolved IP geolocation (offline mmdb). Empty/absent when geo is disabled
 * or the IP couldn't be resolved. */
export interface GeoLocation {
  country_code: string
  country: string
  region: string
  city: string
}

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
  region?: GeoLocation
  /**
   * The device the fetch declared through x-hwid, for ADMINS ONLY: the server
   * leaves both out for anyone else, and for a fetch that declared nothing.
   * The label is a sanitized OS / version / model string; the id is the
   * first 4 characters of a keyed per-account digest — enough to tell one
   * account's devices apart, useless for anything else.
   */
  device_label?: string
  device_id4?: string
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

export async function getSubLogs(filter: SubLogFilter = {}, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<SubLogListResponse>('/admin/sub-logs', {
    params: filter,
    signal: opts.signal,
  })
  return data
}

export async function clearSubLogs() {
  await client.delete('/admin/sub-logs')
}

export async function purgeSubLogs() {
  const { data } = await client.post<{ deleted: number }>('/admin/sub-logs/purge')
  return data
}

import { client } from './client'
import type { GeoLocation } from './subLogs'

export interface AuditEntry {
  id: number
  actor: string
  action: string
  target: string
  before_json: string
  after_json: string
  ip: string
  at: string
  region?: GeoLocation
}

export interface AuditFilter {
  page?: number
  page_size?: number
  /** Case-insensitive substring matched across actor / action / target. */
  search?: string
  since?: string
  until?: string
}

export async function listAudit(params: AuditFilter = {}, opts: { signal?: AbortSignal } = {}) {
  const { data } = await client.get<{ items: AuditEntry[]; total: number }>('/admin/audit', {
    params,
    signal: opts.signal,
  })
  return data
}

export async function clearAudit() {
  await client.delete('/admin/audit')
}

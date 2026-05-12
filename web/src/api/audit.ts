import { client } from './client'

export interface AuditEntry {
  id: number
  actor: string
  action: string
  target: string
  before_json: string
  after_json: string
  ip: string
  at: string
}

export interface AuditFilter {
  page?: number
  page_size?: number
  actor?: string
  action?: string
  since?: string
  until?: string
}

export async function listAudit(params: AuditFilter = {}) {
  const { data } = await client.get<{ items: AuditEntry[]; total: number }>(
    '/admin/audit',
    { params },
  )
  return data
}

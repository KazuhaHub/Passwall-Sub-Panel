import { client } from './client'
import type { ReadOptions } from './requestOptions'
import type { GeoLocation } from './subLogs'

export type AuthMethod = 'local' | 'saml' | 'oidc'
export type AuthOutcome = 'success' | 'failure'

export interface AuthEvent {
  id: number
  user_id?: number
  upn: string
  method: AuthMethod
  outcome: AuthOutcome
  /** Failure reason code; empty on success. */
  reason?: string
  ip: string
  ua?: string
  at: string
  /** Resolved IP region (offline geo); absent when geo is off / IP unmapped. */
  region?: GeoLocation
}

export interface AuthEventFilter {
  page?: number
  page_size?: number
  method?: AuthMethod | ''
  outcome?: AuthOutcome | ''
  user_id?: number
  /** Case-insensitive substring across upn / ip / ua / reason. */
  search?: string
  since?: string
  until?: string
}

export async function listAuthEvents(params: AuthEventFilter = {}, opts: ReadOptions = {}) {
  const { data } = await client.get<{ items: AuthEvent[]; total: number }>('/admin/auth-events', {
    params,
    signal: opts.signal,
  })
  return data
}

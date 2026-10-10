import { client } from './client'
import type { ReadOptions } from './requestOptions'
import type { DestinationAuditLosses } from './accessControl'

export type DestinationHitGroupBy = 'none' | 'site' | 'user' | 'policy'

export interface DestinationHitQuery {
  user_id?: number
  panel_id?: number
  source?: string
  source_kind?: 'policy' | 'group'
  action?: 'block' | 'observe'
  since: number
  until: number
  group_by: DestinationHitGroupBy
  include_trial: boolean
  page: number
  page_size: number
  q?: string
}

export interface DestinationHitRecord {
  hour: number
  panel_id: number
  panel_name: string | null
  user_id: number
  user_upn: string | null
  source: string
  source_name: string | null
  action: 'block' | 'observe'
  dest: string
  port: number
  count: number
  first_at: number
  last_at: number
}

export interface DestinationHitGroup {
  key: string
  name: string | null
  count: number
  user_count: number
  source_count: number
  last_at: number
}

interface DestinationHitsMetadata {
  total: number
  page: number
  page_size: number
  summary: { block: number; deny: number; observe: number; users: number }
  sources: Array<{ source: string; name: string | null }>
  dropped_in_range: number
  losses: DestinationAuditLosses
}

export type DestinationHitsPage = DestinationHitsMetadata & (
  | { group_by: 'none'; items: DestinationHitRecord[] }
  | { group_by: Exclude<DestinationHitGroupBy, 'none'>; items: DestinationHitGroup[] }
)

export async function getDestinationHits(input: DestinationHitQuery, opts: ReadOptions = {}): Promise<DestinationHitsPage> {
  const params: Record<string, string | number> = { include_trial: input.include_trial ? 1 : 0 }
  // Keep the HTTP query narrower than component and shared-link state.
  for (const key of ['user_id', 'panel_id', 'source', 'source_kind', 'action', 'since', 'until', 'group_by', 'page', 'page_size', 'q'] as const) {
    if (input[key] !== undefined) params[key] = input[key]
  }
  const { data } = await client.get<DestinationHitsPage>('/admin/dest/hits', {
    params, signal: opts.signal, _skipErrorToast: opts.silent,
  })
  return data
}

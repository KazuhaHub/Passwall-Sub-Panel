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

const object = (value: unknown): value is Record<string, unknown> => typeof value === 'object' && value !== null && !Array.isArray(value)
const count = (value: unknown) => typeof value === 'number' && Number.isInteger(value) && value >= 0
const label = (value: unknown) => value === null || typeof value === 'string'
const instant = (value: unknown) => typeof value === 'number' && Number.isSafeInteger(value) && value > 0 && value <= 8640000000000000
const source = (value: unknown) => typeof value === 'string' && /^[pg][1-9]\d*$/.test(value) && Number.isSafeInteger(Number(value.slice(1)))

function validHitPage(value: unknown, input: DestinationHitQuery): value is DestinationHitsPage {
  if (!object(value) || value.group_by !== input.group_by || !count(value.total) || value.page !== input.page || value.page_size !== input.page_size ||
    !Array.isArray(value.items) || value.items.length > input.page_size || !object(value.summary) || !object(value.losses) || !Array.isArray(value.sources)) return false
  const summary = value.summary, losses = value.losses
  if (!['block', 'deny', 'observe', 'users'].every(key => count(summary[key])) || !['rows', 'events', 'unmatched'].every(key => count(losses[key])) ||
    losses.scope !== 'panel' || losses.complete !== false || value.dropped_in_range !== losses.rows ||
    !value.sources.every(item => object(item) && source(item.source) && label(item.name))) return false
  return value.items.every(item => {
    if (!object(item) || !count(item.count)) return false
    if (input.group_by !== 'none') {
      if (typeof item.key !== 'string' || !item.key || !label(item.name) || !count(item.user_count) || !count(item.source_count) || !instant(item.last_at)) return false
      if (input.group_by === 'user') return /^(0|[1-9]\d*)$/.test(item.key) && Number.isSafeInteger(Number(item.key))
      return input.group_by !== 'policy' || source(item.key)
    }
    return instant(item.hour) && instant(item.first_at) && instant(item.last_at) && Number(item.first_at) <= Number(item.last_at) &&
      Number.isSafeInteger(item.panel_id) && Number(item.panel_id) > 0 && Number.isSafeInteger(item.user_id) && Number(item.user_id) >= 0 &&
      source(item.source) && label(item.source_name) && label(item.panel_name) && label(item.user_upn) &&
      (item.action === 'block' || item.action === 'observe') && typeof item.dest === 'string' && !!item.dest && count(item.port) && Number(item.port) <= 65535
  })
}

export async function getDestinationHits(input: DestinationHitQuery, opts: ReadOptions = {}): Promise<DestinationHitsPage> {
  const params: Record<string, string | number> = { include_trial: input.include_trial ? 1 : 0 }
  // Keep the HTTP query narrower than component and shared-link state.
  for (const key of ['user_id', 'panel_id', 'source', 'source_kind', 'action', 'since', 'until', 'group_by', 'page', 'page_size', 'q'] as const) {
    if (input[key] !== undefined) params[key] = input[key]
  }
  const { data } = await client.get<unknown>('/admin/dest/hits', {
    params, signal: opts.signal, _skipErrorToast: opts.silent,
  })
  // Older routers may return a successful SPA document for an unknown API.
  // Only a valid incomplete-history envelope can become visible records.
  if (!validHitPage(data, input)) throw new Error('destination records unavailable')
  return data
}

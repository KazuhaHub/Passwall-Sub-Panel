import type { DestinationHitGroupBy, DestinationHitQuery as HitRequest } from '@/api/destinationHits'
export type { DestinationHitQuery as HitRequest } from '@/api/destinationHits'

export interface RecordsFilters {
  user_id?: number
  panel_id?: number
  source?: string
  action?: 'block' | 'deny' | 'observe'
  since: string
  until?: string
  group_by: DestinationHitGroupBy
  include_trial: boolean
  page: number
  page_size: number
}

export const RECORDS_PAGE_SIZES = [25, 50, 100] as const

const HOUR = 3600000, DAY = 24 * HOUR
const LOCAL_TIME = /^(\d{4})-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])T([01]\d|2[0-3]):([0-5]\d)(?::([0-5]\d)(?:\.(\d{1,3}))?)?$/
const names = { user_id: 'rec_user', panel_id: 'rec_panel', source: 'rec_source', action: 'rec_action', since: 'rec_since', until: 'rec_until',
  group_by: 'rec_group_by', include_trial: 'rec_trial', page: 'rec_page', page_size: 'rec_size' } as const
const keys = Object.keys(names) as Array<keyof RecordsFilters>
const defaults: RecordsFilters = { since: '24h', group_by: 'none', include_trial: false, page: 1, page_size: 50 }

function positive(raw: string | null): number | undefined {
  if (!raw || !/^\d+$/.test(raw)) return undefined
  const n = Number(raw)
  return Number.isSafeInteger(n) && n > 0 ? n : undefined
}

function localTime(raw: string): number | null {
  const match = LOCAL_TIME.exec(raw)
  if (!match) return null
  const d = new Date(raw), ms = d.getTime()
  // Date() normalizes impossible dates and browser-local DST gaps. A
  // datetime-local filter must describe the time the input actually shows.
  if (!Number.isFinite(ms) || ms <= 0 || d.getFullYear() !== Number(match[1]) || d.getMonth() + 1 !== Number(match[2]) ||
    d.getDate() !== Number(match[3]) || d.getHours() !== Number(match[4]) || d.getMinutes() !== Number(match[5]) ||
    d.getSeconds() !== Number(match[6] ?? 0) || d.getMilliseconds() !== Number((match[7] ?? '').padEnd(3, '0'))) return null
  return ms
}

function relativeTime(raw: string, maximumDays = 31): number | null {
  if (raw === '1h') return HOUR
  if (raw === '24h') return DAY
  const match = /^([1-9]\d*)d$/.exec(raw)
  const days = match ? Number(match[1]) : 0
  return days >= 1 && days <= maximumDays ? days * DAY : null
}

export function parseRecordsParams(params: URLSearchParams, retentionDays: number): RecordsFilters {
  const maxDays = Math.min(31, Number.isFinite(retentionDays) && retentionDays > 0 ? Math.max(1, Math.floor(retentionDays)) : 30)
  const user = positive(params.get('rec_user')), panel = positive(params.get('rec_panel'))
  const rawSource = params.get('rec_source') ?? ''
  const source = /^[pg][1-9]\d*$/.test(rawSource) && positive(rawSource.slice(1)) ? rawSource : undefined
  const action = params.get('rec_action'), group = params.get('rec_group_by')
  const rawSince = params.get('rec_since') ?? '', rawUntil = params.get('rec_until') ?? ''
  const since = relativeTime(rawSince, maxDays) !== null || localTime(rawSince) !== null ? rawSince : '24h'
  const custom = localTime(since) !== null
  const size = positive(params.get('rec_size'))
  return { ...defaults, ...(user ? { user_id: user } : {}), ...(panel ? { panel_id: panel } : {}), ...(source ? { source } : {}),
    ...(action === 'block' || action === 'deny' || action === 'observe' ? { action } : {}), since,
    ...(custom && localTime(rawUntil) !== null ? { until: rawUntil } : {}),
    group_by: group === 'site' || group === 'user' || group === 'policy' ? group : 'none',
    include_trial: params.get('rec_trial') === '1', page: positive(params.get('rec_page')) ?? 1,
    page_size: size !== undefined && (RECORDS_PAGE_SIZES as readonly number[]).includes(size) ? size : 50 }
}

// Only these typed filters can enter browser history. Search belongs to
// component state and recordsRequest; stale search keys are removed on edits.
// Parsing never rewrites a shared link, and neither helper mutates its input.
export function recordsSearch(prev: URLSearchParams, patch: Partial<RecordsFilters>, retentionDays: number): URLSearchParams {
  const out = new URLSearchParams(prev)
  out.delete('rec_q'); out.delete('q')
  for (const key of keys) {
    if (!(key in patch)) continue
    const value = patch[key]
    if (value === undefined || value === false) out.delete(names[key])
    else out.set(names[key], value === true ? '1' : String(value))
  }
  const normalized = parseRecordsParams(out, retentionDays)
  for (const key of keys) {
    if (!(key in patch)) continue
    const value = normalized[key]
    if (value === undefined || value === defaults[key]) out.delete(names[key])
    else out.set(names[key], value === true ? '1' : String(value))
  }
  if (relativeTime(normalized.since) !== null) out.delete('rec_until')
  if (keys.some(key => key !== 'page' && key in patch)) out.delete('rec_page')
  return out
}

// The transport uses instants; custom inputs and shared links use browser-local
// text. Relative windows are resolved once at request time, not at URL parsing.
export function recordsRequest(filters: RecordsFilters, keyword: string, now: number): HitRequest | null {
  if (!Number.isSafeInteger(now) || now <= 0) return null
  const relative = relativeTime(filters.since)
  const since = relative !== null ? now - relative : localTime(filters.since)
  const until = relative !== null || !filters.until ? now : localTime(filters.until)
  if (since === null || until === null || since <= 0 || until <= since || until - since > 31 * DAY) return null
  const q = keyword.trim()
  return { ...(filters.user_id ? { user_id: filters.user_id } : {}), ...(filters.panel_id ? { panel_id: filters.panel_id } : {}),
    ...(filters.source ? { source: filters.source } : {}),
    ...(filters.action ? { action: filters.action === 'observe' ? 'observe' : 'block', source_kind: filters.action === 'deny' ? 'group' : 'policy' } : {}),
    since, until, group_by: filters.group_by, include_trial: filters.include_trial, page: filters.page, page_size: filters.page_size,
    ...(q ? { q } : {}) }
}

import type { DestHitsParams } from '@/api/accessControl'
import { parseUserId } from '@/hooks/useDrawerParam'

// THE RECORDS TAB'S URL (plan S12, `recordsParams.ts`). Every filter is a
// `rec_*` param; the default of each is not written, so the bare tab is the
// default view and a copied link says only what differs from it. There is NO
// search param: the keyword a destination is looked up by is component state
// and a request parameter, never part of the page URL (R12) — nothing in
// here can write one. Pure: parse, build and convert, no router.

export const REC_ACTIONS = ['block', 'observe'] as const
/** `block` is "blocked"; `observe` is a report-only policy's "would have blocked". The URL keeps the wire word. */
export type RecAction = (typeof REC_ACTIONS)[number]

export const REC_GROUP_BY = ['none', 'site', 'user', 'policy'] as const
export type RecGroupBy = (typeof REC_GROUP_BY)[number]

export const REC_PAGE_SIZES = [25, 50, 100, 200] as const
export const REC_DEFAULT_SIZE = 50
export const REC_DEFAULT_SINCE = '24h'

/** The longest range the server answers (handler maxHitsRange). */
export const REC_MAX_RANGE_DAYS = 31

export interface RecordsFilter {
  user_id?: number
  panel_id?: number
  /** p<policy id> or s<inbound node id>. */
  source?: string
  action?: RecAction
  /** `1h`, `24h`, `<N>d`, or browser-local datetime text (then `until` may be set). */
  since: string
  until?: string
  group_by: RecGroupBy
  page: number
  page_size: number
}

const LOCAL_TIME = /^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])T([01]\d|2[0-3]):[0-5]\d(:[0-5]\d(\.\d{1,3})?)?$/
const RELATIVE = /^(\d{1,3})d$/
const SOURCE = /^[ps]\d{1,12}$/

function oneOf<T extends string>(raw: string | null, allowed: readonly T[]): T | undefined {
  return raw !== null && (allowed as readonly string[]).includes(raw) ? raw as T : undefined
}

function validSince(raw: string): boolean {
  if (raw === '1h' || raw === '24h') return true
  const m = RELATIVE.exec(raw)
  if (m) return Number(m[1]) >= 1
  return LOCAL_TIME.test(raw)
}

export function parseRecordsParams(params: URLSearchParams): RecordsFilter {
  const user = parseUserId(params.get('rec_user'))
  const panel = parseUserId(params.get('rec_panel'))
  const rawSource = params.get('rec_source') ?? ''
  const action = oneOf(params.get('rec_action'), REC_ACTIONS)
  const sinceRaw = params.get('rec_since') ?? ''
  const since = validSince(sinceRaw) ? sinceRaw : REC_DEFAULT_SINCE
  const until = params.get('rec_until') ?? ''
  const page = Number(params.get('rec_page'))
  const size = Number(params.get('rec_size'))
  return {
    ...(user ? { user_id: user } : {}),
    ...(panel ? { panel_id: panel } : {}),
    ...(SOURCE.test(rawSource) ? { source: rawSource } : {}),
    ...(action ? { action } : {}),
    since,
    // An end without an absolute start bounds nothing.
    ...(LOCAL_TIME.test(since) && LOCAL_TIME.test(until) ? { until } : {}),
    group_by: oneOf(params.get('rec_group_by'), REC_GROUP_BY) ?? 'none',
    page: Number.isInteger(page) && page > 1 ? page : 1,
    page_size: (REC_PAGE_SIZES as readonly number[]).includes(size) ? size : REC_DEFAULT_SIZE,
  }
}

/**
 * The URL after a change to the filters: only what differs from the default is
 * written, and any change other than the page itself returns to page 1 (the
 * page `patch` carries, if any, is the one written).
 */
export function recordsSearch(prev: URLSearchParams, patch: Partial<RecordsFilter>): URLSearchParams {
  const out = new URLSearchParams(prev)
  const put = (name: string, value: string | undefined) => {
    if (value) out.set(name, value)
    else out.delete(name)
  }
  if ('user_id' in patch) put('rec_user', patch.user_id ? String(patch.user_id) : undefined)
  if ('panel_id' in patch) put('rec_panel', patch.panel_id ? String(patch.panel_id) : undefined)
  if ('source' in patch) put('rec_source', patch.source)
  if ('action' in patch) put('rec_action', patch.action)
  if ('since' in patch) put('rec_since', patch.since === REC_DEFAULT_SINCE ? undefined : patch.since)
  if ('until' in patch) put('rec_until', patch.until)
  if ('group_by' in patch) put('rec_group_by', patch.group_by === 'none' ? undefined : patch.group_by)
  if ('page_size' in patch) put('rec_size', patch.page_size === REC_DEFAULT_SIZE ? undefined : String(patch.page_size ?? ''))
  if ('page' in patch) put('rec_page', patch.page && patch.page > 1 ? String(patch.page) : undefined)
  else out.delete('rec_page')
  return out
}

/** How many filters are in force (the phone's badge): who, where, which rule and what happened. Not the view, the page or the range. */
export function recordsFilterCount(f: RecordsFilter): number {
  return [f.user_id, f.panel_id, f.source, f.action].filter(v => v !== undefined).length
}

/**
 * The relative-range word for "the last `days` days", held to the retention
 * (R14: a fixed window is `min(window, retention)`, written with the days it
 * really is). One day is spelled `24h`.
 */
export function relativeSince(window: number, retentionDays: number | undefined): string {
  const days = retentionDays && retentionDays > 0 ? Math.min(window, retentionDays) : window
  return days <= 1 ? '24h' : `${days}d`
}

const HOUR = 3_600_000
const DAY = 24 * HOUR

/** The request of GET /dest/hits for a filter. `q` is request state: it is added here, from the component, and never read from or written to the URL. */
/** The first hour boundary after t: the end of the hour bucket t falls in. */
function nextHour(t: number): number {
  return Math.floor(t / HOUR) * HOUR + HOUR
}

export function recordsApiParams(
  f: RecordsFilter,
  ctx: { now: number; retentionDays: number | undefined; q?: string },
): DestHitsParams {
  const out: DestHitsParams = {}
  if (f.user_id) out.user_id = f.user_id
  if (f.panel_id) out.panel_id = f.panel_id
  if (f.source) out.source = f.source
  if (f.action) out.action = f.action

  const cap = REC_MAX_RANGE_DAYS * DAY
  if (LOCAL_TIME.test(f.since)) {
    const until = f.until && LOCAL_TIME.test(f.until) ? Date.parse(f.until) : undefined
    const since = Date.parse(f.since)
    const end = until ?? nextHour(ctx.now)
    out.since = Math.max(since, end - cap)
    // Always sent: left out, the server ends the range at now + 1h and a 31-day span becomes one hour too long (dest_hits_range).
    out.until = end
  } else {
    const days = f.since === '1h' ? 1 / 24 : f.since === '24h' ? 1 : Number(RELATIVE.exec(f.since)?.[1] ?? 1)
    const window = Math.min(days, ctx.retentionDays && ctx.retentionDays > 0 ? ctx.retentionDays : days, REC_MAX_RANGE_DAYS)
    // Ends at the next hour boundary so the current hour's bucket is in range,
    // and the span still never exceeds the server's 31 days.
    out.until = nextHour(ctx.now)
    out.since = Math.max(ctx.now - Math.round(window * DAY), out.until - cap)
  }

  const q = ctx.q?.trim()
  if (q) out.q = q
  if (f.group_by !== 'none') out.group_by = f.group_by
  if (f.page > 1) out.page = f.page
  if (f.page_size !== REC_DEFAULT_SIZE) out.page_size = f.page_size
  return out
}

/**
 * The query string that opens the records tab on a filter (a link from a
 * policy row, an account drawer, the risk center). Relative ranges only: a
 * link carries "the last 7 days", never a moment, and never a search term.
 */
export function recordsHref(patch: Partial<Pick<RecordsFilter, 'user_id' | 'panel_id' | 'source' | 'action' | 'since' | 'group_by'>>): string {
  return `?${recordsSearch(new URLSearchParams({ tab: 'records' }), patch).toString()}`
}

import {
  EXCLUSION_EXCLUDED, EXCLUSION_KEPT, EXCLUSION_REASONS, FLAG_EVENTS, FLAG_LEVELS, QUEUE_SOURCE_FILTERS,
  type LiveParams, type QueueParams, type QueueStatus,
} from '@/api/riskCenter'
import { FLAG_SOURCES } from '@/utils/riskCenter'

// THE RISK CENTER'S URL. The page, its tabs' filters and the drawer's
// account all live in the query string, so a copied link, a reload, the bell
// and Back all mean the same view. Each tab's params carry their own prefix
// (none for the queue, the default tab; `live_` for the live view, `rec_` for
// the records), so a tab switch keeps every tab's filters. Pure: parse and
// build, no router.

export const RISK_TABS = ['queue', 'live', 'records'] as const
export type RiskTab = (typeof RISK_TABS)[number]

/** Anything else reads as the queue: the page opens on what needs a look. */
export function parseRiskTab(raw: string | null): RiskTab {
  return (RISK_TABS as readonly string[]).includes(raw ?? '') ? raw as RiskTab : 'queue'
}

/**
 * An account id from the URL, or null for anything that is not a positive
 * safe integer: a malformed link asks for nobody rather than for #0 or for
 * whatever Number() makes of the text ("7.5", "07x", "-7").
 */
export function parseUserId(raw: string | null): number | null {
  if (!raw || !/^\d+$/.test(raw)) return null
  const id = Number(raw)
  return Number.isSafeInteger(id) && id > 0 ? id : null
}

/**
 * Where a link written for the old five tabs lands now, as a search string,
 * or null when the link is not an old one. The page renders the redirect
 * BEFORE mounting any tab, so nothing is read with the old link's meaning
 * and the landing is one replace:
 *
 *   geo          → the queue narrowed to the location detector (a source
 *                  the link already names is kept instead)
 *   risk         → the queue: the risk kinds are its rows now
 *   flags        → records
 *   connections  → live
 *   user&id=N    → the queue with N's drawer open; without a valid id, the
 *                  queue — the header's picker opens any account from there
 *
 * Every other param travels with the link. The lookup's `id` never does: it
 * either became the drawer's `user` or named nobody.
 */
export function legacyRedirect(params: URLSearchParams): string | null {
  const out = new URLSearchParams(params)
  const id = parseUserId(params.get('id'))
  out.delete('id')
  switch (params.get('tab')) {
    case 'geo':
      out.set('tab', 'queue')
      if (!out.get('source')) out.set('source', 'geo')
      break
    case 'risk':
      out.set('tab', 'queue')
      break
    case 'flags':
      out.set('tab', 'records')
      break
    case 'connections':
      out.set('tab', 'live')
      break
    case 'user':
      out.set('tab', 'queue')
      if (id) out.set('user', String(id))
      break
    default:
      return null
  }
  return `?${out.toString()}`
}

const QUEUE_STATUSES: readonly QueueStatus[] = ['open', 'dismissed', 'trusted', 'all']
export const QUEUE_PAGE_SIZES = [25, 50, 100]
const QUEUE_DEFAULT_SIZE = 25
const LIVE_PAGE_SIZES = [10, 25, 50, 100]
const LIVE_DEFAULT_SIZE = 25

/** A positive page number, else the first page. */
function pageOf(raw: string | null): number {
  return parseUserId(raw) ?? 1
}

/** One of the offered sizes, else the default. */
function sizeOf(raw: string | null, sizes: readonly number[], def: number): number {
  const n = parseUserId(raw)
  return n !== null && sizes.includes(n) ? n : def
}

/**
 * The queue's sources as one comma list: only the filters the queue offers,
 * each once, in their canonical order — so two spellings of one filter are
 * one query key, and geo_auto (shown by its card and chip, D18) or a source
 * this build does not know filters nothing. '' for none.
 */
function normalizeSources(raw: string | null | undefined): string {
  const asked = new Set((raw ?? '').split(',').map(s => s.trim()))
  return QUEUE_SOURCE_FILTERS.filter(s => asked.has(s)).join(',')
}

/**
 * The queue's request, read off the URL: `status` (omitted when open),
 * `source`, `level`, `auto=1`, `urgent=1`, `q`, `page`, `size`. Only what is
 * set is present, so the query key names exactly the filters in force.
 */
export function parseQueueParams(params: URLSearchParams): QueueParams {
  const status = params.get('status') ?? ''
  const level = params.get('level')
  const source = normalizeSources(params.get('source'))
  const q = (params.get('q') ?? '').trim()
  return {
    status: (QUEUE_STATUSES as readonly string[]).includes(status) ? status as QueueStatus : 'open',
    ...(source ? { source } : {}),
    ...(level === 'flagged' || level === 'suspect' ? { level } : {}),
    ...(params.get('auto') === '1' ? { auto_suspended: true } : {}),
    ...(params.get('urgent') === '1' ? { urgent: true } : {}),
    ...(q ? { q } : {}),
    page: pageOf(params.get('page')),
    page_size: sizeOf(params.get('size'), QUEUE_PAGE_SIZES, QUEUE_DEFAULT_SIZE),
  }
}

/**
 * The URL after a change to the queue's filters. Every key of `patch` is
 * written (an empty or default value removes its param); every other param —
 * the tab, the drawer's user, the other tabs' filters — is kept. Any change
 * but the page itself starts again at the first page: page 3 of one filter
 * is not a page of another.
 */
export function queueSearch(prev: URLSearchParams, patch: Partial<QueueParams>): URLSearchParams {
  const out = new URLSearchParams(prev)
  const put = (name: string, value: string | undefined) => {
    if (value) out.set(name, value)
    else out.delete(name)
  }
  if ('status' in patch) put('status', patch.status === 'open' ? undefined : patch.status)
  if ('source' in patch) put('source', normalizeSources(patch.source))
  if ('level' in patch) put('level', patch.level)
  if ('auto_suspended' in patch) put('auto', patch.auto_suspended ? '1' : undefined)
  if ('urgent' in patch) put('urgent', patch.urgent ? '1' : undefined)
  if ('q' in patch) put('q', patch.q?.trim())
  if ('page_size' in patch) {
    put('size', patch.page_size === QUEUE_DEFAULT_SIZE ? undefined : String(patch.page_size ?? ''))
  }
  if ('page' in patch) put('page', patch.page && patch.page > 1 ? String(patch.page) : undefined)
  else out.delete('page')
  return out
}

/** The live view's exclusion filter values: the two groupings and each
 *  reason. */
const LIVE_EXCLUSIONS: readonly string[] = [EXCLUSION_KEPT, EXCLUSION_EXCLUDED, ...EXCLUSION_REASONS]

/** The live view's request, read off its `live_` params. */
export function parseLiveParams(params: URLSearchParams): LiveParams {
  const user = parseUserId(params.get('live_user'))
  const panel = parseUserId(params.get('live_panel'))
  const excl = params.get('live_excl') ?? ''
  return {
    page: pageOf(params.get('live_page')),
    page_size: sizeOf(params.get('live_size'), LIVE_PAGE_SIZES, LIVE_DEFAULT_SIZE),
    ...(user ? { user_id: user } : {}),
    ...(panel ? { panel_id: panel } : {}),
    ...(LIVE_EXCLUSIONS.includes(excl) ? { exclusion: excl } : {}),
  }
}

/** The URL after a change to the live view's filters: queueSearch's rules
 *  under the `live_` names. */
export function liveSearch(prev: URLSearchParams, patch: Partial<LiveParams>): URLSearchParams {
  const out = new URLSearchParams(prev)
  const put = (name: string, value: string | undefined) => {
    if (value) out.set(name, value)
    else out.delete(name)
  }
  if ('user_id' in patch) put('live_user', patch.user_id ? String(patch.user_id) : undefined)
  if ('panel_id' in patch) put('live_panel', patch.panel_id ? String(patch.panel_id) : undefined)
  if ('exclusion' in patch) put('live_excl', patch.exclusion)
  if ('page_size' in patch) {
    put('live_size', patch.page_size === LIVE_DEFAULT_SIZE ? undefined : String(patch.page_size ?? ''))
  }
  if ('page' in patch) put('live_page', patch.page && patch.page > 1 ? String(patch.page) : undefined)
  else out.delete('live_page')
  return out
}

/**
 * The records tab's filters as the URL holds them. `since` / `until` are the
 * datetime-local text the admin typed, in BROWSER time (the field labels say
 * so); the tab turns them into instants when it asks, so the URL shows what
 * the fields show.
 */
export interface RecordsFilters {
  user_id?: number
  source?: string
  since?: string
  until?: string
  level?: string
  event?: string
  page: number
  page_size: number
}

export const RECORDS_PAGE_SIZES = [25, 50, 100]
const RECORDS_DEFAULT_SIZE = 25

// What a datetime-local field writes: a date and a time to the minute, with
// optional seconds and fraction. Anything else — "yesterday", a half-typed
// date — bounds nothing.
const LOCAL_TIME = /^\d{4}-(0[1-9]|1[0-2])-(0[1-9]|[12]\d|3[01])T([01]\d|2[0-3]):[0-5]\d(:[0-5]\d(\.\d{1,3})?)?$/

/** One of `allowed`, else undefined: a value this build does not offer (an
 *  event from a newer server, a mistyped link) filters nothing. */
function oneOf(raw: string | null, allowed: readonly string[]): string | undefined {
  return raw !== null && allowed.includes(raw) ? raw : undefined
}

/** The records tab's filters, read off its `rec_` params. Only what is set is
 *  present, so the request names exactly the filters in force. */
export function parseRecordsParams(params: URLSearchParams): RecordsFilters {
  const user = parseUserId(params.get('rec_user'))
  const source = oneOf(params.get('rec_source'), FLAG_SOURCES)
  const level = oneOf(params.get('rec_level'), FLAG_LEVELS)
  const event = oneOf(params.get('rec_event'), FLAG_EVENTS)
  const since = params.get('rec_since') ?? ''
  const until = params.get('rec_until') ?? ''
  return {
    ...(user ? { user_id: user } : {}),
    ...(source ? { source } : {}),
    ...(LOCAL_TIME.test(since) ? { since } : {}),
    ...(LOCAL_TIME.test(until) ? { until } : {}),
    ...(level ? { level } : {}),
    ...(event ? { event } : {}),
    page: pageOf(params.get('rec_page')),
    page_size: sizeOf(params.get('rec_size'), RECORDS_PAGE_SIZES, RECORDS_DEFAULT_SIZE),
  }
}

/** The URL after a change to the records' filters: queueSearch's rules under
 *  the `rec_` names. */
export function recordsSearch(prev: URLSearchParams, patch: Partial<RecordsFilters>): URLSearchParams {
  const out = new URLSearchParams(prev)
  const put = (name: string, value: string | undefined) => {
    if (value) out.set(name, value)
    else out.delete(name)
  }
  if ('user_id' in patch) put('rec_user', patch.user_id ? String(patch.user_id) : undefined)
  if ('source' in patch) put('rec_source', patch.source)
  if ('since' in patch) put('rec_since', patch.since)
  if ('until' in patch) put('rec_until', patch.until)
  if ('level' in patch) put('rec_level', patch.level)
  if ('event' in patch) put('rec_event', patch.event)
  if ('page_size' in patch) {
    put('rec_size', patch.page_size === RECORDS_DEFAULT_SIZE ? undefined : String(patch.page_size ?? ''))
  }
  if ('page' in patch) put('rec_page', patch.page && patch.page > 1 ? String(patch.page) : undefined)
  else out.delete('rec_page')
  return out
}

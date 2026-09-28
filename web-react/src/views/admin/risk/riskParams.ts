import {
  EXCLUSION_EXCLUDED, EXCLUSION_KEPT, EXCLUSION_REASONS, QUEUE_SOURCE_FILTERS,
  type LiveParams, type QueueParams, type QueueStatus,
} from '@/api/riskCenter'

// THE RISK CENTER'S URL. The page, its tabs' filters and the drawer's
// account all live in the query string, so a copied link, a reload, the bell
// and Back all mean the same view. Each tab's params carry their own prefix
// (none for the queue, the default tab; `live_` for the live view), so a tab
// switch keeps every tab's filters. Pure: parse and build, no router.

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

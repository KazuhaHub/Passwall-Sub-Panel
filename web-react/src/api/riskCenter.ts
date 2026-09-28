import { client } from './client'
import type { GeoAnomaly } from './geoAnomalies'
import type { ReadOptions } from './requestOptions'
import { RISK_KINDS, type RiskKind, type RiskSignal } from './riskSignals'
import type { Role, ServiceStatus, UserAccess } from './types'

// The risk center's own reads (handler/admin_risk_center.go, adminGroup
// only). Field names are the server's wire contract. None of these endpoints
// changes an account: the refresh reads every panel once and replaces the
// in-memory snapshot, nothing else.

/**
 * Why a live source was set aside rather than judged ('' = judged). Mirrors
 * domain.AddressExcluded*: a shared exit (held by several accounts at once),
 * the admin's ignore list, PSP's own node or relay, or an internal address.
 */
export type ConnExclusion = '' | 'internal' | 'listed' | 'infra' | 'shared'

/** The four reasons, in the order the filters list them. */
export const EXCLUSION_REASONS = ['internal', 'listed', 'infra', 'shared'] as const

/** The exclusion FILTER's two groupings besides one reason (ports.ConnExclusionKept
 *  / ConnExclusionExcluded): every judged source, or every set-aside one. */
export const EXCLUSION_KEPT = 'kept'
export const EXCLUSION_EXCLUDED = 'excluded'

/**
 * domain.LiveConnMaxPerUser, copied: the snapshot keeps at most this many
 * connections per account and counts the rest in `truncated`. The live DTO
 * reports the count but not the cap, and the cap is a code bound (not a
 * setting), so the copy only has to follow a code change there.
 */
export const LIVE_CONN_MAX_PER_USER = 64

export interface PanelRef {
  id: number
  /** '' for a panel deleted since the snapshot was taken. */
  name: string
}

/** Where the location database put an address: names and a region code,
 *  never a coordinate. Null on the wire when nothing placed it. */
export interface ConnRegion {
  country_code: string
  country: string
  region: string
  region_code: string
  city: string
}

/**
 * A device INFERRED behind a connection: a subscription fetch by the same
 * account from the same source within the device window. The connection
 * itself carries no device; the page must say "inferred" wherever it shows
 * one. `device_id4` is the declared device digest's first 4 characters.
 */
export interface ConnDevice {
  label: string
  device_id4: string
  client_type: string
  ua: string
  fetches: number
  last_at_ms: number
}

export interface LiveConnection {
  panel_id: number
  panel_name: string
  /** The 3X-UI node id, raw (PSP has no name for it); '' for readers
   *  without a node layer. */
  node: string
  /** The source the detector counts: an IPv4 address or an IPv6 /64. */
  source_key: string
  /** One address of the source on this (panel, node), for display. */
  ip: string
  exclusion: ConnExclusion | string
  /** Newest sighting on the PANEL's clock (unix seconds); 0 = none. */
  seen_at: number
  region: ConnRegion | null
  /** [] when nothing matched, or the source is not the account's own egress. */
  devices: ConnDevice[]
}

export interface LiveUser {
  user_id: number
  upn: string
  display_name: string
  /** Addresses the upstream still remembered that were not live. */
  stale_addresses: number
  /** Panels holding this account's clients that could not be read: its list
   *  is a floor, not a total, when this is above 0. */
  unread_panels: number
  connections: LiveConnection[]
}

export interface LiveSnapshotInfo {
  /** Null before the first poll since the panel started. */
  taken_at: string | null
  source: '' | 'poll' | 'refresh'
  age_seconds: number
  stale: boolean
  stale_after_seconds: number
  panels_asked: number
  /** Panels whose read failed: their connections are missing. */
  panels_unread: PanelRef[]
  /** Panels whose adapter has no live read (S-UI): never a failure. */
  panels_unsupported: PanelRef[]
  /** Nodes judged with no previous reference (the first reading after a
   *  restart): each is taken as still scanning, which only the reference can
   *  tell, so a node that stopped may list its last scan's addresses. The
   *  live window still applies; the upstream's whole 30 minutes is not shown. */
  unreferenced_nodes: number
  users: number
  connections: number
  truncated: number
}

export interface LiveView {
  snapshot: LiveSnapshotInfo
  refresh: { cooldown_seconds: number; available_in_seconds: number }
  device_window_hours: number
  devices_unavailable: boolean
  /** Every panel, for the filter and for naming. */
  panels: PanelRef[]
  /** One page of ACCOUNTS, most connections first. */
  items: LiveUser[]
  total: number
  page: number
  page_size: number
}

export interface LiveParams {
  page?: number
  page_size?: number
  user_id?: number
  panel_id?: number
  /** '' any; 'kept' the judged sources; 'excluded' the set-aside ones; or
   *  one reason. */
  exclusion?: string
  sort_by?: 'connections' | 'user_id'
  sort_dir?: 'asc' | 'desc'
}

/** A 200 from the refresh: describes the snapshot stored afterwards, which
 *  may be a newer poll's. `reason` is 'just_polled' when no panel was read
 *  because a poll had just answered. */
export interface RefreshResult {
  refreshed: boolean
  reason: '' | 'just_polled' | string
  taken_at: string | null
  source: '' | 'poll' | 'refresh'
  panels_asked: number
  panels_unread: PanelRef[]
  panels_unsupported: PanelRef[]
  connections: number
}

/** The body of a refused refresh (429; also in Retry-After). */
export interface RefreshThrottled {
  error: 'refresh_throttled'
  reason: 'cooldown' | 'in_progress' | string
  retry_after_seconds: number
}

/** One account's connection from one source through one panel node, merged
 *  across the detector samples that saw it. */
export interface ConnectionRecord {
  user_id: number
  upn: string
  display_name: string
  panel_id: number
  panel_name: string
  node: string
  source_key: string
  ip: string
  exclusion: ConnExclusion | string
  region: ConnRegion | null
  first_seen_ms: number
  last_seen_ms: number
  /** Detector samples that saw it. */
  count: number
}

export interface ConnectionHistoryParams {
  page?: number
  page_size?: number
  user_id?: number
  panel_id?: number
  exclusion?: string
  /** RFC 3339, on the last sighting. */
  since?: string
  until?: string
  search?: string
  sort_by?: 'last_seen' | 'first_seen' | 'count' | 'ip' | 'user_id'
  sort_dir?: 'asc' | 'desc'
}

/** Mirrors domain.FlagEvent. */
export type FlagEvent = 'enter_suspect' | 'enter_flagged' | 'leave_suspect' | 'leave_flagged'
  | 'auto_suspended' | 'auto_lifted_expiry' | 'auto_lifted_admin' | 'auto_replaced'

export const FLAG_EVENTS: readonly FlagEvent[] = [
  'enter_suspect', 'enter_flagged', 'leave_suspect', 'leave_flagged',
  'auto_suspended', 'auto_lifted_expiry', 'auto_lifted_admin', 'auto_replaced',
]

/** The level filter's values: the level a record moved TO, or 'cleared' for
 *  every record that moved to no attention (each leave and each lift). */
export const FLAG_LEVELS = ['flagged', 'suspect', 'suspended', 'cleared'] as const

/**
 * One attention change on one source. `source` is 'geo', 'geo_auto' or a risk
 * kind. `params` is address-free: GeoFlagParams for geo, the verdict's stored
 * evidence for a risk kind, the producer's numbers for geo_auto; null when
 * the record has none.
 */
export interface FlagRecord {
  id: number
  user_id: number
  upn: string
  display_name: string
  source: string
  event: FlagEvent | string
  /** '' = none (a leave or a lift). */
  level: '' | 'suspect' | 'flagged' | 'suspended' | string
  prev_level: '' | 'suspect' | 'flagged' | 'suspended' | string
  state: string
  code: string
  params: unknown | null
  at_ms: number
}

export interface FlagRecordParams {
  page?: number
  page_size?: number
  user_id?: number
  source?: string
  level?: string
  event?: string
  since?: string
  until?: string
}

export interface PagedResult<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

/** The params as sent: an empty filter is left out rather than sent as '',
 *  so a request says only what the admin actually chose. */
function sent<T extends object>(params: T): Partial<T> {
  return Object.fromEntries(
    Object.entries(params).filter(([, v]) => v !== undefined && v !== null && v !== ''),
  ) as Partial<T>
}

export async function getLiveConnections(params: LiveParams = {}, opts: ReadOptions = {}): Promise<LiveView> {
  const { data } = await client.get<LiveView>('/admin/risk-center/live', {
    params: sent(params), signal: opts.signal,
  })
  return data
}

/**
 * How long the SPA waits for one refresh: longer than the server lets a
 * refresh run (riskcenter.liveRefreshTimeout, 45 s), which the shared
 * client's 30 s is not. A panel that has not answered is only listed as
 * unread once that server bound passes, and the refresh is not cancelled by
 * the browser giving up — it still stores its reading and has spent the
 * fleet-wide cooldown. Aborted at 30 s, the page would report a timeout for a
 * refresh that did happen, and the admin's retry would meet the cooldown.
 * Local to this request, like nodes.ts's REALITY scan: every other call keeps
 * the shared 30 s. api/riskCenter.test.ts reads the server bound from the Go
 * source, so raising it past this fails there.
 */
export const LIVE_REFRESH_TIMEOUT_MS = 60_000

/**
 * Asks every panel for its live addresses now. The global error toast is
 * skipped: every outcome — refreshed, answered by a poll, refused with the
 * seconds to wait, or failed — is the caller's to report, and a generic
 * "too many requests" toast beside it would say the same thing twice, worse.
 */
export async function refreshLiveConnections(): Promise<RefreshResult> {
  const { data } = await client.post<RefreshResult>('/admin/risk-center/live/refresh', undefined, {
    _skipErrorToast: true,
    timeout: LIVE_REFRESH_TIMEOUT_MS,
  })
  return data
}

export async function listConnectionHistory(
  params: ConnectionHistoryParams = {}, opts: ReadOptions = {},
): Promise<PagedResult<ConnectionRecord>> {
  const { data } = await client.get<PagedResult<ConnectionRecord>>('/admin/risk-center/connections', {
    params: sent(params), signal: opts.signal,
  })
  return { ...data, items: data.items ?? [] }
}

export async function listFlagRecords(
  params: FlagRecordParams = {}, opts: ReadOptions = {},
): Promise<PagedResult<FlagRecord>> {
  const { data } = await client.get<PagedResult<FlagRecord>>('/admin/risk-center/flags', {
    params: sent(params), signal: opts.signal,
  })
  return { ...data, items: data.items ?? [] }
}

// ---------------------------------------------------------------------------
// The attention reads (handler/admin_risk_queue.go) and the review actions
// (handler/admin_risk_review.go). Every list arrives as [] and every optional
// object as null, never absent: the server pins that, so nothing below needs
// a null check beyond the ones the types name.

/**
 * What can put an account on the queue: the concurrent-location verdict, its
 * automatic suspension (geo_auto, level "suspended"), and each risk kind. The
 * flag records' `review` source is not one — an admin's decision is never
 * attention.
 */
export type AttentionSource = 'geo' | 'geo_auto' | RiskKind

export const ATTENTION_SOURCES: readonly AttentionSource[] = ['geo', 'geo_auto', ...RISK_KINDS]

/** The queue's source filter: geo_auto is shown by its card and level chip
 *  instead, so offering it here would say one fact twice. */
export const QUEUE_SOURCE_FILTERS: readonly AttentionSource[] = ['geo', ...RISK_KINDS]

/** The sources an admin's trust exempts (domain: the location detectors). */
export const LOCATION_SOURCES: readonly AttentionSource[] = ['geo', 'sub_spread', 'login_country']

export type QueueStatus = 'open' | 'dismissed' | 'trusted' | 'all'

export interface QueueParams {
  status?: QueueStatus
  /** A comma list of sources. */
  source?: string
  level?: 'flagged' | 'suspect'
  auto_suspended?: boolean
  urgent?: boolean
  q?: string
  page?: number
  page_size?: number
}

/** One attention source and its level. A source or level this build does not
 *  know is kept as a string rather than dropped. */
export interface AttentionEntry {
  source: AttentionSource | string
  level: 'flagged' | 'suspect' | 'suspended' | string
}

/** A queue row's review state: a stored dismissal, whether it no longer
 *  covers the account (reopened, naming the sources that escalated) or
 *  lapsed, and trust. */
export interface ReviewBadge {
  dismissed: boolean
  reopened: boolean
  lapsed: boolean
  trusted: boolean
  escalated: string[]
}

/** One account on the queue. `geo` is the /geo-anomalies item, null unless
 *  geo is among `sources`; `signals` hold only the kinds at attention. The
 *  hold's reason and time are omitted while the service is active. */
export interface QueueRow {
  user_id: number
  upn: string
  display_name: string
  group_id: number
  group_name: string
  level: '' | 'suspect' | 'flagged'
  auto_suspended: boolean
  urgent: boolean
  service_state: ServiceStatus
  service_disabled_reason?: string
  service_disabled_at_ms?: number
  sources: AttentionEntry[]
  geo: GeoAnomaly | null
  signals: RiskSignal[]
  /** 0 when nothing changed on record (a trusted account with no attention). */
  changed_at_ms: number
  review: ReviewBadge
}

export interface QueueCounts {
  /** Null before the first snapshot. */
  online: number | null
  online_taken_at: string | null
  online_stale: boolean
  urgent: number
  flagged: number
  suspect: number
  auto_suspended: number
  dismissed: number
  trusted: number
  geo_unknown: number
}

export interface QueueView extends PagedResult<QueueRow> {
  counts: QueueCounts
  global_detectors_off: boolean
}

/**
 * The drawer's review: the stored dismissal with the levels it accepted
 * (display only) and the admin's note (admin-only, never in a flag record),
 * trust, and what the reopen rule decides now. An admin is named by the
 * CURRENT UPN, '' once that admin is gone — the page shows `#id` then.
 */
export interface RiskReview {
  dismissed: boolean
  dismissed_at_ms: number
  dismissed_by: number
  dismissed_by_upn: string
  note: string
  levels: Record<string, string>
  reopened: boolean
  lapsed: boolean
  escalated: string[]
  trusted: boolean
  trusted_at_ms: number
  trusted_by: number
  trusted_by_upn: string
}

/** The drawer's account. The hold (reason, detail, time) is sent only while
 *  the service axis carries one; `access` is the Users page's decision. */
export interface RiskUserBasics {
  id: number
  upn: string
  display_name: string
  role: Role
  group_id: number
  group_name: string
  enabled: boolean
  traffic_limit_bytes: number
  service_disabled_reason?: string
  service_disable_detail?: string
  service_disabled_at_ms?: number
  access?: UserAccess
}

/** One client behind the account's subscription fetches in the device window.
 *  `sources` are addresses: admin-only, like every risk-center read. */
export interface UserDevice {
  label: string
  device_id4: string
  client_type: string
  ua: string
  fetches: number
  first_at_ms: number
  last_at_ms: number
  sources: string[]
  sources_more: number
}

/**
 * Everything the drawer shows about one account in one read. `stale` marks a
 * verdict nobody re-judged within the freshness window: it is history and
 * counts toward nothing. `live` is exactly what GET
 * /risk-center/live?user_id=<id>&page=1&page_size=1 serves.
 */
export interface RiskUserSummary {
  user: RiskUserBasics
  attention: AttentionEntry[]
  review: RiskReview
  geo: (GeoAnomaly & { stale: boolean }) | null
  signals: (RiskSignal & { stale: boolean })[]
  live: LiveView
  devices: UserDevice[]
  device_window_hours: number
  devices_unavailable: boolean
}

/** The Users page's risk column, keyed by the decimal account id: every
 *  account at attention and every trusted one (level '' when it has none). */
export type RiskLevels = Record<string, {
  level: '' | 'suspect' | 'flagged'
  auto_suspended: boolean
  open: boolean
  dismissed: boolean
  trusted: boolean
}>

/** The 409 codes of the review routes: a stale tab or a second admin must
 *  never write a duplicate record or act on a state it did not see. */
export type ReviewConflictCode = 'nothing_to_dismiss' | 'already_dismissed' | 'not_dismissed' | 'already_trusted'
  | 'not_trusted' | 'changed'

export interface ReviewResult {
  review: RiskReview
}

/** A trust, and what became of the resume it was asked to make: lifted
 *  (`resumed`), lifted with the push neither made nor queued
 *  (`resume_warning`), or not lifted (`resume_error`). A failed lift is not a
 *  failed trust — the trust stands either way. */
export interface TrustResult extends ReviewResult {
  resumed: boolean
  resume_warning?: string
  resume_error?: string
}

/** The queue's params as sent: a false switch is left out like an empty
 *  filter, so a request says only what the admin chose. */
export async function getRiskQueue(params: QueueParams = {}, opts: ReadOptions = {}): Promise<QueueView> {
  const { auto_suspended, urgent, ...rest } = params
  const { data } = await client.get<QueueView>('/admin/risk-center/queue', {
    params: sent({ ...rest, auto_suspended: auto_suspended || undefined, urgent: urgent || undefined }),
    signal: opts.signal,
  })
  return { ...data, items: data.items ?? [] }
}

/** One account's drawer. Silent: the drawer answers a 404 (and every other
 *  failure) itself, and a toast beside it would say it twice. */
export async function getRiskUser(userId: number, opts: ReadOptions = {}): Promise<RiskUserSummary> {
  const { data } = await client.get<RiskUserSummary>(`/admin/risk-center/users/${userId}`, {
    signal: opts.signal, _skipErrorToast: true,
  })
  return data
}

/** The Users page's column. Silent: a failed read leaves the column blank,
 *  never a toast over the list it only decorates. */
export async function getRiskLevels(opts: ReadOptions = {}): Promise<RiskLevels> {
  const { data } = await client.get<RiskLevels>('/admin/risk-center/levels', {
    signal: opts.signal, _skipErrorToast: true,
  })
  return data ?? {}
}

// The four review actions. Each skips the global error toast: every outcome —
// done, each 409 code, a note too long — is reported by the caller in its own
// words, and a generic toast beside it would say the same thing worse.

/** `expected` is every level the admin saw (source → level); a level worse
 *  than that now is a 409 `changed` and nothing is written. */
export async function dismissRiskUser(
  userId: number, body: { note?: string; expected?: Record<string, string> } = {},
): Promise<ReviewResult> {
  const { data } = await client.post<ReviewResult>(`/admin/risk-center/users/${userId}/dismiss`, body, {
    _skipErrorToast: true,
  })
  return data
}

export async function undismissRiskUser(userId: number): Promise<ReviewResult> {
  const { data } = await client.delete<ReviewResult>(`/admin/risk-center/users/${userId}/dismiss`, {
    _skipErrorToast: true,
  })
  return data
}

export async function trustRiskUser(userId: number, resumeService: boolean): Promise<TrustResult> {
  const { data } = await client.post<TrustResult>(`/admin/risk-center/users/${userId}/trust`,
    { resume_service: resumeService }, { _skipErrorToast: true })
  return data
}

export async function untrustRiskUser(userId: number): Promise<ReviewResult> {
  const { data } = await client.delete<ReviewResult>(`/admin/risk-center/users/${userId}/trust`, {
    _skipErrorToast: true,
  })
  return data
}

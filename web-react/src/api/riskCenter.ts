import { client } from './client'
import type { ReadOptions } from './requestOptions'

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
  /** Nodes judged with no previous reference, whose whole upstream window
   *  was trusted once (the first poll after a restart). */
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
 * Asks every panel for its live addresses now. The global error toast is
 * skipped: every outcome — refreshed, answered by a poll, refused with the
 * seconds to wait, or failed — is the caller's to report, and a generic
 * "too many requests" toast beside it would say the same thing twice, worse.
 */
export async function refreshLiveConnections(): Promise<RefreshResult> {
  const { data } = await client.post<RefreshResult>('/admin/risk-center/live/refresh', undefined, {
    _skipErrorToast: true,
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

import { client } from './client'
import type { GeoAnomaly, GeoExcluded, GeoTier } from './geoAnomalies'

/**
 * The four observe-only risk signals, mirroring domain.RiskKind. Each is
 * judged on its own and stored as its own (account, kind) row: there is no
 * score, and nothing on the server acts on any of them.
 */
export type RiskKind = 'sub_spread' | 'devices' | 'usage_shift' | 'login_country'

/** Display order — the table's column order, and domain.RiskKinds(). The one
 *  list to extend for a new kind; a kind outside it is ignored everywhere. */
export const RISK_KINDS: readonly RiskKind[] = ['sub_spread', 'devices', 'usage_shift', 'login_country']

/**
 * Every code each kind's evaluator can write: a copy of
 * domain.AllRiskCodes(), in its order. Held to the server through the locale
 * bundles — the Go side checks the bundles against AllRiskCodes in both
 * directions, and utils/riskSignals.test.ts checks this copy against the
 * bundles — so a drift fails a build on one side or the other.
 */
export const RISK_CODES: Readonly<Record<RiskKind, readonly string[]>> = {
  sub_spread: [
    'signal_off', 'scope_off', 'scope_country', 'allow_anywhere', 'trusted',
    'no_fetches', 'retention_short', 'all_excluded', 'geo_unavailable',
    'low_placed', 'no_regions',
    'spread', 'spread_building', 'within',
  ],
  devices: [
    'signal_off', 'capture_off', 'no_fetches', 'retention_short',
    'no_hwid', 'over', 'over_building', 'within',
  ],
  usage_shift: [
    'signal_off', 'retention_short', 'no_usage', 'warmup',
    'sustained', 'building', 'within',
  ],
  login_country: [
    'signal_off', 'scope_off', 'allow_anywhere', 'trusted', 'no_recent_logins',
    'geo_unavailable', 'unplaced', 'learning',
    'new_country', 'known_countries',
  ],
}

/** v2's seven states, read the same way: only `flagged` reaches the bell, and
 *  `unknown` / `idle` are "cannot tell", never "clean". */
export type RiskState = GeoAnomaly['state']

// The evidence bodies. Field names are the server's wire contract
// (domain.*Evidence); every list is present, empty rather than null. A day
// mask is bit i = window day i, oldest first, counted in panel-local
// calendar days from window_start. None of them carries an address.

/** sub_spread: the judged country's provinces, grouped by the clients that
 *  link them, with the day masks the verdict counted. */
export interface SubSpreadEvidence {
  v: number
  window_days: number
  /** YYYY-MM-DD, panel-local: the date of day mask bit 0. */
  window_start: string
  /** The sub-log retention setting; absent when the logs are never pruned. */
  retention_days?: number
  min_days: number
  min_placed_pct: number
  /** The group's concurrent-location region tolerance (geo_anomaly.max_regions). */
  tolerance: number
  /** '' when nothing reached judging. */
  country: string
  /** Groups holding a recurring province; groups ≤ groups_all always. */
  groups: number
  groups_all: number
  /** `rc` is the region's ISO 3166-2 code, display only, absent when the
   *  database gave none; the province is still keyed by (cc, region). */
  provinces: { cc: string; region: string; rc?: string; days: number; established: boolean; group: number }[]
  /** `provinces` are indexes into the provinces above. */
  identities: { kind: 'hwid' | 'ua'; label: string; hwid4?: string; days: number; provinces: number[] }[]
  /** Every other placed country: context only, never judged. */
  foreign: { cc: string; days: number }[]
  excluded: GeoExcluded
  coverage: { sources: number; placed: number; region_known: number }
}

/** devices: the devices clients declared through x-hwid. Only a 4-character
 *  prefix of the per-account digest ever reaches the page. */
export interface DevicesEvidence {
  v: number
  window_days: number
  window_start: string
  retention_days?: number
  min_days: number
  max_devices: number
  recurrent: number
  distinct: number
  devices: { label: string; hwid4: string; days: number; last_ms: number; client: string; recurrent: boolean }[]
  fetches_with_hwid: number
  fetches_without: number
  clients: { label: string; days: number }[]
}

/** usage_shift: the panel-local days of bytes, oldest first (35 shipped: 28
 *  baseline days, then 7 judged ones), and every number the judged days were
 *  held to. */
export interface UsageShiftEvidence {
  v: number
  end_date: string
  history_retention_days?: number
  /** Daily totals in bytes: the baseline days, then the judged ones. */
  series: number[]
  history_days: number
  median: number
  ratio: number
  /** Bytes. */
  floor: number
  /** One per judged day, oldest first; empty before judging. */
  thresholds: number[]
  over: boolean[]
  over_days: number
  fleet_factors: number[]
  /** The days the verdict was judged with, "unset" already resolved: the
   *  fleet's baseline and judged days, and the group's warm-up, flag and
   *  suspect days. Absent on a row stored before they were settings, which
   *  was judged with the shipped 28 / 7 / 14 / 4 / 2. */
  baseline_days?: number
  recent_days?: number
  warmup_days?: number
  flag_days?: number
  suspect_days?: number
}

/** login_country: panel logins by country, and the ones from a new one. */
export interface LoginCountryEvidence {
  v: number
  lookback_days: number
  hold_days: number
  warmup: number
  logins: number
  recent: number
  judged: number
  skipped: { infra: number; internal: number; listed: number; node_country: number; unplaced: number }
  known: string[]
  /** Newest first. */
  events: { cc: string; at_ms: number; method: string }[]
}

/** One stored (account, kind) row. `evidence` is null for a verdict with
 *  nothing to show (idle, disabled, exempt); otherwise the kind's body above.
 *  `kind` may be one this build does not know — readers skip those. */
export interface RiskSignal {
  kind: RiskKind
  state: RiskState
  code: string
  evidence: unknown | null
  updated_at_ms: number
}

/** One account: its signals (RISK_KINDS order, only the kinds with a row) and
 *  its concurrent-location verdict beside them. `geo` is null when the
 *  detector has nothing on the account. `flagged` there is the LATCH, apart
 *  from `state`, as on the Geo tab. */
export interface RiskUserRow {
  user_id: number
  upn?: string
  display_name?: string
  geo: { state: RiskState; flagged: boolean; tier: GeoTier; updated_at_ms: number } | null
  signals: RiskSignal[]
}

/**
 * Every account with at least one signal row, by user id. Unfiltered by
 * design: the attention filter is the tab's, on a switch the admin can turn
 * off, so a signal that quietly stopped judging stays visible.
 *
 * `user_id` asks for that one account's row alone (the risk center's lookup),
 * instead of the fleet list filtered here.
 */
export async function listRiskSignals(
  signal?: AbortSignal, params: { user_id?: number } = {},
): Promise<RiskUserRow[]> {
  const { data } = await client.get<{ items: RiskUserRow[] }>('/admin/risk-signals', { params, signal })
  return data.items ?? []
}

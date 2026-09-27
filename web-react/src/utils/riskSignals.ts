// Pure helpers for the risk-signals tab and its settings block. No I/O, no
// React: each one either mirrors a server rule (riskPolicy) or decides what an
// admin reads first (needsAttention, sortRiskRows, riskCodeText), so all of
// them are pinned by unit tests rather than by rendering.
import {
  RISK_KINDS, type DevicesEvidence, type LoginCountryEvidence, type RiskKind, type RiskSignal, type RiskState,
  type RiskUserRow, type SubSpreadEvidence, type UsageShiftEvidence,
} from '@/api/riskSignals'
import type { UISettings } from '@/api/settings'
import type { Translate } from './geoAnomaly'
import type { RegionNamer, RegionRef } from './regionName'

/** Whether this build draws a column for the kind. A newer server's kind has
 *  no column here, so it neither lists a row nor ages one. */
function knownKind(kind: string): kind is RiskKind {
  return (RISK_KINDS as readonly string[]).includes(kind)
}

/**
 * Whether the row belongs in the default "needs attention" list: any shown
 * signal flagged or suspect, or a concurrent-location verdict that is.
 *
 * The geo LATCH counts, not only its state: a flagged account that went idle
 * reads state idle and is still flagged, and disconnecting for a while would
 * otherwise take it off the list. Unknown does not count — it is "cannot
 * tell", and on a fleet whose clients send no x-hwid every row reads
 * devices: unknown; listing them would bury the accounts worth opening. The
 * switch beside the table shows every row.
 */
export function needsAttention(row: RiskUserRow): boolean {
  const loud = (s: RiskState) => s === 'flagged' || s === 'suspect'
  if (row.signals.some(s => knownKind(s.kind) && loud(s.state))) return true
  return !!row.geo && (row.geo.flagged || loud(row.geo.state))
}

function stateRank(s: RiskState): number {
  switch (s) {
    case 'flagged': return 0
    case 'suspect': return 1
    case 'unknown': return 2
    case 'clean': return 3
    default: return 4 // idle / exempt / disabled
  }
}

/** The row's most severe shown signal, its geo verdict included. A geo latch
 *  ranks as a flag for the reason needsAttention counts it. */
function rowRank(row: RiskUserRow): number {
  let rank = 4
  for (const s of row.signals) {
    if (knownKind(s.kind)) rank = Math.min(rank, stateRank(s.state))
  }
  if (row.geo) rank = Math.min(rank, row.geo.flagged ? 0 : stateRank(row.geo.state))
  return rank
}

function rowName(row: RiskUserRow): string {
  return row.upn || `#${row.user_id}`
}

/** A sorted COPY (the input is the query cache's): most severe first, then by
 *  name, so equal rows keep one order across refreshes. */
export function sortRiskRows(rows: RiskUserRow[]): RiskUserRow[] {
  return [...rows].sort((a, b) => rowRank(a) - rowRank(b) || rowName(a).localeCompare(rowName(b)))
}

/** The evidence as a record, or an empty one for a verdict that carries none
 *  (idle, disabled, exempt): those codes' sentences have no numbers. */
function evidenceOf<T>(sig: RiskSignal): Partial<T> {
  return (sig.evidence && typeof sig.evidence === 'object' ? sig.evidence : {}) as Partial<T>
}

/** The numbers each code's sentence names, read from the evidence the verdict
 *  was judged on (spec §13), never recomputed from the settings: the policy is
 *  resolved per group, as the concurrent-location reasons are. */
function codeParams(sig: RiskSignal): Record<string, unknown> {
  switch (sig.kind) {
    case 'sub_spread': {
      const ev = evidenceOf<SubSpreadEvidence>(sig)
      const x = ev.excluded ?? { shared: 0, listed: 0, infra: 0, internal: 0 }
      return {
        // retention_days is left out when the logs are never pruned, and then
        // the window is the whole seven days.
        retention: ev.retention_days ?? ev.window_days, needed: ev.min_days,
        total: x.shared + x.listed + x.infra + x.internal,
        shared: x.shared, listed: x.listed, infra: x.infra, internal: x.internal,
        sources: ev.coverage?.sources, placed: ev.coverage?.placed, ratio: ev.min_placed_pct,
        country: ev.country, min_days: ev.min_days, tolerance: ev.tolerance,
        // A flag counts the groups holding a RECURRING province; the ramp and
        // the clean verdict were judged on every group.
        groups: sig.code === 'spread' ? ev.groups : ev.groups_all,
      }
    }
    case 'devices': {
      const ev = evidenceOf<DevicesEvidence>(sig)
      return {
        retention: ev.retention_days ?? ev.window_days, needed: ev.min_days,
        recurrent: ev.recurrent, distinct: ev.distinct, max: ev.max_devices,
      }
    }
    case 'usage_shift': {
      const ev = evidenceOf<UsageShiftEvidence>(sig)
      // The series is 35 days, and hourly history must cover one more,
      // whatever the fetch window is: the prune cuts at now minus the
      // retention, so at 35 the series' first day is already partly gone
      // (domain.EvaluateUsageShift's retention_short).
      return {
        retention: ev.history_retention_days, needed: 36,
        over: ev.over_days, ratio: ev.ratio, history: ev.history_days,
      }
    }
    case 'login_country': {
      const ev = evidenceOf<LoginCountryEvidence>(sig)
      return {
        warmup: ev.warmup,
        countries: [...new Set((ev.events ?? []).map(e => e.cc))].join(', '),
      }
    }
    default:
      return {}
  }
}

/**
 * The localized sentence for one signal's code, with the numbers from its
 * evidence. The default is the code itself: a code a newer server wrote
 * before this build learned it still says something, where a blank tooltip
 * would say nothing.
 */
export function riskCodeText(sig: RiskSignal, t: Translate): string {
  return t(`admin:risk_signals.code.${sig.kind}.${sig.code}`, { ...codeParams(sig), defaultValue: sig.code })
}

/** A day mask as one flag per window day, oldest first (bit i = day i). */
export function dayBits(mask: number, days: number): boolean[] {
  return Array.from({ length: Math.max(days, 0) }, (_, i) => (mask & (1 << i)) !== 0)
}

/**
 * The date of each window day, from the panel-local window start the server
 * wrote. Whole days are added to the date read as UTC, where every day is 24
 * hours — the dates are calendar labels, not instants, so no zone applies. A
 * start that is not YYYY-MM-DD labels every day ''.
 */
export function dayLabels(start: string, days: number): string[] {
  const n = Math.max(days, 0)
  const base = /^\d{4}-\d{2}-\d{2}$/.test(start) ? Date.parse(`${start}T00:00:00Z`) : NaN
  if (Number.isNaN(base)) return Array.from({ length: n }, () => '')
  return Array.from({ length: n }, (_, i) => new Date(base + i * 86_400_000).toISOString().slice(0, 10))
}

/** Bytes as GiB — the unit the daily floor is typed in. */
export function formatGB(bytes: number): string {
  return `${(bytes / 2 ** 30).toFixed(2)} GB`
}

// domain.RiskPolicyFromSettings' defaults and bounds, copied rather than
// fetched: the server returns what is STORED (0 = unset), not what is in
// effect.
const RISK_WINDOW_DAYS = 7
const RISK_DEFAULT = { minDays: 3, maxDevices: 3, ratio: 3, floorGB: 3 }
const RISK_RATIO_MIN = 1.5

function positive(v: number | undefined): v is number {
  return typeof v === 'number' && v > 0
}

/**
 * The risk policy actually in effect for these settings, mirroring
 * domain.RiskPolicyFromSettings: an unset (≤ 0 or missing) value is the
 * shipped default, and every repair errs toward not accusing — min_days is
 * clamped to the seven-day window, the ratio is raised to 1.5.
 */
export function riskPolicy(s: UISettings): { minDays: number; maxDevices: number; ratio: number; floorGB: number } {
  return {
    minDays: positive(s.risk_min_days) ? Math.min(s.risk_min_days, RISK_WINDOW_DAYS) : RISK_DEFAULT.minDays,
    maxDevices: positive(s.risk_max_devices) ? s.risk_max_devices : RISK_DEFAULT.maxDevices,
    ratio: positive(s.risk_usage_ratio) ? Math.max(s.risk_usage_ratio, RISK_RATIO_MIN) : RISK_DEFAULT.ratio,
    floorGB: positive(s.risk_usage_floor_gb) ? s.risk_usage_floor_gb : RISK_DEFAULT.floorGB,
  }
}

/**
 * The OLDEST computed time among the row's shown signals, 0 for none. Not the
 * newest: a kind skipped for days (infrastructure never loaded, a scan
 * failing) must show its age rather than hide behind a fresh sibling. Each
 * kind's own time is in its chip's tooltip.
 */
export function oldestUpdate(row: RiskUserRow): number {
  let oldest = 0
  for (const s of row.signals) {
    if (knownKind(s.kind) && (oldest === 0 || s.updated_at_ms < oldest)) oldest = s.updated_at_ms
  }
  return oldest
}

/**
 * How a province is named on the page: what `name` makes of it — the tab
 * passes regionNamer, which gives a Chinese UI the Chinese name of a known CN
 * code and everyone else the database's own region — or the country when
 * there is no region to name. The one place the name is chosen.
 */
export function placeLabel(p: RegionRef, name: RegionNamer = x => x.region): string {
  return name(p) || p.cc
}

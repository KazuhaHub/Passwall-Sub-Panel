// Pure helpers for the risk signals' evidence. No I/O, no React: each one
// decides what an admin reads (riskCodeText, the day strips), so all of them
// are pinned by unit tests rather than by rendering.
import type {
  DevicesEvidence, LoginCountryEvidence, RiskSignal, SubSpreadEvidence, UsageShiftEvidence,
} from '@/api/riskSignals'
import type { Translate } from './geoAnomaly'
import type { RegionNamer, RegionRef } from './regionName'

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
      // The days this verdict was judged with, as the evidence records them.
      // A row stored before they were settings has none of them and was
      // judged with the shipped 28 + 7 days, a 14-day warm-up and flag at 4;
      // its series is then the fixed 35 days.
      const days = ev.baseline_days && ev.recent_days
        ? ev.baseline_days + ev.recent_days
        : (ev.series?.length || 35)
      // Hourly history must cover the whole series and one day more,
      // whatever the fetch window is: the prune cuts at now minus the
      // retention, so at exactly the series length its first day is already
      // partly gone (domain.EvaluateUsageShift's retention_short).
      return {
        retention: ev.history_retention_days, needed: days + 1,
        over: ev.over_days, ratio: ev.ratio, history: ev.history_days,
        recent: ev.recent_days ?? 7, warmup: ev.warmup_days ?? 14, flag: ev.flag_days ?? 4,
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

/**
 * How a province is named on the page: what `name` makes of it — the tab
 * passes regionNamer, which gives a Chinese UI the Chinese name of a known CN
 * code and everyone else the database's own region — or the country when
 * there is no region to name. The one place the name is chosen.
 */
export function placeLabel(p: RegionRef, name: RegionNamer = x => x.region): string {
  return name(p) || p.cc
}

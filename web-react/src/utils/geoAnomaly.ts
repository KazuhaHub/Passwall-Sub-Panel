// Pure helpers for the concurrent-location views: the risk center's queue and
// drawer, the geo settings section and the per-group editor. No I/O, no React
// — each one either mirrors a server rule (geoTolerances) or decides what an
// admin reads (reasonText, the place tree), so both are pinned by unit tests
// rather than by rendering.
import type { GeoAnomaly, GeoEvidence, GeoSpot, GeoTier, GeoWhy } from '@/api/geoAnomalies'
import type { GeoIPStatus, UISettings } from '@/api/settings'

/**
 * The i18n key for a tier chip, or null when the row has no tier (clean,
 * exempt, never over). A tier this build does not know is null too: a chip
 * showing a raw key would read as a verdict nobody can explain.
 */
export function tierLabelKey(tier: GeoTier): string | null {
  switch (tier) {
    case 'country':
    case 'region':
    case 'city':
      return `admin:geo_anomalies.tier_${tier}`
    default:
      return null
  }
}

export type Translate = (key: string, opts?: Record<string, unknown>) => string

const REASON = 'admin:geo_anomalies.'

/**
 * The numbers an over sentence names at the verdict's tier: how many, where,
 * against which tolerance. The same choice the server's describeOver makes,
 * so the localized sentence and the stored English cannot disagree on them.
 * null for a tier this build does not know, which the caller renders as the
 * stored English rather than a sentence with a hole in it.
 */
function overAt(why: GeoWhy, ev: GeoEvidence): { spread: number; tolerance: number; country: string } | null {
  switch (why.tier) {
    case 'country': return { spread: ev.spread.countries, tolerance: why.tol.countries, country: '' }
    case 'region': return { spread: ev.spread.regions, tolerance: why.tol.regions, country: ev.spread.region_country }
    case 'city': return { spread: ev.spread.cities, tolerance: why.tol.cities, country: ev.spread.city_country }
    default: return null
  }
}

/**
 * The localized reason for a geo row, built from evidence.why (the policy the
 * server actually judged with, group overrides included), the evidence counts
 * and the row's streak fields. Every key carries defaultValue: row.reason, so a
 * v<2 row, an unknown code or a missing key renders the stored English.
 *
 * The numbers come from the row, never from the settings: the policy is
 * resolved per group, and geoTolerances(global) would print the default
 * tolerance for an account whose group allows more. Unlike the English, the
 * sentence does not repeat the place list — the Places column beside it
 * already shows every spot, by country, region and city.
 */
export function reasonText(row: GeoAnomaly, t: Translate): string {
  const ev = row.evidence
  const why = ev && ev.v >= 2 ? ev.why : undefined
  if (!why) return row.reason
  const d = { defaultValue: row.reason }
  // Two strings make one sentence, and they fall back together. With the
  // head missing, t has already returned the whole stored English, streak
  // included; a localized tail glued onto it would print the streak twice,
  // once in each language.
  const glue = (head: string, tail: () => string) => (head === row.reason ? head : head + tail())

  switch (why.code) {
    case 'disabled':
      return t(`${REASON}reason_disabled`, d)
    case 'exempt':
      return t(`${REASON}reason_exempt`, d)
    case 'trusted':
      // An admin's trust in this one account (risk center), not the group's
      // allow_anywhere: the sentence says who exempted it.
      return t(`${REASON}reason_trusted`, d)
    case 'idle_stale':
      return t(`${REASON}reason_idle_stale`, { ...d, stale: ev.stale })
    case 'idle_none':
      return t(`${REASON}reason_idle_none`, d)
    case 'unknown_excluded': {
      const x = ev.excluded
      return t(`${REASON}reason_unknown_excluded`, {
        ...d, total: x.shared + x.listed + x.infra + x.internal,
        shared: x.shared, listed: x.listed, infra: x.infra, internal: x.internal,
      })
    }
    case 'unknown_geo_off':
      return t(`${REASON}reason_unknown_geo_off`, d)
    case 'unknown_low_ratio':
      return t(`${REASON}reason_unknown_low_ratio`, {
        ...d, placed: ev.coverage.placed, sample: ev.coverage.placed + ev.coverage.unplaced,
        ratio: Math.round(why.min_placed_ratio * 100),
      })
    case 'suspect':
    case 'flagged_sustained': {
      const at = overAt(why, ev)
      if (!at) return row.reason
      const suffix = why.code === 'suspect' ? 'reason_suspect_suffix' : 'reason_flagged_suffix'
      return glue(t(`${REASON}reason_over_${why.tier}`, { ...d, ...at }),
        () => t(`${REASON}${suffix}`, { over: row.over_streak, need: why.flag_after, defaultValue: '' }))
    }
    case 'flagged_clearing': {
      const head = t(`${REASON}reason_flagged_clearing`, { ...d, under: row.under_streak, need: why.clear_after })
      // The tier that RAISED the flag, named with the Geo tab's own chip
      // label. A latch stored without one gets no clause, as on the server.
      const tierKey = tierLabelKey(why.tier ?? '')
      if (!tierKey) return head
      return glue(head, () => t(`${REASON}reason_flagged_clearing_tier`, {
        tier: t(tierKey, { defaultValue: why.tier }), defaultValue: '',
      }))
    }
    case 'clean_unplaced':
      return t(`${REASON}reason_clean_unplaced`, d)
    case 'clean_within':
      return t(`${REASON}reason_clean_within`, {
        ...d, countries: ev.spread.countries, regions: ev.spread.regions, cities: ev.spread.cities,
        tol_countries: why.tol.countries, tol_regions: why.tol.regions, tol_cities: why.tol.cities,
        scope: t(`${REASON}reason_scope_${why.scope}`, { defaultValue: why.scope }),
      })
    default:
      // A code a newer server wrote before this build learned it.
      return row.reason
  }
}

export interface SpotTree {
  cc: string
  n: number
  /** `rc` is the region's ISO 3166-2 code, present only when a spot gave one:
   *  display only, the region is still keyed by its name. */
  regions: { region: string; rc?: string; n: number; cities: { city: string; n: number }[] }[]
}

/**
 * Orders siblings by count, most first, then by name so equal counts render in
 * a stable order across polls. An EMPTY name always goes last: it is the bucket
 * of sources the database resolved no further than the parent (a country-only
 * row, a region without a city), not a place competing with the named ones —
 * sorting it by count would put "somewhere in CN" above the provinces that
 * actually explain a region-tier flag.
 */
function bySpotOrder<T extends { n: number }>(name: (x: T) => string) {
  return (a: T, b: T) => {
    const ea = name(a) === '' ? 1 : 0
    const eb = name(b) === '' ? 1 : 0
    if (ea !== eb) return ea - eb
    if (a.n !== b.n) return b.n - a.n
    return name(a).localeCompare(name(b))
  }
}

/**
 * Folds the flat evidence spots into country → region → city, summing `n` up
 * the tree so each level reads as "how many of the judged sources were here".
 * The evidence caps spots server-side, so the sums describe what was recorded,
 * which is what the verdict's reason names too.
 *
 * A region's code is the smallest non-empty `rc` among its spots, the rule
 * the server applies (domain.PreferRegionCode). The server already gives one
 * region one code per poll; picking the same way here keeps the tree
 * deterministic whatever order the spots arrive in. It never splits a region —
 * the key stays the name — and a region with no code gets no `rc` property at
 * all, so its node is shaped as it always was.
 */
export function groupSpots(spots: GeoSpot[]): SpotTree[] {
  const countries = new Map<string, Map<string, { rc: string; cities: Map<string, number> }>>()
  for (const s of spots) {
    let regions = countries.get(s.cc)
    if (!regions) countries.set(s.cc, (regions = new Map()))
    let r = regions.get(s.region)
    if (!r) regions.set(s.region, (r = { rc: '', cities: new Map() }))
    const rc = s.rc ?? ''
    if (rc !== '' && (r.rc === '' || rc < r.rc)) r.rc = rc
    r.cities.set(s.city, (r.cities.get(s.city) ?? 0) + s.n)
  }
  const out: SpotTree[] = []
  for (const [cc, regions] of countries) {
    const rs: SpotTree['regions'] = []
    for (const [region, { rc, cities }] of regions) {
      const cs = [...cities].map(([city, n]) => ({ city, n })).sort(bySpotOrder(c => c.city))
      rs.push({ region, ...(rc ? { rc } : {}), n: cs.reduce((sum, c) => sum + c.n, 0), cities: cs })
    }
    rs.sort(bySpotOrder(r => r.region))
    out.push({ cc, n: rs.reduce((sum, r) => sum + r.n, 0), regions: rs })
  }
  return out.sort(bySpotOrder(c => c.cc))
}

/**
 * The farthest pair of the row's concurrent sources, in km after both
 * accuracy radii (rounded to 10 by the server), or 0 for nothing to show.
 *
 * Only v3 evidence can carry it. Before that an absent max_km means "not
 * recorded", and a number on such a row is not one this build vouches for.
 * On v3, absent or 0 means no distance was measured (fewer than two located
 * sources below the country) or every pair lay within its radii — never "the
 * same place" — so it is not shown either. Display only: the verdict never
 * reads it.
 */
export function spreadKm(ev: GeoEvidence | undefined): number {
  const km = ev && ev.v >= 3 ? ev.spread?.max_km : undefined
  return typeof km === 'number' && Number.isFinite(km) && km > 0 ? Math.round(km) : 0
}

/**
 * Whether the ACTIVE location database resolves countries only, so the region
 * and city tiers cannot fire. Only the active file counts — a country file
 * lying next to an active city one changes nothing. An unknown status (failed
 * read, older server) is false: no evidence of a coarse database is not
 * evidence of one, and a wrong banner would say two tiers are dead.
 */
export function activeDbIsCountryOnly(s: GeoIPStatus | undefined): boolean {
  return (s?.available ?? []).some(d => d.active && d.granularity === 'country')
}

export interface GeoTierCounts {
  countries: number
  regions: number
  cities: number
}

// The shipped defaults and the ceiling, as domain.DefaultGeoPolicy and
// domain.GeoBanMaxDurationMinutes define them. Copied, not fetched: the
// server returns what is STORED (0 = unset), not what is in effect.
const FLAG_DEFAULT: GeoTierCounts = { countries: 1, regions: 1, cities: 2 }
const BAN_DEFAULT: GeoTierCounts = { countries: 1, regions: 2, cities: 3 }
const BAN_AFTER_DEFAULT = 6
const BAN_MINUTES_DEFAULT = 60
const BAN_MINUTES_MAX = 10080

/** A stored value that is not a positive number means "never configured". */
function orDefault(v: number | undefined, def: number): number {
  return typeof v === 'number' && v > 0 ? v : def
}

/**
 * The tolerances actually in effect for these settings, mirroring the server's
 * GeoPolicyFromSettings + sanitized(): an unset (<= 0 or missing) value is the
 * shipped default, never zero tolerance; each ban tolerance is raised to at
 * least its flag tolerance, so a ban-over sample is always a flag-over one;
 * the suspension length is clamped to 1..10080 minutes.
 *
 * These are TOLERANCES — how many are allowed at once. The first count that is
 * over is one more.
 */
export function geoTolerances(s: UISettings): {
  flag: GeoTierCounts
  ban: GeoTierCounts
  banAfterPolls: number
  banMinutes: number
} {
  const flag: GeoTierCounts = {
    countries: orDefault(s.geo_anomaly_max_places, FLAG_DEFAULT.countries),
    regions: orDefault(s.geo_anomaly_max_regions, FLAG_DEFAULT.regions),
    cities: orDefault(s.geo_anomaly_max_cities, FLAG_DEFAULT.cities),
  }
  const ban: GeoTierCounts = {
    countries: Math.max(orDefault(s.geo_anomaly_ban_max_countries, BAN_DEFAULT.countries), flag.countries),
    regions: Math.max(orDefault(s.geo_anomaly_ban_max_regions, BAN_DEFAULT.regions), flag.regions),
    cities: Math.max(orDefault(s.geo_anomaly_ban_max_cities, BAN_DEFAULT.cities), flag.cities),
  }
  return {
    flag,
    ban,
    banAfterPolls: orDefault(s.geo_anomaly_ban_after_polls, BAN_AFTER_DEFAULT),
    banMinutes: Math.min(orDefault(s.geo_anomaly_ban_duration_minutes, BAN_MINUTES_DEFAULT), BAN_MINUTES_MAX),
  }
}

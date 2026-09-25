// Pure helpers for the concurrent-location views: the Geo tab, the geo
// settings section and the per-group editor. No I/O, no React — each one
// either mirrors a server rule (geoTolerances) or decides what an admin reads
// first, so both are pinned by unit tests rather than by rendering.
import type { GeoAnomaly, GeoSpot, GeoTier } from '@/api/geoAnomalies'
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

export interface SpotTree {
  cc: string
  n: number
  regions: { region: string; n: number; cities: { city: string; n: number }[] }[]
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
 */
export function groupSpots(spots: GeoSpot[]): SpotTree[] {
  const countries = new Map<string, Map<string, Map<string, number>>>()
  for (const s of spots) {
    let regions = countries.get(s.cc)
    if (!regions) countries.set(s.cc, (regions = new Map()))
    let cities = regions.get(s.region)
    if (!cities) regions.set(s.region, (cities = new Map()))
    cities.set(s.city, (cities.get(s.city) ?? 0) + s.n)
  }
  const out: SpotTree[] = []
  for (const [cc, regions] of countries) {
    const rs: SpotTree['regions'] = []
    for (const [region, cities] of regions) {
      const cs = [...cities].map(([city, n]) => ({ city, n })).sort(bySpotOrder(c => c.city))
      rs.push({ region, n: cs.reduce((sum, c) => sum + c.n, 0), cities: cs })
    }
    rs.sort(bySpotOrder(r => r.region))
    out.push({ cc, n: rs.reduce((sum, r) => sum + r.n, 0), regions: rs })
  }
  return out.sort(bySpotOrder(c => c.cc))
}

/**
 * Rank by what the admin should look at first. The LATCH outranks the state:
 * a flagged account that went idle or unreadable is still flagged (the streak
 * froze), and ranking it by its "idle" would bury it below every clean row —
 * disconnecting for a while is the easiest evasion there is. It still sits
 * below a live flag, which is the stronger, current statement.
 */
function severityRank(r: GeoAnomaly): number {
  if (r.state === 'flagged') return 0
  if (r.flagged && (r.state === 'idle' || r.state === 'unknown')) return 1
  switch (r.state) {
    case 'suspect': return 2
    case 'unknown': return 3
    case 'clean': return 4
    default: return 5 // idle / exempt / disabled
  }
}

/** A sorted COPY (the input is the query cache's); ties newest first. */
export function sortBySeverity(rows: GeoAnomaly[]): GeoAnomaly[] {
  return [...rows].sort((a, b) => severityRank(a) - severityRank(b) || b.updated_at_ms - a.updated_at_ms)
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

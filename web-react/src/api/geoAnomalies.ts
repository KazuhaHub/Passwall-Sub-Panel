import { client } from './client'

/**
 * The tier a verdict was over at, coarsest first: two COUNTRIES is a stronger
 * statement than the cities that come with them. '' when nothing was over.
 */
export type GeoTier = '' | 'country' | 'region' | 'city'

/**
 * One place the account was seen, and how many of its concurrent sources
 * resolved there. region / city are '' when the location database did not
 * resolve that far — a country-only database yields country-only spots.
 */
export interface GeoSpot {
  cc: string
  region: string
  city: string
  n: number
}

/** Live sources deliberately set aside, by the rule that set them aside. */
export interface GeoExcluded {
  /** Held by 3+ accounts at once: a shared exit, not one person. */
  shared: number
  /** On the admin's ignore list. */
  listed: number
  /** A node or relay address PSP itself knows. */
  infra: number
  /** CGNAT (100.64.0.0/10), private, loopback, link-local. */
  internal: number
}

/**
 * The structured account of a verdict. It carries NO addresses, ever — only
 * places, counts and coverage.
 *
 * `v` 0 is a row an older build wrote (evidence not recorded); every other
 * field is then zero and `spots` is `[]`, never null — so `v`, not the shape,
 * is how a reader tells "not recorded" from "nothing found".
 */
export interface GeoEvidence {
  v: number
  spots: GeoSpot[]
  excluded: GeoExcluded
  /** Addresses the upstream still remembered that were not live at poll time. */
  stale: number
  coverage: { placed: number; unplaced: number; region_known: number; city_known: number }
  /** Distinct IPv4 /24 and IPv6 /48 networks among the judged sources. */
  networks: number
  spread: { countries: number; regions: number; region_country: string; cities: number; city_country: string }
}

/**
 * One user's current concurrent-location verdict.
 *
 * `reason` travels with `state` on purpose: an operator deciding whether to
 * act on an account needs why, and a flag with no explanation leaves them
 * trusting the panel blindly or ignoring it.
 */
export interface GeoAnomaly {
  user_id: number
  upn?: string
  display_name?: string
  /**
   * Seven states, and only `flagged` is meant to drive action. `suspect` is
   * the visible ramp; `unknown` / `exempt` / `disabled` / `idle` all mean the
   * detector is not in a position to judge — and they are indistinguishable
   * from "no flags" if a reader only counts the flagged ones.
   */
  state: 'disabled' | 'exempt' | 'unknown' | 'idle' | 'clean' | 'suspect' | 'flagged'
  reason: string
  /** The tier the flag (or the last over-sample) was raised at. Kept while
   *  the flag is latched, so an idle flagged row still says what raised it. */
  tier: GeoTier
  /**
   * The LATCH, separate from `state`. An idle or unreadable sample freezes the
   * streak, so a flagged account that disconnected reads state `idle` and is
   * still flagged; reading `state` alone makes that row look cleared.
   */
  flagged: boolean
  /** The COUNTRIES behind the verdict, co-travel folded. Regions and cities
   *  are in `evidence.spots`. */
  places: string[]
  /** Every address the upstream still remembered — its whole retention
   *  window, not what was judged (that is `concurrent_ips`). */
  live_ips: number
  /** Sources judged: live at poll time and not set aside. */
  concurrent_ips: number
  /** Live sources set aside (shared exits, ignore list, own nodes, internal). */
  excluded_ips: number
  /**
   * false means `live_ips` is a FLOOR: a panel holding this user's clients
   * could not be read. Must be rendered — a partial count shown as a total
   * reads as "this account is fine" exactly when the evidence is missing.
   */
  complete: boolean
  over_streak: number
  under_streak: number
  /** Consecutive judged polls over the SUSPENSION tolerances (0 where
   *  automatic suspension is off). Resets when a suspension fires. */
  ban_streak: number
  evidence: GeoEvidence
  updated_at_ms: number
  /** The account's current service hold, omitted when active. `geo_auto` means
   *  the detector itself suspended it; anything else is somebody else's hold. */
  service_disabled_reason?: string
  service_disabled_at_ms?: number
}

/** Every judged user, newest first. Unfiltered by design: the denominator has
 *  to stay reachable so a detector that has stopped working is visible. */
export async function listGeoAnomalies(signal?: AbortSignal): Promise<GeoAnomaly[]> {
  const { data } = await client.get<{ items: GeoAnomaly[] }>('/admin/geo-anomalies', { signal })
  return data.items ?? []
}

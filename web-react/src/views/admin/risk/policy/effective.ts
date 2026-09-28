import type { RiskPolicyKey, RiskPolicySettings } from './policyKeys'
import { GEO_BAN_MAX_DURATION_MINUTES, RISK_USAGE_RATIO_MIN, RISK_WINDOW_DAYS } from './policyLayout'

// WHAT THE SERVER WILL JUDGE WITH, for the draft as it stands (U33): the
// numbers the cards' "in effect" lines print. Every unset value comes from
// the defaults the server serves, never from a copy here — a copy is a
// second definition of each rule and the first thing to drift. What IS
// mirrored is how the server repairs a value (raised, held, clamped), each
// in the direction of not accusing, exactly as the domain does it.

export interface GeoTierCounts {
  countries: number
  regions: number
  cities: number
}

export interface GeoEffective {
  flag: GeoTierCounts
  ban: GeoTierCounts
  banAfterPolls: number
  banMinutes: number
}

export interface RiskEffective {
  minDays: number
  maxDevices: number
  ratio: number
  floorGB: number
}

/** The stored value when set (> 0), else the served default. */
function effectiveOf(draft: Partial<RiskPolicySettings>, defaults: Record<string, number>, key: RiskPolicyKey): number {
  const v = draft[key]
  return typeof v === 'number' && v > 0 ? v : defaults[key]
}

/**
 * The geo tolerances in effect, as domain.GeoPolicyFromSettings and
 * sanitized() make them: unset is the served default; each suspension
 * tolerance is raised to at least its flag tolerance, tier by tier, so a
 * ban-over sample is always a flag-over one; the suspension is clamped to
 * 1..10080 minutes. These are TOLERANCES — the first count that is over is
 * one more, which the caption adds.
 */
export function effectiveGeo(draft: Partial<RiskPolicySettings>, defaults: Record<string, number>): GeoEffective {
  const of = (key: RiskPolicyKey) => effectiveOf(draft, defaults, key)
  const flag: GeoTierCounts = {
    countries: of('geo_anomaly_max_places'),
    regions: of('geo_anomaly_max_regions'),
    cities: of('geo_anomaly_max_cities'),
  }
  return {
    flag,
    ban: {
      countries: Math.max(of('geo_anomaly_ban_max_countries'), flag.countries),
      regions: Math.max(of('geo_anomaly_ban_max_regions'), flag.regions),
      cities: Math.max(of('geo_anomaly_ban_max_cities'), flag.cities),
    },
    banAfterPolls: of('geo_anomaly_ban_after_polls'),
    banMinutes: Math.min(Math.max(of('geo_anomaly_ban_duration_minutes'), 1), GEO_BAN_MAX_DURATION_MINUTES),
  }
}

/**
 * The risk thresholds in effect, as domain.RiskPolicyFromSettings and
 * RiskPolicy.Bounded make them: unset is the served default; min_days is
 * held to the structural window and to the window in effect (the served
 * value of risk_window_days, when there is one); a ratio under 1.5 is
 * raised to it.
 */
export function effectiveRisk(
  draft: Partial<RiskPolicySettings>, defaults: Record<string, number>, windowDays: number | undefined,
): RiskEffective {
  const of = (key: RiskPolicyKey) => effectiveOf(draft, defaults, key)
  const window = windowDays !== undefined && windowDays > 0 ? Math.min(windowDays, RISK_WINDOW_DAYS) : RISK_WINDOW_DAYS
  return {
    minDays: Math.min(of('risk_min_days'), window),
    maxDevices: of('risk_max_devices'),
    ratio: Math.max(of('risk_usage_ratio'), RISK_USAGE_RATIO_MIN),
    floorGB: of('risk_usage_floor_gb'),
  }
}

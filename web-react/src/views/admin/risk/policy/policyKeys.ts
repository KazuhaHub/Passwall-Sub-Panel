import type { RiskPolicyKey, RiskPolicySettings, RiskPolicyView } from '@/api/riskCenter'
import keys from './policyKeys.json'

export type { RiskPolicyKey, RiskPolicySettings, RiskPolicyView }

/**
 * The policy's 48 keys in the server's order (ports.RiskCenterPolicyKeys).
 *
 * A JSON file rather than a literal so that one list is checked from both
 * sides: TestRiskCenterPolicyKeysMatchTheSPA holds the file to the Go
 * struct, and policyLayout.test holds it to ALL_POLICY_KEYS below — a record
 * the compiler holds to the RiskPolicyKey union. A key the server adds or
 * drops fails one of the two until the page learns it.
 */
export const POLICY_KEYS = keys as readonly RiskPolicyKey[]

/** Every key of the union, once: a missing or extra key is a type error. */
export const ALL_POLICY_KEYS: Record<RiskPolicyKey, true> = {
  geo_anomaly_scope: true, geo_anomaly_max_places: true, geo_anomaly_max_regions: true,
  geo_anomaly_max_cities: true, geo_anomaly_flag_after_polls: true, geo_anomaly_clear_after_polls: true,
  geo_anomaly_min_placed_ratio: true, geo_anomaly_co_travel: true, geo_anomaly_allow_anywhere: true,
  geo_anomaly_ignore_addresses: true, geo_anomaly_ban_enabled: true, geo_anomaly_ban_max_countries: true,
  geo_anomaly_ban_max_regions: true, geo_anomaly_ban_max_cities: true, geo_anomaly_ban_after_polls: true,
  geo_anomaly_ban_duration_minutes: true, geo_anomaly_fresh_window_seconds: true,
  geo_anomaly_shared_exit_min_users: true, geo_anomaly_ban_max_per_poll: true, geo_anomaly_lift_max_per_poll: true,
  geo_anomaly_infra_refresh_minutes: true, geo_anomaly_infra_host_ttl_minutes: true,
  risk_hwid_capture_off: true, risk_sub_spread_off: true, risk_devices_off: true, risk_usage_shift_off: true,
  risk_login_country_off: true, risk_min_days: true, risk_max_devices: true, risk_usage_ratio: true,
  risk_usage_floor_gb: true, risk_login_warmup_logins: true, risk_login_hold_days: true,
  risk_usage_warmup_days: true, risk_usage_flag_days: true, risk_usage_suspect_days: true,
  risk_refresh_interval_minutes: true, risk_first_delay_minutes: true, risk_alert_freshness_hours: true,
  risk_window_days: true, risk_login_lookback_days: true, risk_usage_baseline_days: true,
  risk_usage_recent_days: true, risk_connection_retention_days: true, risk_flag_record_retention_days: true,
  risk_live_snapshot_stale_minutes: true, risk_live_refresh_cooldown_seconds: true, risk_device_infer_hours: true,
}

/** A policy value as the page reads it: whatever the server stored. */
export type PolicyValue = RiskPolicySettings[RiskPolicyKey]

/**
 * The keys whose value in `draft` differs from `seed`: exactly what a save
 * sends (D10) and what makes the page dirty. Compared by value, so typing a
 * field back to what it was leaves the page clean.
 */
export function changedKeys(draft: RiskPolicySettings, seed: RiskPolicySettings): RiskPolicyKey[] {
  return POLICY_KEYS.filter(k => draft[k] !== seed[k])
}

/** The named keys of a policy, and nothing else. */
export function pick(s: RiskPolicySettings, keys: readonly RiskPolicyKey[]): Partial<RiskPolicySettings> {
  return Object.fromEntries(keys.map(k => [k, s[k]])) as Partial<RiskPolicySettings>
}

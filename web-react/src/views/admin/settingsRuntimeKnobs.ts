import type { RuntimeKnob, UISettings } from '@/api/settings'

/**
 * The detectors' former constants, shown in the Settings page's advanced
 * panel in this order: the location detector's, then the risk worker's.
 *
 * Data rather than literals inside SettingsView, so the rule that opens the
 * panel (advancedKnobConfigured) can be checked knob by knob without
 * rendering the whole page once per knob.
 */
export const ADVANCED_GEO_KNOBS = [
  'geo_anomaly_fresh_window_seconds', 'geo_anomaly_shared_exit_min_users',
  'geo_anomaly_ban_max_per_poll', 'geo_anomaly_lift_max_per_poll',
  'geo_anomaly_infra_refresh_minutes', 'geo_anomaly_infra_host_ttl_minutes',
] as const satisfies readonly RuntimeKnob[]

export const ADVANCED_RISK_KNOBS = [
  'risk_refresh_interval_minutes', 'risk_first_delay_minutes', 'risk_alert_freshness_hours',
  'risk_window_days', 'risk_usage_baseline_days', 'risk_usage_recent_days', 'risk_login_lookback_days',
] as const satisfies readonly RuntimeKnob[]

type AdvancedKnob = (typeof ADVANCED_GEO_KNOBS)[number] | (typeof ADVANCED_RISK_KNOBS)[number]

/**
 * Whether any former constant is configured, which opens the advanced panel:
 * a changed value must not hide behind a closed one. "Configured" is a stored
 * value above 0 — 0, or a negative the server reads the same way, is the
 * shipped default and leaves the panel closed.
 */
export function advancedKnobConfigured(settings: Pick<UISettings, AdvancedKnob>): boolean {
  return ADVANCED_GEO_KNOBS.some(k => settings[k] > 0) || ADVANCED_RISK_KNOBS.some(k => settings[k] > 0)
}

import type { PolicyFieldSpec as SharedPolicyFieldSpec } from '@/components/PolicyField'
import type { RiskPolicyKey, RiskPolicySettings } from './policyKeys'
import type { PresetDetector } from './presets'

// THE POLICY PAGE'S LAYOUT: which of the 48 keys goes on which card, in
// which part of it, with which label, hint and bounds. Data rather than
// JSX, so the rules the page stands on — every key placed exactly once, the
// runtime knobs captioned with their value in effect, a lit preset
// describing every number beside it — are checked key by key without
// rendering the page (policyLayout.test).
//
// Labels and hints keep the keys they had on the system settings page
// (settings.*), which no longer shows these knobs: the same words for the
// same knob wherever it is edited, the group editor's hints included.
//
// Bounds are the server's sanitizers', none invented, so the page stops an
// admin only where the server would silently change the number:
//   - geo: GeoPolicyFromSettings + sanitized() — the placed ratio 0..1, the
//     suspension 1..GeoBanMaxDurationMinutes. The tolerances and check
//     counts are only held to >= 1, which any stored (> 0) whole number
//     already is, and a suspension tolerance is raised to its flag one —
//     a rule between two fields, shown by the "in effect" line, not a bound.
//   - risk: RiskPolicyFromSettings — min_days 1..RiskWindowDays, the ratio
//     >= RiskUsageRatioMin, and the five per-group thresholds' clamps.
//   - the runtime knobs: settingOr's lo..hi in GeoRuntimeFromSettings and
//     RiskRuntimeFromSettings. Floors that depend on another setting (two
//     traffic polls, the judged days, the lookback) are the "in effect"
//     caption's to show.

/** domain.GeoBanMaxDurationMinutes: the longest automatic suspension. */
export const GEO_BAN_MAX_DURATION_MINUTES = 10080
/** domain.RiskUsageRatioMin: a lower ratio accuses more, so it is raised. */
export const RISK_USAGE_RATIO_MIN = 1.5
/** domain.RiskWindowDays: the fetch window's structural ceiling. */
export const RISK_WINDOW_DAYS = 7

export type PolicyFieldKind = 'number' | 'float' | 'switch' | 'inverted' | 'select' | 'text'

export interface PolicyFieldSpec extends SharedPolicyFieldSpec {
  key: RiskPolicyKey
  kind: PolicyFieldKind
  /** Full i18n keys, namespace included. */
  label: string
  hint?: string
  /** Stored values above 0 outside these bounds are marked and block the save. */
  min?: number
  max?: number
  step?: number
  /** The helper text ends in the composed range and served default (§3.8 †). */
  tail?: boolean
  /** A runtime knob: its caption is the value the server runs with. */
  effective?: boolean
  multiline?: boolean
  /** 'select' only. */
  options?: { value: string; label: string }[]
}

export type PolicyCardId = 'geo' | 'sub_spread' | 'devices' | 'usage_shift' | 'login_country' | 'data'

export interface PolicyCardSpec {
  id: PolicyCardId
  title: string
  desc: string
  /**
   * The card's on/off switch. The geo card's is its scope ('off' is off);
   * the four risk signals' are their negative *_off keys. The data card has
   * none: nothing on it detects.
   */
  toggle?: { kind: 'geo_scope' } | { kind: 'inverted'; key: RiskPolicyKey }
  preset?: PresetDetector
  /** The key thresholds, always shown. */
  fields: PolicyFieldSpec[]
  /** 处置: the geo card's automatic suspension. */
  disposal?: PolicyFieldSpec[]
  /** Behind the card's 高级 panel. */
  advanced?: PolicyFieldSpec[]
}

const GEO = 'admin:settings.geo_anomaly.'
const RISK = 'admin:settings.risk.'

/** A geo tolerance or check count: a † field under settings.geo_anomaly.*. */
function geoNumber(key: RiskPolicyKey, name: string, hint = `${name}_hint`): PolicyFieldSpec {
  return { key, kind: 'number', label: `${GEO}${name}`, hint: `${GEO}${hint}`, tail: true }
}

/**
 * A runtime knob: label and hint under settings.<ns>.<name>, where name is
 * the key less its geo_anomaly_ / risk_ prefix — the keys the settings page
 * used for the same knob.
 */
function knob(key: RiskPolicyKey, ns: 'geo_anomaly' | 'risk' | 'risk_center', min: number, max: number): PolicyFieldSpec {
  const name = key.replace(/^(geo_anomaly|risk)_/, '')
  return {
    key, kind: 'number', label: `admin:settings.${ns}.${name}`, hint: `admin:settings.${ns}.${name}_hint`,
    min, max, effective: true,
  }
}

export const POLICY_CARDS: PolicyCardSpec[] = withRiskCopy([
  {
    id: 'geo',
    title: 'admin:risk_center.policy.card.geo',
    desc: 'admin:risk_center.policy.card.geo_desc',
    toggle: { kind: 'geo_scope' },
    preset: 'geo',
    fields: [
      {
        key: 'geo_anomaly_scope', kind: 'select', label: `${GEO}scope`, hint: `${GEO}scope_hint`,
        // 'off' is the card's switch, not a granularity.
        options: [
          { value: 'city', label: `${GEO}scope_city` },
          { value: 'region', label: `${GEO}scope_region` },
          { value: 'country', label: `${GEO}scope_country` },
        ],
      },
      geoNumber('geo_anomaly_max_places', 'max_places'),
      geoNumber('geo_anomaly_max_regions', 'max_regions'),
      geoNumber('geo_anomaly_max_cities', 'max_cities'),
      geoNumber('geo_anomaly_flag_after_polls', 'flag_after'),
      geoNumber('geo_anomaly_clear_after_polls', 'clear_after'),
    ],
    disposal: [
      { key: 'geo_anomaly_ban_enabled', kind: 'switch', label: `${GEO}ban_enabled`, hint: `${GEO}ban_hint` },
      geoNumber('geo_anomaly_ban_max_countries', 'ban_max_countries', 'ban_tolerance_hint'),
      geoNumber('geo_anomaly_ban_max_regions', 'ban_max_regions', 'ban_tolerance_hint'),
      geoNumber('geo_anomaly_ban_max_cities', 'ban_max_cities', 'ban_tolerance_hint'),
      geoNumber('geo_anomaly_ban_after_polls', 'ban_after'),
      {
        key: 'geo_anomaly_ban_duration_minutes', kind: 'number', label: `${GEO}ban_duration`,
        hint: `${GEO}ban_duration_hint`, min: 1, max: GEO_BAN_MAX_DURATION_MINUTES,
      },
    ],
    advanced: [
      {
        key: 'geo_anomaly_min_placed_ratio', kind: 'float', label: `${GEO}min_placed_ratio`,
        hint: `${GEO}min_placed_ratio_hint`, min: 0, max: 1, step: 0.05, tail: true,
      },
      { key: 'geo_anomaly_co_travel', kind: 'text', label: `${GEO}co_travel`, hint: `${GEO}co_travel_hint`, multiline: true },
      { key: 'geo_anomaly_allow_anywhere', kind: 'switch', label: `${GEO}allow_anywhere`, hint: `${GEO}allow_anywhere_hint` },
      {
        key: 'geo_anomaly_ignore_addresses', kind: 'text', label: `${GEO}ignore_addresses`,
        hint: `${GEO}ignore_addresses_hint`, multiline: true,
      },
      knob('geo_anomaly_fresh_window_seconds', 'geo_anomaly', 20, 900),
      knob('geo_anomaly_ban_max_per_poll', 'geo_anomaly', 1, 200),
      knob('geo_anomaly_lift_max_per_poll', 'geo_anomaly', 1, 200),
    ],
  },
  {
    id: 'sub_spread',
    title: 'admin:risk_center.policy.card.sub_spread',
    desc: 'admin:risk_center.policy.card.sub_spread_desc',
    toggle: { kind: 'inverted', key: 'risk_sub_spread_off' },
    preset: 'sub_spread',
    // min_days is shared with 设备数 and appears only here (P5); the 设备数
    // card states its value and that it follows this card's preset.
    fields: [{
      key: 'risk_min_days', kind: 'number', label: `${RISK}min_days`, hint: `${RISK}min_days_hint`,
      min: 1, max: RISK_WINDOW_DAYS, tail: true,
    }],
  },
  {
    id: 'devices',
    title: 'admin:risk_center.policy.card.devices',
    desc: 'admin:risk_center.policy.card.devices_desc',
    toggle: { kind: 'inverted', key: 'risk_devices_off' },
    preset: 'devices',
    fields: [{ key: 'risk_max_devices', kind: 'number', label: `${RISK}max_devices`, hint: `${RISK}max_devices_hint`, tail: true }],
  },
  {
    id: 'usage_shift',
    title: 'admin:risk_center.policy.card.usage_shift',
    desc: 'admin:risk_center.policy.card.usage_shift_desc',
    toggle: { kind: 'inverted', key: 'risk_usage_shift_off' },
    preset: 'usage_shift',
    fields: [
      {
        key: 'risk_usage_ratio', kind: 'float', label: `${RISK}usage_ratio`, hint: `${RISK}usage_ratio_hint`,
        min: RISK_USAGE_RATIO_MIN, step: 0.5, tail: true,
      },
      { key: 'risk_usage_floor_gb', kind: 'number', label: `${RISK}usage_floor_gb`, hint: `${RISK}usage_floor_gb_hint`, tail: true },
    ],
    // The over-days live here rather than beside the ratio: they are not
    // preset keys, and a visible key field a lit preset does not set would
    // let the card name a preset it no longer is (U19).
    advanced: [
      knob('risk_usage_flag_days', 'risk', 2, 14),
      knob('risk_usage_suspect_days', 'risk', 2, 14),
      knob('risk_usage_warmup_days', 'risk', 7, 56),
      knob('risk_usage_baseline_days', 'risk', 14, 56),
      knob('risk_usage_recent_days', 'risk', 3, 14),
    ],
  },
  {
    id: 'login_country',
    title: 'admin:risk_center.policy.card.login_country',
    desc: 'admin:risk_center.policy.card.login_country_desc',
    toggle: { kind: 'inverted', key: 'risk_login_country_off' },
    fields: [],
    advanced: [
      knob('risk_login_warmup_logins', 'risk', 1, 50),
      knob('risk_login_hold_days', 'risk', 1, 365),
      knob('risk_login_lookback_days', 'risk', 7, 365),
    ],
  },
  {
    id: 'data',
    title: 'admin:risk_center.policy.card.data',
    desc: 'admin:risk_center.policy.card.data_desc',
    fields: [
      knob('risk_connection_retention_days', 'risk_center', 1, 90),
      knob('risk_flag_record_retention_days', 'risk_center', 1, 3650),
      knob('risk_live_snapshot_stale_minutes', 'risk_center', 1, 1440),
      knob('risk_live_refresh_cooldown_seconds', 'risk_center', 5, 3600),
      knob('risk_device_infer_hours', 'risk_center', 1, 168),
      { key: 'risk_hwid_capture_off', kind: 'inverted', label: `${RISK}hwid_capture`, hint: `${RISK}hwid_capture_hint` },
    ],
    advanced: [
      knob('risk_refresh_interval_minutes', 'risk', 10, 1440),
      knob('risk_first_delay_minutes', 'risk', 1, 60),
      knob('risk_alert_freshness_hours', 'risk', 1, 720),
      knob('risk_window_days', 'risk', 1, RISK_WINDOW_DAYS),
      knob('geo_anomaly_shared_exit_min_users', 'geo_anomaly', 2, 20),
      knob('geo_anomaly_infra_refresh_minutes', 'geo_anomaly', 1, 60),
      knob('geo_anomaly_infra_host_ttl_minutes', 'geo_anomaly', 1, 1440),
    ],
  },
])

/** Every field of a card, in page order. */
export function cardFields(card: PolicyCardSpec): PolicyFieldSpec[] {
  return [...card.fields, ...(card.disposal ?? []), ...(card.advanced ?? [])]
}

/** Every key a card edits, its switch included. */
export function cardKeys(card: PolicyCardSpec): RiskPolicyKey[] {
  const toggle = card.toggle?.kind === 'inverted' ? [card.toggle.key] : []
  return [...toggle, ...cardFields(card).map(f => f.key)]
}

/**
 * Whether a stored value is outside the field's bounds. Only a SET number
 * is judged: 0 (or a negative the server reads the same way) is the
 * default, which is in range by construction.
 */
export function outOfRange(spec: PolicyFieldSpec, value: unknown): boolean {
  if ((spec.kind !== 'number' && spec.kind !== 'float') || typeof value !== 'number' || !(value > 0)) return false
  return (spec.min !== undefined && value < spec.min) || (spec.max !== undefined && value > spec.max)
}

/**
 * Whether anything behind a card's 高级 panel is configured, which opens
 * it: a changed value must not hide behind a closed panel. A number above
 * 0, a text that is not blank (the server trims it), or a switch that is on
 * — every switch's default is off.
 */
export function advancedConfigured(card: PolicyCardSpec, draft: Partial<RiskPolicySettings>): boolean {
  return (card.advanced ?? []).some(f => {
    const v = draft[f.key]
    if (typeof v === 'number') return v > 0
    if (typeof v === 'string') return v.trim() !== ''
    return v === true
  })
}

// Preserve the existing policy page's words while sharing its field renderer.
function withRiskCopy(cards: PolicyCardSpec[]): PolicyCardSpec[] {
  const copy = {
    range_default: 'admin:risk_center.policy.range_default', min_default: 'admin:risk_center.policy.min_default',
    default_only: 'admin:risk_center.policy.default_only', out_of_range: 'admin:risk_center.policy.out_of_range',
    reset_default: 'admin:risk_center.policy.reset_default', default_adornment: 'admin:risk_center.policy.default_adornment',
    effective: 'admin:settings.risk_center.effective',
  }
  const field = (spec: PolicyFieldSpec): PolicyFieldSpec => ({ ...spec, copy })
  return cards.map(card => ({ ...card, fields: card.fields.map(field), disposal: card.disposal?.map(field), advanced: card.advanced?.map(field) }))
}

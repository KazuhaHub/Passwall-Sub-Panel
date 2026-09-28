import type { RiskPolicyKey, RiskPolicySettings } from './policyKeys'

// THE PRESETS: three named settings of a detector's KEY thresholds, and
// nothing else — the advanced knobs, the switches and the automatic
// suspension are never touched by one. 标准 is exactly the shipped default
// (P5: domain.DefaultGeoPolicy 1/1/2, 3/6; DefaultRiskPolicy min_days 3,
// max_devices 3, ratio 3, floor 3 GB), which the tests hold against the
// defaults the server serves. The country tolerance is 1 in all three, and
// 宽松 does not shorten the checks needed to clear: a looser detector must
// still not shed a flag faster.
//
// Only the NAMES and their numbers live here. Which preset a card shows is
// computed from the draft and the served defaults (detectPreset), never
// stored, so a value typed by hand lights 自定义 at once.

export type PresetDetector = 'geo' | 'sub_spread' | 'devices' | 'usage_shift'
export type PresetName = 'loose' | 'standard' | 'strict' | 'custom'
export const PRESET_NAMES = ['loose', 'standard', 'strict'] as const

type PresetValues = Partial<Record<RiskPolicyKey, number>>

export const PRESETS: Record<PresetDetector, Record<Exclude<PresetName, 'custom'>, PresetValues>> = {
  geo: {
    loose: {
      geo_anomaly_max_places: 1, geo_anomaly_max_regions: 2, geo_anomaly_max_cities: 3,
      geo_anomaly_flag_after_polls: 5, geo_anomaly_clear_after_polls: 6,
    },
    standard: {
      geo_anomaly_max_places: 1, geo_anomaly_max_regions: 1, geo_anomaly_max_cities: 2,
      geo_anomaly_flag_after_polls: 3, geo_anomaly_clear_after_polls: 6,
    },
    strict: {
      geo_anomaly_max_places: 1, geo_anomaly_max_regions: 1, geo_anomaly_max_cities: 1,
      geo_anomaly_flag_after_polls: 2, geo_anomaly_clear_after_polls: 8,
    },
  },
  sub_spread: { loose: { risk_min_days: 4 }, standard: { risk_min_days: 3 }, strict: { risk_min_days: 2 } },
  devices: { loose: { risk_max_devices: 5 }, standard: { risk_max_devices: 3 }, strict: { risk_max_devices: 2 } },
  usage_shift: {
    loose: { risk_usage_ratio: 5, risk_usage_floor_gb: 5 },
    standard: { risk_usage_ratio: 3, risk_usage_floor_gb: 3 },
    strict: { risk_usage_ratio: 2, risk_usage_floor_gb: 2 },
  },
}

/** The keys a detector's presets set (the same for all three). */
export function presetKeys(detector: PresetDetector): RiskPolicyKey[] {
  return Object.keys(PRESETS[detector].standard) as RiskPolicyKey[]
}

/**
 * The number the server judges a key with: the stored value when it is
 * set (> 0), else the served default. A key whose default the server did
 * not send has no effective value, and matches no preset.
 */
function effectiveOf(key: RiskPolicyKey, draft: Partial<RiskPolicySettings>, defaults: Record<string, number>) {
  const v = draft[key]
  return typeof v === 'number' && v > 0 ? v : defaults[key]
}

/**
 * The first preset whose every key the draft judges with, else 自定义.
 * Compared on the EFFECTIVE value, so an unset field and one typed equal
 * to its default both read as the default.
 */
export function detectPreset(
  detector: PresetDetector, draft: Partial<RiskPolicySettings>, defaults: Record<string, number>,
): PresetName {
  for (const name of PRESET_NAMES) {
    const values = PRESETS[detector][name]
    if (Object.entries(values).every(([k, v]) => effectiveOf(k as RiskPolicyKey, draft, defaults) === v)) return name
  }
  return 'custom'
}

/**
 * What applying a preset writes into the draft: a value that IS the served
 * default is stored unset (0), so the panel keeps following the shipped
 * default if a later release moves it; every other value as the number.
 */
export function applyPreset(
  detector: PresetDetector, preset: Exclude<PresetName, 'custom'>, defaults: Record<string, number>,
): Partial<RiskPolicySettings> {
  return Object.fromEntries(
    Object.entries(PRESETS[detector][preset]).map(([k, v]) => [k, v === defaults[k] ? 0 : v]),
  ) as Partial<RiskPolicySettings>
}

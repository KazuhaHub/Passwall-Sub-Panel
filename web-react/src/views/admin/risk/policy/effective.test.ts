import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/client', () => ({ client: {} }))

import type { RiskPolicySettings } from '@/api/riskCenter'
import { effectiveGeo, effectiveRisk } from './effective'

// Served defaults deliberately unlike the shipped ones, so a number that
// came from a copy in the SPA instead of from the server shows up as wrong.
const DEFAULTS: Record<string, number> = {
  geo_anomaly_max_places: 2, geo_anomaly_max_regions: 3, geo_anomaly_max_cities: 4,
  geo_anomaly_flag_after_polls: 5, geo_anomaly_clear_after_polls: 9,
  geo_anomaly_ban_max_countries: 3, geo_anomaly_ban_max_regions: 5, geo_anomaly_ban_max_cities: 6,
  geo_anomaly_ban_after_polls: 7, geo_anomaly_ban_duration_minutes: 45,
  risk_min_days: 4, risk_max_devices: 6, risk_usage_ratio: 4, risk_usage_floor_gb: 8,
}

function geo(over: Partial<RiskPolicySettings> = {}): Partial<RiskPolicySettings> {
  return {
    geo_anomaly_max_places: 0, geo_anomaly_max_regions: 0, geo_anomaly_max_cities: 0,
    geo_anomaly_ban_max_countries: 0, geo_anomaly_ban_max_regions: 0, geo_anomaly_ban_max_cities: 0,
    geo_anomaly_ban_after_polls: 0, geo_anomaly_ban_duration_minutes: 0,
    ...over,
  }
}

function risk(over: Partial<RiskPolicySettings> = {}): Partial<RiskPolicySettings> {
  return { risk_min_days: 0, risk_max_devices: 0, risk_usage_ratio: 0, risk_usage_floor_gb: 0, ...over }
}

describe('effectiveGeo', () => {
  it('reads every unset value from the served defaults', () => {
    expect(effectiveGeo(geo(), DEFAULTS)).toEqual({
      flag: { countries: 2, regions: 3, cities: 4 },
      ban: { countries: 3, regions: 5, cities: 6 },
      banAfterPolls: 7,
      banMinutes: 45,
    })
  })

  // domain sanitized(): ban-over always implies flag-over, tier by tier, so
  // a suspension threshold below the flag one is judged AT the flag one.
  it('raises each ban tolerance to its flag tolerance', () => {
    const eff = effectiveGeo(geo({
      geo_anomaly_max_places: 4, geo_anomaly_max_regions: 6, geo_anomaly_max_cities: 2,
      geo_anomaly_ban_max_countries: 1, geo_anomaly_ban_max_regions: 1, geo_anomaly_ban_max_cities: 9,
    }), DEFAULTS)
    expect(eff.ban).toEqual({ countries: 4, regions: 6, cities: 9 })
  })

  it('clamps the suspension to 1..10080 minutes', () => {
    expect(effectiveGeo(geo({ geo_anomaly_ban_duration_minutes: 99999 }), DEFAULTS).banMinutes).toBe(10080)
    expect(effectiveGeo(geo({ geo_anomaly_ban_duration_minutes: 30 }), DEFAULTS).banMinutes).toBe(30)
  })
})

describe('effectiveRisk', () => {
  it('reads every unset value from the served defaults', () => {
    expect(effectiveRisk(risk(), DEFAULTS, 7)).toEqual({ minDays: 4, maxDevices: 6, ratio: 4, floorGB: 8 })
  })

  // RiskPolicy.Bounded: a place or device cannot recur on more days than
  // the fetch window in effect holds.
  it('holds min_days to the window in effect', () => {
    expect(effectiveRisk(risk({ risk_min_days: 6 }), DEFAULTS, 5).minDays).toBe(5)
    expect(effectiveRisk(risk(), DEFAULTS, 2).minDays).toBe(2)
    // RiskPolicyFromSettings also holds it to the structural 7-day window,
    // whatever the runtime says.
    expect(effectiveRisk(risk({ risk_min_days: 30 }), DEFAULTS, undefined).minDays).toBe(7)
  })

  // A lower ratio accuses more, so the server raises one below 1.5.
  it('raises the ratio to 1.5', () => {
    expect(effectiveRisk(risk({ risk_usage_ratio: 1.2 }), DEFAULTS, 7).ratio).toBe(1.5)
    expect(effectiveRisk(risk({ risk_usage_ratio: 2.5 }), DEFAULTS, 7).ratio).toBe(2.5)
  })
})

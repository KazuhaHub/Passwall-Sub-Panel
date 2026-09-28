import { describe, expect, it, vi } from 'vitest'

// The wire module the policy's types come from imports the shared axios
// client, which reads the document at import time. Nothing here makes a
// request.
vi.mock('@/api/client', () => ({ client: {} }))

import type { RiskPolicySettings } from '@/api/riskCenter'
import { applyPreset, detectPreset, PRESETS, presetKeys } from './presets'

// What the server serves as `defaults` (ports.RiskCenterPolicyDefaults): the
// shipped value of every numeric key. 标准 is exactly these, which is what
// makes an untouched install read as 标准.
const DEFAULTS: Record<string, number> = {
  geo_anomaly_max_places: 1, geo_anomaly_max_regions: 1, geo_anomaly_max_cities: 2,
  geo_anomaly_flag_after_polls: 3, geo_anomaly_clear_after_polls: 6,
  risk_min_days: 3, risk_max_devices: 3, risk_usage_ratio: 3, risk_usage_floor_gb: 3,
}

/** A draft of the preset keys only, every one unset unless given. */
function draft(over: Partial<RiskPolicySettings> = {}): Partial<RiskPolicySettings> {
  return {
    geo_anomaly_max_places: 0, geo_anomaly_max_regions: 0, geo_anomaly_max_cities: 0,
    geo_anomaly_flag_after_polls: 0, geo_anomaly_clear_after_polls: 0,
    risk_min_days: 0, risk_max_devices: 0, risk_usage_ratio: 0, risk_usage_floor_gb: 0,
    ...over,
  }
}

describe('PRESETS', () => {
  // 标准 is the shipped default, key for key (P5): a panel nobody tuned must
  // read as 标准, and applying 标准 must change nothing the server judges.
  it('makes standard the served defaults', () => {
    for (const detector of ['geo', 'sub_spread', 'devices', 'usage_shift'] as const) {
      for (const [key, value] of Object.entries(PRESETS[detector].standard)) {
        expect(value, key).toBe(DEFAULTS[key])
      }
    }
  })

  // Every preset of a detector names the same keys: a preset that left one
  // out would leave a stale value behind it and light up over a card it
  // does not describe.
  it('gives each preset of a detector the same keys', () => {
    for (const detector of ['geo', 'sub_spread', 'devices', 'usage_shift'] as const) {
      const keys = presetKeys(detector)
      for (const name of ['loose', 'standard', 'strict'] as const) {
        expect(Object.keys(PRESETS[detector][name]).sort(), `${detector}.${name}`).toEqual([...keys].sort())
      }
    }
  })
})

describe('detectPreset', () => {
  it('reads an all-unset draft as standard', () => {
    for (const detector of ['geo', 'sub_spread', 'devices', 'usage_shift'] as const) {
      expect(detectPreset(detector, draft(), DEFAULTS), detector).toBe('standard')
    }
  })

  // A typed value equal to the default is the default: the server judges
  // with the same number either way, so the card must not call it custom.
  it('still reads standard when a value is typed equal to its default', () => {
    expect(detectPreset('geo', draft({ geo_anomaly_max_cities: 2, geo_anomaly_clear_after_polls: 6 }), DEFAULTS))
      .toBe('standard')
    expect(detectPreset('usage_shift', draft({ risk_usage_ratio: 3 }), DEFAULTS)).toBe('standard')
  })

  it('reads loose and strict from their values', () => {
    expect(detectPreset('geo', draft({
      geo_anomaly_max_regions: 2, geo_anomaly_max_cities: 3, geo_anomaly_flag_after_polls: 5,
    }), DEFAULTS)).toBe('loose')
    expect(detectPreset('geo', draft({
      geo_anomaly_max_cities: 1, geo_anomaly_flag_after_polls: 2, geo_anomaly_clear_after_polls: 8,
    }), DEFAULTS)).toBe('strict')
    expect(detectPreset('devices', draft({ risk_max_devices: 2 }), DEFAULTS)).toBe('strict')
    expect(detectPreset('sub_spread', draft({ risk_min_days: 4 }), DEFAULTS)).toBe('loose')
  })

  // One hand-typed value that matches no preset is custom, and the card
  // must say so rather than keep a preset lit over a value it did not set.
  it('reads a value matching no preset as custom', () => {
    expect(detectPreset('geo', draft({ geo_anomaly_max_cities: 4 }), DEFAULTS)).toBe('custom')
    expect(detectPreset('usage_shift', draft({ risk_usage_ratio: 2.5 }), DEFAULTS)).toBe('custom')
  })

  // The defaults are the server's: a panel whose shipped default moved
  // detects against the new one, with no copy in the SPA to lag behind.
  it('compares against the served defaults', () => {
    const moved = { ...DEFAULTS, risk_max_devices: 5 }
    expect(detectPreset('devices', draft(), moved)).toBe('loose')
  })
})

describe('applyPreset', () => {
  // 标准 writes zeros: the page stores "unset", so a later change of a
  // shipped default reaches this panel too.
  it('writes standard as unset', () => {
    for (const detector of ['geo', 'sub_spread', 'devices', 'usage_shift'] as const) {
      const out = applyPreset(detector, 'standard', DEFAULTS)
      for (const [key, value] of Object.entries(out)) expect(value, key).toBe(0)
    }
  })

  // A preset value that IS the default is stored unset too; every other
  // one as the number itself.
  it('writes loose geo with the defaults unset and the rest as numbers', () => {
    expect(applyPreset('geo', 'loose', DEFAULTS)).toEqual({
      geo_anomaly_max_places: 0,
      geo_anomaly_max_regions: 2,
      geo_anomaly_max_cities: 3,
      geo_anomaly_flag_after_polls: 5,
      geo_anomaly_clear_after_polls: 0,
    })
  })

  it('round-trips: an applied preset is detected as itself', () => {
    for (const detector of ['geo', 'sub_spread', 'devices', 'usage_shift'] as const) {
      for (const name of ['loose', 'standard', 'strict'] as const) {
        const applied = { ...draft(), ...applyPreset(detector, name, DEFAULTS) }
        expect(detectPreset(detector, applied, DEFAULTS), `${detector}.${name}`).toBe(name)
      }
    }
  })
})

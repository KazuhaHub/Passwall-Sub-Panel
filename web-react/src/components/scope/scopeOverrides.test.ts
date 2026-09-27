import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/api/scopeSettings', () => ({
  deleteGroupScopeOverride: vi.fn(),
  getGroupScopeSettings: vi.fn(),
  setGroupScopeOverride: vi.fn(),
}))
vi.mock('@/api/settings', () => ({ getUISettings: vi.fn() }))

import {
  deleteGroupScopeOverride,
  getGroupScopeSettings,
  setGroupScopeOverride,
} from '@/api/scopeSettings'
import { getUISettings } from '@/api/settings'
import {
  kvFromGlobal,
  loadScopeState,
  saveScopeState,
  SCOPE_CATEGORIES,
  SCOPE_KEYS,
  type ScopeState,
} from './scopeOverrides'

describe('scope override state', () => {
  beforeEach(() => vi.clearAllMocks())

  it('serializes global values into the backend KV representation', () => {
    expect(kvFromGlobal('bool', true)).toBe('1')
    expect(kvFromGlobal('bool', false)).toBe('0')
    expect(kvFromGlobal('int', 12)).toBe('12')
    expect(kvFromGlobal('float', 1.25)).toBe('1.25')
    expect(kvFromGlobal('str', null)).toBe('')
  })

  it('merges sparse overrides with inherited global settings', async () => {
    vi.mocked(getGroupScopeSettings).mockResolvedValue({
      scope_type: 'group',
      scope_id: 42,
      overridable: ['security.totp_enabled', 'notify.expire_before_days'],
      overrides: { 'notify.expire_before_days': '21' },
    })
    vi.mocked(getUISettings).mockResolvedValue({
      totp_enabled: true,
      expire_before_days: 7,
    } as Awaited<ReturnType<typeof getUISettings>>)

    const state = await loadScopeState(42)

    expect(getGroupScopeSettings).toHaveBeenCalledWith(42, undefined)
    expect(state.global['security.totp_enabled']).toBe('1')
    expect(state.edit['security.totp_enabled']).toEqual({ on: false, value: '1' })
    expect(state.global['notify.expire_before_days']).toBe('7')
    expect(state.edit['notify.expire_before_days']).toEqual({ on: true, value: '21' })
  })

  it('persists only changed allowlisted values', async () => {
    const state: ScopeState = {
      overridable: [
        'security.totp_enabled',
        'security.passkey_enabled',
        'notify.expire_before_days',
        'notify.traffic_remain_percent',
      ],
      global: {},
      orig: {
        'security.totp_enabled': '1',
        'notify.expire_before_days': '7',
        'notify.traffic_remain_percent': '20',
      },
      edit: {
        'security.totp_enabled': { on: false, value: '1' },
        'security.passkey_enabled': { on: true, value: '1' },
        'notify.expire_before_days': { on: true, value: '14' },
        'notify.traffic_remain_percent': { on: true, value: '20' },
        // Present in editor state, but absent from the server allowlist.
        'security.emergency_access_enabled': { on: true, value: '1' },
      },
    }

    await saveScopeState(9, state)

    expect(deleteGroupScopeOverride).toHaveBeenCalledTimes(1)
    expect(deleteGroupScopeOverride).toHaveBeenCalledWith(9, 'security', 'totp_enabled')
    expect(setGroupScopeOverride).toHaveBeenCalledTimes(2)
    expect(setGroupScopeOverride).toHaveBeenNthCalledWith(1, 9, 'security', 'passkey_enabled', '1')
    expect(setGroupScopeOverride).toHaveBeenNthCalledWith(2, 9, 'notify', 'expire_before_days', '14')
  })
})

describe('geo catalog', () => {
  const geoKeys = (cat: string) => SCOPE_KEYS.filter(k => k.cat === cat).map(k => k.key)

  it('has the geo and geo_ban keys and never ignore_addresses', () => {
    expect(SCOPE_CATEGORIES.map(c => c.id)).toEqual(expect.arrayContaining(['geo', 'geo_ban']))
    expect(geoKeys('geo')).toEqual([
      'geo_anomaly.scope',
      'geo_anomaly.max_places',
      'geo_anomaly.max_regions',
      'geo_anomaly.max_cities',
      'geo_anomaly.flag_after_polls',
      'geo_anomaly.clear_after_polls',
      'geo_anomaly.co_travel',
      'geo_anomaly.allow_anywhere',
    ])
    expect(geoKeys('geo_ban')).toEqual([
      'geo_anomaly.ban_enabled',
      'geo_anomaly.ban_max_countries',
      'geo_anomaly.ban_max_regions',
      'geo_anomaly.ban_max_cities',
      'geo_anomaly.ban_after_polls',
      'geo_anomaly.ban_duration_minutes',
    ])
    // The ignore list names fleet infrastructure (a relay, an office exit),
    // not a population's tolerance: global only. The backend refuses the
    // override anyway; a row here would only offer a switch that 400s.
    expect(SCOPE_KEYS.some(k => k.key === 'geo_anomaly.ignore_addresses')).toBe(false)
    // Every row's key is its type.name, so saveScopeState writes what it shows.
    for (const k of SCOPE_KEYS.filter(k => k.type === 'geo_anomaly')) {
      expect(`${k.type}.${k.name}`).toBe(k.key)
    }
  })

  it('makes the scope row an enum with the four options, defaulting to city', () => {
    const row = SCOPE_KEYS.find(k => k.key === 'geo_anomaly.scope')
    expect(row?.kind).toBe('enum')
    expect(row?.options?.map(o => o.value)).toEqual(['city', 'region', 'country', 'off'])
    expect(row?.enumDefault).toBe('city')
  })

  it('names each numeric row\'s shipped default, so an unset 0 reads as it', () => {
    const unset = Object.fromEntries(
      SCOPE_KEYS.filter(k => k.type === 'geo_anomaly' && k.kind === 'int').map(k => [k.name, k.unsetValue]),
    )
    expect(unset).toEqual({
      max_places: '1',
      max_regions: '1',
      max_cities: '2',
      flag_after_polls: '3',
      clear_after_polls: '6',
      ban_max_countries: '1',
      ban_max_regions: '2',
      ban_max_cities: '3',
      ban_after_polls: '6',
      ban_duration_minutes: '60',
    })
  })

  it('keeps an unset enum as the empty string rather than inventing a value', () => {
    // '' is what the server stores for "never configured"; the editor shows
    // it as the default, but the inherited baseline stays exactly what the
    // server said.
    expect(kvFromGlobal('enum', '')).toBe('')
    expect(kvFromGlobal('enum', 'region')).toBe('region')
  })
})

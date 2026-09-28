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
import zhBundle from '@/locales/zh-CN/admin.json'
import enBundle from '@/locales/en-US/admin.json'
import { flatten, type Nested } from '@/i18n/options'
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

describe('risk catalog', () => {
  const riskRows = () => SCOPE_KEYS.filter(k => k.cat === 'risk')

  it('has exactly the thirteen overridable risk keys, in order', () => {
    // The mirror of ports.OverridableScopeKeys' risk block: the four
    // switches, the four tolerances, then usage_shift's three thresholds and
    // login_country's two, which became per-group settings with the risk
    // center.
    expect(SCOPE_CATEGORIES.find(c => c.id === 'risk')).toEqual({ id: 'risk', labelKey: 'cat_risk', def: '风险信号（只提示）' })
    expect(riskRows().map(k => k.key)).toEqual([
      'risk.sub_spread_off',
      'risk.devices_off',
      'risk.usage_shift_off',
      'risk.login_country_off',
      'risk.min_days',
      'risk.max_devices',
      'risk.usage_ratio',
      'risk.usage_floor_gb',
      'risk.usage_warmup_days',
      'risk.usage_flag_days',
      'risk.usage_suspect_days',
      'risk.login_warmup_logins',
      'risk.login_hold_days',
    ])
    expect(riskRows().map(k => k.kind)).toEqual(
      ['bool', 'bool', 'bool', 'bool', 'int', 'int', 'float', 'int', 'int', 'int', 'int', 'int', 'int'])
  })

  it('never offers hwid capture per group', () => {
    // /sub reads it from the global settings on every fetch, before the
    // account's group is known; the backend refuses the override. A row here
    // would only offer a switch that 400s.
    expect(SCOPE_KEYS.some(k => k.key === 'risk.hwid_capture_off')).toBe(false)
  })

  it('names the shipped default of every numeric row, and writes what it shows', () => {
    // domain.DefaultRiskPolicy: a stored 0 means "never configured" — never
    // "no device allowed", "a zero-byte floor" or "flag on no days".
    const unset = Object.fromEntries(riskRows().filter(k => k.kind !== 'bool').map(k => [k.key, k.unsetValue]))
    expect(unset).toEqual({
      'risk.min_days': '3',
      'risk.max_devices': '3',
      'risk.usage_ratio': '3',
      'risk.usage_floor_gb': '3',
      'risk.usage_warmup_days': '14',
      'risk.usage_flag_days': '4',
      'risk.usage_suspect_days': '2',
      'risk.login_warmup_logins': '3',
      'risk.login_hold_days': '7',
    })
    for (const k of riskRows()) {
      expect(`${k.type}.${k.name}`).toBe(k.key)
      expect(k.field).toBe(`risk_${k.name}`)
      expect(k.labelKey).toBe(`risk_${k.name}`)
    }
  })
})

// The group editor on the risk center's policy page shows each geo and risk
// row's hint under its label (showHints), so a group admin reads the same
// explanation the global field carries. The hints are the policy page's own
// settings.* texts, which is why none of them may point at a place on a page
// (「上面」「下方」) or restate "0 = default": the editor is not that page,
// and its inherited value already says the default.
describe('geo and risk hints', () => {
  const HINTLESS = ['risk.devices_off', 'risk.usage_shift_off', 'risk.login_country_off']
  const rows = () => SCOPE_KEYS.filter(k => ['geo', 'geo_ban', 'risk'].includes(k.cat))
  const zh = flatten(zhBundle as Nested)
  const en = flatten(enBundle as Nested)

  it('gives every geo and risk row a hint both bundles have, the three plain switches excepted', () => {
    const missing: string[] = []
    for (const k of rows()) {
      if (HINTLESS.includes(k.key)) {
        expect(k.hintKey, k.key).toBeUndefined()
        continue
      }
      expect(k.hintKey, k.key).toMatch(/^admin:settings\./)
      const flat = (k.hintKey ?? '').slice('admin:'.length)
      for (const [lang, dict] of Object.entries({ zh, en })) {
        if (typeof dict[flat] !== 'string' || dict[flat] === '') missing.push(`${lang}: ${k.key} → ${k.hintKey}`)
      }
    }
    expect(missing).toEqual([])
  })

  it('points at no place on a page and restates no default', () => {
    const found: string[] = []
    for (const k of rows()) {
      if (!k.hintKey) continue
      const flat = k.hintKey.slice('admin:'.length)
      for (const word of ['上面', '下方', '0 = 默认']) if (zh[flat]?.includes(word)) found.push(`zh ${flat}: ${word}`)
      // English "above"/"below" also compare numbers ("keep it above the
      // checks to flag"), so only the default phrase is checked there.
      if (en[flat]?.includes('0 = default')) found.push(`en ${flat}: 0 = default`)
    }
    expect(found).toEqual([])
  })
})

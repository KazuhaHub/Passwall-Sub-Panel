import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/client', () => ({ client: {} }))

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { flatten, type Nested } from '@/i18n/options'
import type { RiskPolicySettings } from '@/api/riskCenter'
import { ALL_POLICY_KEYS, POLICY_KEYS } from './policyKeys'
import { advancedConfigured, cardFields, cardKeys, POLICY_CARDS, type PolicyFieldSpec } from './policyLayout'
import { presetKeys } from './presets'

const bundles = { zh: flatten(zh as Nested), en: flatten(en as Nested) }

// The 23 runtime knobs (ports.RuntimeEffective): the ones the server states
// an "in effect" value for.
const RUNTIME_KNOBS = [
  'geo_anomaly_fresh_window_seconds', 'geo_anomaly_shared_exit_min_users',
  'geo_anomaly_ban_max_per_poll', 'geo_anomaly_lift_max_per_poll',
  'geo_anomaly_infra_refresh_minutes', 'geo_anomaly_infra_host_ttl_minutes',
  'risk_refresh_interval_minutes', 'risk_first_delay_minutes', 'risk_alert_freshness_hours',
  'risk_window_days', 'risk_usage_baseline_days', 'risk_usage_recent_days',
  'risk_usage_warmup_days', 'risk_usage_flag_days', 'risk_usage_suspect_days',
  'risk_login_warmup_logins', 'risk_login_hold_days', 'risk_login_lookback_days',
  'risk_connection_retention_days', 'risk_flag_record_retention_days',
  'risk_live_snapshot_stale_minutes', 'risk_live_refresh_cooldown_seconds', 'risk_device_infer_hours',
]

// §3.8's † fields: the ones whose helper text ends in the composed range
// and default. The runtime knobs' hints state their own.
const TAILED = [
  'geo_anomaly_max_places', 'geo_anomaly_max_regions', 'geo_anomaly_max_cities',
  'geo_anomaly_flag_after_polls', 'geo_anomaly_clear_after_polls',
  'geo_anomaly_ban_max_countries', 'geo_anomaly_ban_max_regions', 'geo_anomaly_ban_max_cities',
  'geo_anomaly_ban_after_polls', 'geo_anomaly_min_placed_ratio',
  'risk_min_days', 'risk_max_devices', 'risk_usage_ratio', 'risk_usage_floor_gb',
]

const everyField = (): PolicyFieldSpec[] => POLICY_CARDS.flatMap(cardFields)

function field(key: string): PolicyFieldSpec {
  const f = everyField().find(x => x.key === key)
  if (!f) throw new Error(`no field ${key}`)
  return f
}

describe('POLICY_KEYS', () => {
  // The JSON is the list a Go test holds to ports.RiskCenterPolicy; the
  // record is the list the compiler holds to the type. Equal, the page's
  // type and the server's policy name the same 48 keys.
  it('is the typed key set, 48 keys, each once', () => {
    expect(POLICY_KEYS).toHaveLength(48)
    expect(new Set(POLICY_KEYS).size).toBe(48)
    expect([...POLICY_KEYS].sort()).toEqual(Object.keys(ALL_POLICY_KEYS).sort())
  })
})

describe('POLICY_CARDS', () => {
  it('lays out the six cards in order', () => {
    expect(POLICY_CARDS.map(c => c.id)).toEqual(['geo', 'sub_spread', 'devices', 'usage_shift', 'login_country', 'data'])
  })

  // Every key has exactly one place on the page: a key laid out nowhere is
  // editable nowhere once the settings page hands the policy over, and a
  // key laid out twice is two fields fighting over one value.
  it('places every policy key exactly once, the detector switches included', () => {
    const placed = POLICY_CARDS.flatMap(cardKeys)
    expect([...placed].sort()).toEqual([...POLICY_KEYS].sort())
    expect(new Set(placed).size).toBe(placed.length)
  })

  it('marks exactly the runtime knobs as having a value in effect', () => {
    const withEffective = everyField().filter(f => f.effective).map(f => f.key)
    expect([...withEffective].sort()).toEqual([...RUNTIME_KNOBS].sort())
  })

  it('puts the composed range and default on exactly the § 3.8 fields', () => {
    const tailed = everyField().filter(f => f.tail).map(f => f.key)
    expect([...tailed].sort()).toEqual([...TAILED].sort())
  })

  // A lit preset must describe the card (U19): every number shown among a
  // card's key fields is one the preset sets, so no visible key field can
  // differ from the preset that is lit.
  it("makes every numeric key field of a card with a preset one of the preset's keys", () => {
    for (const card of POLICY_CARDS.filter(c => c.preset)) {
      const numeric = card.fields.filter(f => f.kind === 'number' || f.kind === 'float').map(f => f.key)
      expect([...numeric].sort(), card.id).toEqual([...presetKeys(card.preset!)].sort())
    }
  })

  it('names only keys both bundles have', () => {
    const keys = new Set<string>()
    for (const card of POLICY_CARDS) {
      keys.add(card.title)
      keys.add(card.desc)
      for (const f of cardFields(card)) {
        keys.add(f.label)
        if (f.hint) keys.add(f.hint)
        for (const o of f.options ?? []) keys.add(o.label)
      }
    }
    const missing: string[] = []
    for (const k of keys) {
      const flat = k.replace(/^admin:/, '')
      for (const [lang, dict] of Object.entries(bundles)) {
        if (typeof dict[flat] !== 'string' || dict[flat] === '') missing.push(`${lang}: ${k}`)
      }
    }
    expect(missing).toEqual([])
  })

  // The bounds are the server's sanitizers', none invented: an admin is
  // stopped only where the server would change the number.
  it('bounds the fields the server clamps', () => {
    expect(field('risk_usage_ratio')).toMatchObject({ kind: 'float', min: 1.5 })
    expect(field('risk_usage_ratio').max).toBeUndefined()
    expect(field('geo_anomaly_min_placed_ratio')).toMatchObject({ kind: 'float', min: 0, max: 1 })
    expect(field('geo_anomaly_ban_duration_minutes')).toMatchObject({ max: 10080 })
    expect(field('risk_min_days')).toMatchObject({ min: 1, max: 7 })
    expect(field('geo_anomaly_fresh_window_seconds')).toMatchObject({ min: 20, max: 900 })
    expect(field('risk_live_refresh_cooldown_seconds')).toMatchObject({ min: 5, max: 3600 })
    // A tolerance the server only raises to its flag one has no bound of
    // its own.
    expect(field('geo_anomaly_ban_max_cities').min).toBeUndefined()
    expect(field('geo_anomaly_ban_max_cities').max).toBeUndefined()
  })
})

describe('advancedConfigured', () => {
  const card = (id: string) => POLICY_CARDS.find(c => c.id === id)!
  const base = (over: Partial<RiskPolicySettings> = {}): RiskPolicySettings => ({
    // Every number unset, then the ten texts and switches at their zero.
    ...Object.fromEntries(POLICY_KEYS.map(k => [k, 0])),
    geo_anomaly_scope: '', geo_anomaly_co_travel: '', geo_anomaly_ignore_addresses: '',
    geo_anomaly_allow_anywhere: false, geo_anomaly_ban_enabled: false,
    risk_hwid_capture_off: false, risk_sub_spread_off: false, risk_devices_off: false,
    risk_usage_shift_off: false, risk_login_country_off: false,
    ...over,
  } as RiskPolicySettings)

  it('is closed while nothing advanced is configured', () => {
    for (const c of POLICY_CARDS) expect(advancedConfigured(c, base()), c.id).toBe(false)
  })

  it('opens on a number above 0, a non-empty text or a switched switch', () => {
    expect(advancedConfigured(card('geo'), base({ geo_anomaly_fresh_window_seconds: 60 }))).toBe(true)
    expect(advancedConfigured(card('geo'), base({ geo_anomaly_co_travel: 'JP,TW' }))).toBe(true)
    expect(advancedConfigured(card('geo'), base({ geo_anomaly_allow_anywhere: true }))).toBe(true)
    expect(advancedConfigured(card('usage_shift'), base({ risk_usage_flag_days: 3 }))).toBe(true)
    expect(advancedConfigured(card('data'), base({ geo_anomaly_infra_host_ttl_minutes: 30 }))).toBe(true)
  })

  // Unset is 0 or a negative the server reads the same way, and whitespace
  // is trimmed away when saved.
  it('stays closed for unset values and for key fields', () => {
    expect(advancedConfigured(card('geo'), base({ geo_anomaly_fresh_window_seconds: -1 }))).toBe(false)
    expect(advancedConfigured(card('geo'), base({ geo_anomaly_co_travel: '  ' }))).toBe(false)
    expect(advancedConfigured(card('geo'), base({ geo_anomaly_max_cities: 4 }))).toBe(false)
    expect(advancedConfigured(card('data'), base({ risk_connection_retention_days: 30 }))).toBe(false)
  })
})

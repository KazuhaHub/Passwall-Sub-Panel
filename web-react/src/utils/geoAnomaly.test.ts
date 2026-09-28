import { describe, expect, it, vi } from 'vitest'

// GEO_REASON_CODES lives beside the wire types, in a module that imports the
// shared axios client, which reads the document at import time. These are
// pure-function tests in a node environment; nothing here makes a request.
vi.mock('@/api/client', () => ({ client: {} }))

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { GEO_REASON_CODES, type GeoAnomaly, type GeoReasonCode, type GeoSpot, type GeoWhy } from '@/api/geoAnomalies'
import type { GeoIPStatus } from '@/api/settings'
import { flatten, type Nested } from '@/i18n/options'
import {
  activeDbIsCountryOnly, groupSpots, reasonText, spreadKm, tierLabelKey, type Translate,
} from './geoAnomaly'

function row(over: Partial<GeoAnomaly>): GeoAnomaly {
  return {
    user_id: 1,
    state: 'clean',
    reason: '',
    tier: '',
    flagged: false,
    places: [],
    live_ips: 0,
    concurrent_ips: 0,
    excluded_ips: 0,
    complete: true,
    over_streak: 0,
    under_streak: 0,
    ban_streak: 0,
    evidence: {
      v: 1, spots: [], excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
      coverage: { placed: 0, unplaced: 0, region_known: 0, city_known: 0 }, networks: 0,
      spread: { countries: 0, regions: 0, region_country: '', cities: 0, city_country: '' },
    },
    updated_at_ms: 0,
    ...over,
  }
}

describe('tierLabelKey', () => {
  it('names each tier under the geo tab namespace and nothing for no tier', () => {
    expect(tierLabelKey('country')).toBe('admin:geo_anomalies.tier_country')
    expect(tierLabelKey('region')).toBe('admin:geo_anomalies.tier_region')
    expect(tierLabelKey('city')).toBe('admin:geo_anomalies.tier_city')
    // A clean or never-over row has no tier; a chip reading "tier_" would be
    // a claim about nothing.
    expect(tierLabelKey('')).toBeNull()
  })
})

describe('groupSpots', () => {
  const spots: GeoSpot[] = [
    { cc: 'CN', region: 'Guangdong', city: 'Shenzhen', n: 2 },
    { cc: 'CN', region: 'Hunan', city: 'Yueyang', n: 1 },
    { cc: 'JP', region: 'Tokyo', city: '', n: 1 },
    { cc: 'CN', region: 'Guangdong', city: 'Guangzhou', n: 1 },
    { cc: 'CN', region: '', city: '', n: 3 },
    { cc: 'CN', region: 'Hunan', city: 'Changsha', n: 1 },
  ]

  it('sums each tier from the spots below it', () => {
    const tree = groupSpots(spots)
    expect(tree.map(c => [c.cc, c.n])).toEqual([['CN', 8], ['JP', 1]])
    expect(tree[0].regions.map(r => [r.region, r.n])).toEqual([['Guangdong', 3], ['Hunan', 2], ['', 3]])
    expect(tree[1]).toEqual({ cc: 'JP', n: 1, regions: [{ region: 'Tokyo', n: 1, cities: [{ city: '', n: 1 }] }] })
  })

  it('orders by count, then name, with unresolved names last', () => {
    const regions = groupSpots(spots).flatMap(c => c.regions)
    expect(regions.map(r => r.cities)).toEqual([
      [{ city: 'Shenzhen', n: 2 }, { city: 'Guangzhou', n: 1 }],
      // A tie on count falls back to the name, so the order is stable.
      [{ city: 'Changsha', n: 1 }, { city: 'Yueyang', n: 1 }],
      // The country-only bucket carries 3, as many as Guangdong, and is still
      // last: it is "resolved no further than the country", not a region
      // competing with the real ones.
      [{ city: '', n: 3 }],
      [{ city: '', n: 1 }],
    ])
  })

  it('returns nothing for no spots', () => {
    expect(groupSpots([])).toEqual([])
  })

  it('puts the smallest region code on the region, and none where no spot has one', () => {
    // One region's spots can disagree (a database bug, or a city the database
    // coded and one it did not). The code is display only, so the region is
    // still one node, carrying one deterministic code: the smallest, as the
    // server picks it.
    const tree = groupSpots([
      { cc: 'CN', region: 'Guangdong', rc: 'GX', city: 'Shenzhen', n: 2 },
      { cc: 'CN', region: 'Guangdong', city: 'Foshan', n: 1 },
      { cc: 'CN', region: 'Guangdong', rc: 'GD', city: 'Guangzhou', n: 1 },
      { cc: 'CN', region: 'Hunan', rc: '', city: 'Changsha', n: 1 },
      { cc: 'CN', region: 'Hubei', city: 'Wuhan', n: 1 },
    ])
    const regions = tree[0].regions
    expect(regions.map(r => r.region)).toEqual(['Guangdong', 'Hubei', 'Hunan'])
    expect(regions[0].rc).toBe('GD')
    // No code means no property at all, not rc: '' — the node keeps the
    // shape it had before codes existed.
    expect('rc' in regions[1]).toBe(false)
    expect('rc' in regions[2]).toBe(false)
  })
})

describe('spreadKm', () => {
  const ev = (v: number, maxKm?: number) => {
    const base = row({}).evidence
    return { ...base, v, spread: { ...base.spread, ...(maxKm === undefined ? {} : { max_km: maxKm }) } }
  }

  it('reads the distance from v3 evidence', () => {
    expect(spreadKm(ev(3, 1070))).toBe(1070)
  })

  it('reads nothing from a row that could not have recorded one', () => {
    // Before v3 an absent max_km means "not recorded", and a present one
    // would be a shape this version does not vouch for.
    expect(spreadKm(ev(2, 1070))).toBe(0)
    expect(spreadKm(ev(0, 1070))).toBe(0)
    expect(spreadKm(undefined)).toBe(0)
  })

  it('reads an absent, zero or malformed distance as none', () => {
    expect(spreadKm(ev(3))).toBe(0)
    expect(spreadKm(ev(3, 0))).toBe(0)
    expect(spreadKm(ev(3, -5))).toBe(0)
    expect(spreadKm(ev(3, Number.NaN))).toBe(0)
    expect(spreadKm(ev(3, Number.POSITIVE_INFINITY))).toBe(0)
  })
})

describe('activeDbIsCountryOnly', () => {
  const db = (granularity: string, active: boolean) =>
    ({ file: `${granularity}.mmdb`, type: '', granularity, build_epoch: 0, active })
  const status = (available: ReturnType<typeof db>[]) =>
    ({ enabled: true, dir: '', active: '', available, update: { updating: false } }) as GeoIPStatus

  it('judges only the ACTIVE database', () => {
    expect(activeDbIsCountryOnly(status([db('country', true)]))).toBe(true)
    // A country-only file lying next to an active city one changes nothing.
    expect(activeDbIsCountryOnly(status([db('country', false), db('city', true)]))).toBe(false)
    expect(activeDbIsCountryOnly(status([]))).toBe(false)
  })

  it('says nothing when the status is unknown', () => {
    // A failed or missing read is no evidence of a coarse database; claiming
    // one would tell the admin two tiers are dead when they may be fine.
    expect(activeDbIsCountryOnly(undefined)).toBe(false)
    expect(activeDbIsCountryOnly({} as GeoIPStatus)).toBe(false)
  })
})

// A stand-in for i18next's t over one shipped bundle: the admin namespace
// flattened the way the SPA registers it, `{{name}}` interpolation, and the
// defaultValue only when the key is absent. Backed by the REAL locale files,
// so these tests also prove every placeholder a string uses is one
// reasonText supplies.
function translator(bundle: Nested, drop: string[] = []): Translate {
  const dict = flatten(bundle)
  for (const k of drop) delete dict[k]
  return (key, opts = {}) => {
    const flat = key.startsWith('admin:') ? key.slice('admin:'.length) : key
    const raw = dict[flat] ?? (typeof opts.defaultValue === 'string' ? opts.defaultValue : key)
    return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (name in opts ? String(opts[name]) : m))
  }
}

describe('reasonText', () => {
  const zhT = translator(zh as Nested)
  const enT = translator(en as Nested)

  // DefaultGeoPolicy as the server snapshots it: scope city, tolerances
  // 1/1/2, flag after 3, clear after 6, placed ratio 0.5.
  const why = (code: GeoReasonCode, over: Partial<GeoWhy> = {}): GeoWhy => ({
    code, scope: 'city', tol: { countries: 1, regions: 1, cities: 2 },
    flag_after: 3, clear_after: 6, min_placed_ratio: 0.5, ...over,
  })
  // A v2 row: the English reason is deliberately NOT the one EvaluateGeo
  // writes, so a test can tell "localized" from "fell back" at a glance.
  const v2 = (w: GeoWhy, ev: Partial<GeoAnomaly['evidence']> = {}, over: Partial<GeoAnomaly> = {}) => {
    const base = row({})
    return row({
      reason: 'STORED ENGLISH',
      ...over,
      evidence: { ...base.evidence, v: 2, why: w, ...ev },
    })
  }
  const spread = (over: Partial<GeoAnomaly['evidence']['spread']>) =>
    ({ ...row({}).evidence.spread, ...over })

  // One row per sentence EvaluateGeo can write (the §4.2 golden table), and
  // the zh-CN each one must render. The params come from the evidence, the
  // row's streaks and the stored policy — never from global settings.
  const cases: { name: string; r: GeoAnomaly; zh: string }[] = [
    { name: 'disabled', r: v2(why('disabled', { scope: 'off' })), zh: '此账号的地区检测已关闭' },
    { name: 'exempt', r: v2(why('exempt')), zh: '此账号允许从任何地方连接' },
    { name: 'trusted', r: v2(why('trusted')), zh: '管理员已信任此账号，不做地区判定' },
    {
      name: 'idle_stale', r: v2(why('idle_stale'), { stale: 2 }),
      zh: '此刻没有并发连接；上游窗口里还有 2 个更早见过的地址',
    },
    { name: 'idle_none', r: v2(why('idle_none')), zh: '此刻没有连接' },
    {
      name: 'unknown_excluded',
      r: v2(why('unknown_excluded'), { excluded: { shared: 1, listed: 0, infra: 2, internal: 1 } }),
      zh: '全部 4 个并发地址都已排除（共享出口 1、忽略名单 0、本机节点 / 中转 2、内网 1），不下结论',
    },
    {
      name: 'unknown_geo_off',
      r: v2(why('unknown_geo_off'), { coverage: { placed: 0, unplaced: 2, region_known: 0, city_known: 0 } }),
      zh: '地区库不可用，不下结论',
    },
    {
      name: 'unknown_low_ratio',
      r: v2(why('unknown_low_ratio'), { coverage: { placed: 1, unplaced: 2, region_known: 1, city_known: 1 } }),
      zh: '3 个地址中只有 1 个能定位（至少需要 50%）',
    },
    {
      name: 'suspect, country tier',
      r: v2(why('suspect', { tier: 'country' }), { spread: spread({ countries: 2 }) }, { over_streak: 1 }),
      zh: '同时在 2 个国家（容错 1），连续 1 / 3 次',
    },
    {
      name: 'suspect, city tier',
      r: v2(why('suspect', { tier: 'city' }),
        { spread: spread({ countries: 1, regions: 1, region_country: 'JP', cities: 3, city_country: 'JP' }) },
        { over_streak: 1 }),
      zh: '同时在 JP 的 3 个城市（容错 2），连续 1 / 3 次',
    },
    {
      name: 'flagged_sustained, region tier',
      r: v2(why('flagged_sustained', { tier: 'region' }),
        { spread: spread({ countries: 1, regions: 2, region_country: 'CN', cities: 2, city_country: 'CN' }) },
        { over_streak: 3 }),
      zh: '同时在 CN 的 2 个省 / 州（容错 1），已持续 3 / 3 次',
    },
    {
      name: 'flagged_clearing, with the tier that raised it',
      r: v2(why('flagged_clearing', { tier: 'region' }), {}, { under_streak: 1 }),
      zh: '已连续 1 / 6 次在容错内，满 6 次后解除；标记原因：跨省',
    },
    {
      name: 'clean_unplaced',
      r: v2(why('clean_unplaced', { min_placed_ratio: 0 }), { coverage: { placed: 0, unplaced: 1, region_known: 0, city_known: 0 } }),
      zh: '有连接，但没有地址能被定位',
    },
    {
      name: 'clean_within',
      r: v2(why('clean_within'), { spread: spread({ countries: 1, regions: 1, cities: 2 }) }),
      zh: '在容错内：1 个国家；最多的国家内 1 个省、2 个城市；容错 1 / 1 / 2（判到城市）',
    },
  ]

  it.each(cases)('renders $name in the admin\'s language from the stored why', ({ r, zh: want }) => {
    expect(reasonText(r, zhT)).toBe(want)
  })

  it('covers every reason code the server writes', () => {
    // A code added server-side without a case here is a sentence nobody has
    // checked renders at all.
    expect(new Set(cases.map(c => c.r.evidence.why?.code))).toEqual(new Set(GEO_REASON_CODES))
  })

  it('fills every placeholder in English too', () => {
    // The strings are per language; a placeholder only the en-US text uses
    // would reach an English admin as a literal "{{name}}".
    for (const { name, r } of cases) {
      const got = reasonText(r, enT)
      expect(got, name).not.toMatch(/\{\{|\}\}/)
      expect(got, name).not.toBe(r.reason)
    }
  })

  it('prints the group\'s own tolerance, not the default', () => {
    // The policy is resolved per group. An account in a group that allows
    // three provinces must read "tolerance 3", which is why the SPA reads
    // the stored snapshot instead of the global settings.
    const r = v2(why('flagged_sustained', { tier: 'region', tol: { countries: 1, regions: 3, cities: 2 } }),
      { spread: spread({ countries: 1, regions: 4, region_country: 'CN' }) }, { over_streak: 3 })
    expect(reasonText(r, zhT)).toContain('容错 3')
  })

  it('leaves the tier clause out of a latch stored without a tier', () => {
    // An older build could latch a flag without recording its tier; the
    // server's sentence leaves the clause out then, and so must this one.
    const r = v2(why('flagged_clearing', { tier: '' }), {}, { under_streak: 1 })
    expect(reasonText(r, zhT)).toBe('已连续 1 / 6 次在容错内，满 6 次后解除')
    expect(reasonText(v2(why('flagged_clearing'), {}, { under_streak: 1 }), zhT))
      .toBe('已连续 1 / 6 次在容错内，满 6 次后解除')
  })

  describe('falls back to the stored English', () => {
    it('for a row written before why existed', () => {
      // v1 evidence has no why. A v1 row that somehow carries one is still
      // v1: the version, not the shape, says what the row is.
      const r = v2(why('idle_none'))
      expect(reasonText({ ...r, evidence: { ...r.evidence, v: 1 } }, zhT)).toBe('STORED ENGLISH')
      expect(reasonText({ ...r, evidence: { ...r.evidence, v: 0 } }, zhT)).toBe('STORED ENGLISH')
    })

    it('for a v2 row with no why', () => {
      const r = v2(why('idle_none'))
      expect(reasonText({ ...r, evidence: { ...r.evidence, why: undefined } }, zhT)).toBe('STORED ENGLISH')
    })

    it('for a code this build does not know', () => {
      // A newer server may add a branch before this SPA learns it.
      expect(reasonText(v2(why('teleported' as GeoReasonCode)), zhT)).toBe('STORED ENGLISH')
    })

    it('for an over verdict whose tier this build cannot place', () => {
      for (const tier of ['', 'planet'] as const) {
        const r = v2(why('suspect', { tier: tier as GeoWhy['tier'] }), {}, { over_streak: 1 })
        expect(reasonText(r, zhT)).toBe('STORED ENGLISH')
      }
    })

    it('for a missing string, whole — never half localized', () => {
      // The suffix and the tier clause are separate strings. Were only the
      // head missing, gluing a localized suffix onto the English sentence
      // would print its streak twice, once in each language.
      const over = v2(why('suspect', { tier: 'region' }),
        { spread: spread({ regions: 2, region_country: 'CN' }) }, { over_streak: 1 })
      expect(reasonText(over, translator(zh as Nested, ['geo_anomalies.reason_over_region']))).toBe('STORED ENGLISH')

      const clearing = v2(why('flagged_clearing', { tier: 'city' }), {}, { under_streak: 2 })
      expect(reasonText(clearing, translator(zh as Nested, ['geo_anomalies.reason_flagged_clearing']))).toBe('STORED ENGLISH')

      expect(reasonText(v2(why('disabled')), translator(zh as Nested, ['geo_anomalies.reason_disabled']))).toBe('STORED ENGLISH')
    })
  })

  // 「同时在 CN 的 个省 / 州（容错 ）」: a verdict whose evidence lacks a number
  // its sentence names — a field missing, or a whole block of the evidence —
  // reads that sentence without numbers, in either language: never a hole,
  // never a crash, and never the stored English beside localized neighbours.
  describe('reads the sentence without numbers when one is missing', () => {
    const zhDict = flatten(zh as Nested)
    const enDict = flatten(en as Nested)
    // The row with one part of its evidence (or of its why) taken away.
    const without = (r: GeoAnomaly, k: keyof GeoAnomaly['evidence']): GeoAnomaly => {
      const ev: Partial<GeoAnomaly['evidence']> = { ...r.evidence }
      delete ev[k]
      return { ...r, evidence: ev as GeoAnomaly['evidence'] }
    }
    const whyWithout = (w: GeoWhy, k: keyof GeoWhy): GeoWhy => {
      const out: Partial<GeoWhy> = { ...w }
      delete out[k]
      return out as GeoWhy
    }
    const overRegion = v2(why('suspect', { tier: 'region' }),
      { spread: spread({ regions: 2, region_country: 'CN' }) }, { over_streak: 1 })

    const cases: { name: string; r: GeoAnomaly; key: string; tail?: { zh: string; en: string } }[] = [
      { name: 'idle_stale without the stale count', r: without(v2(why('idle_stale')), 'stale'),
        key: 'reason_idle_stale_bare' },
      { name: 'unknown_excluded without its exclusions', r: without(v2(why('unknown_excluded')), 'excluded'),
        key: 'reason_unknown_excluded_bare' },
      { name: 'unknown_low_ratio without its coverage', r: without(v2(why('unknown_low_ratio')), 'coverage'),
        key: 'reason_unknown_low_ratio_bare' },
      { name: 'unknown_low_ratio without the ratio', r: v2(whyWithout(why('unknown_low_ratio'), 'min_placed_ratio')),
        key: 'reason_unknown_low_ratio_bare' },
      { name: 'a country-tier over without its tolerances',
        r: v2(whyWithout(why('suspect', { tier: 'country' }), 'tol'), { spread: spread({ countries: 2 }) }, { over_streak: 1 }),
        key: 'reason_over_country_bare', tail: { zh: '，连续 1 / 3 次', en: ', 1 of 3 checks so far' } },
      { name: 'a region-tier over without the country it counted in',
        r: v2(why('flagged_sustained', { tier: 'region' }), { spread: spread({ regions: 2, region_country: '' }) },
          { over_streak: 3 }),
        key: 'reason_over_region_bare', tail: { zh: '，已持续 3 / 3 次', en: ', sustained for 3 of 3 checks' } },
      { name: 'a city-tier over without its spread',
        r: without(v2(why('suspect', { tier: 'city' }), {}, { over_streak: 2 }), 'spread'),
        key: 'reason_over_city_bare', tail: { zh: '，连续 2 / 3 次', en: ', 2 of 3 checks so far' } },
      { name: 'a latch clearing without the checks it needs',
        r: v2(whyWithout(why('flagged_clearing', { tier: 'region' }), 'clear_after'), {}, { under_streak: 1 }),
        key: 'reason_flagged_clearing_bare', tail: { zh: '；标记原因：跨省', en: '; flagged as Cross-region' } },
      { name: 'clean_within without its tolerances', r: v2(whyWithout(why('clean_within'), 'tol')),
        key: 'reason_clean_within_bare' },
    ]

    it.each(cases)('$name', ({ r, key, tail }) => {
      for (const [lang, t, dict] of [['zh', zhT, zhDict], ['en', enT, enDict]] as const) {
        const bare = dict[`geo_anomalies.${key}`]
        expect(bare, `${lang}: ${key}`).toBeTruthy()
        expect(bare, `${lang}: ${key}`).not.toMatch(/\{\{|\}\}/)
        expect(reasonText(r, t), lang).toBe(bare + (tail?.[lang] ?? ''))
      }
    })

    it('keeps a whole head and leaves out a streak it cannot count', () => {
      const r = v2(whyWithout(why('suspect', { tier: 'region' }), 'flag_after'),
        { spread: spread({ regions: 2, region_country: 'CN' }) }, { over_streak: 1 })
      expect(reasonText(r, zhT)).toBe('同时在 CN 的 2 个省 / 州（容错 1）')
      expect(reasonText(overRegion, zhT)).toBe('同时在 CN 的 2 个省 / 州（容错 1），连续 1 / 3 次')
    })
  })
})

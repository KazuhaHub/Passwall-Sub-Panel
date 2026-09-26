import { describe, expect, it, vi } from 'vitest'

// RISK_CODES lives beside the wire types, in a module that imports the shared
// axios client, which reads the document at import time. These are pure
// function tests in a node environment; nothing here makes a request.
vi.mock('@/api/client', () => ({ client: {} }))

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { RISK_CODES, RISK_KINDS, type RiskKind, type RiskSignal, type RiskUserRow } from '@/api/riskSignals'
import type { UISettings } from '@/api/settings'
import { flatten, type Nested } from '@/i18n/options'
import type { Translate } from './geoAnomaly'
import {
  dayBits, dayLabels, formatGB, needsAttention, oldestUpdate, placeLabel, riskCodeText, riskPolicy, sortRiskRows,
} from './riskSignals'

// A stand-in for i18next's t over one shipped bundle: the admin namespace
// flattened the way the SPA registers it, `{{name}}` interpolation, and the
// defaultValue only when the key is absent. Backed by the REAL locale files,
// so these tests also prove every placeholder a string uses is one
// riskCodeText supplies.
function translator(bundle: Nested): Translate {
  const dict = flatten(bundle)
  return (key, opts = {}) => {
    const flat = key.startsWith('admin:') ? key.slice('admin:'.length) : key
    const raw = dict[flat] ?? (typeof opts.defaultValue === 'string' ? opts.defaultValue : key)
    return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (name in opts ? String(opts[name]) : m))
  }
}

const zhT = translator(zh as Nested)
const enT = translator(en as Nested)

function sig(kind: string, state: RiskSignal['state'], over: Partial<RiskSignal> = {}): RiskSignal {
  return { kind: kind as RiskKind, state, code: 'within', evidence: null, updated_at_ms: 1, ...over }
}

function user(id: number, signals: RiskSignal[], over: Partial<RiskUserRow> = {}): RiskUserRow {
  return { user_id: id, upn: `u${id}`, geo: null, signals, ...over }
}

describe('needsAttention', () => {
  it('holds a row with a flagged or suspect signal', () => {
    expect(needsAttention(user(1, [sig('devices', 'flagged')]))).toBe(true)
    expect(needsAttention(user(1, [sig('usage_shift', 'suspect')]))).toBe(true)
  })

  it('does not hold a row that only cannot tell, has no data, or is clean', () => {
    // "Cannot tell" is not a finding. On a fleet where no client sends
    // x-hwid, every row reads devices: unknown, and listing them all would
    // bury the one account that matters under the whole fleet.
    const quiet = ['unknown', 'idle', 'clean', 'exempt', 'disabled'] as const
    expect(needsAttention(user(1, quiet.map((s, i) => sig(RISK_KINDS[i % 4], s))))).toBe(false)
    expect(needsAttention(user(1, []))).toBe(false)
  })

  it('counts the concurrent-location latch, not only its state', () => {
    // A flagged account that disconnected reads state idle and is still
    // flagged — the easiest evasion there is, if the filter reads the state.
    expect(needsAttention(user(1, [], { geo: { state: 'idle', flagged: true, tier: 'region', updated_at_ms: 1 } }))).toBe(true)
    expect(needsAttention(user(1, [], { geo: { state: 'suspect', flagged: false, tier: 'city', updated_at_ms: 1 } }))).toBe(true)
    expect(needsAttention(user(1, [], { geo: { state: 'flagged', flagged: true, tier: 'country', updated_at_ms: 1 } }))).toBe(true)
    expect(needsAttention(user(1, [], { geo: { state: 'unknown', flagged: false, tier: '', updated_at_ms: 1 } }))).toBe(false)
  })

  it('ignores a kind this build does not show', () => {
    // A row listed for a flag nobody can see in it is a row nobody can act on.
    expect(needsAttention(user(1, [sig('travel', 'flagged')]))).toBe(false)
  })
})

describe('sortRiskRows', () => {
  it('orders by the most severe signal, a geo latch counting as a flag', () => {
    const rows = [
      user(1, [sig('sub_spread', 'clean')], { upn: 'clean' }),
      user(2, [sig('devices', 'suspect'), sig('usage_shift', 'clean')], { upn: 'suspect' }),
      user(3, [sig('sub_spread', 'idle')], { upn: 'idle' }),
      user(4, [sig('usage_shift', 'unknown')], { upn: 'unknown' }),
      user(5, [sig('login_country', 'clean')], {
        upn: 'latched', geo: { state: 'idle', flagged: true, tier: 'region', updated_at_ms: 1 },
      }),
      user(6, [sig('devices', 'clean'), sig('login_country', 'flagged')], { upn: 'flagged' }),
    ]
    expect(sortRiskRows(rows).map(r => r.upn)).toEqual(['flagged', 'latched', 'suspect', 'unknown', 'clean', 'idle'])
  })

  it('breaks ties by name, "#id" for an account without one', () => {
    const rows = [
      user(9, [sig('devices', 'flagged')], { upn: 'bob' }),
      user(7, [sig('devices', 'flagged')], { upn: undefined }),
      user(8, [sig('devices', 'flagged')], { upn: 'alice' }),
    ]
    expect(sortRiskRows(rows).map(r => r.user_id)).toEqual([7, 8, 9])
  })

  it('returns a new array and leaves the input alone', () => {
    // The input is the query cache's, shared with every reader.
    const rows = [user(1, [sig('devices', 'clean')]), user(2, [sig('devices', 'flagged')])]
    const sorted = sortRiskRows(rows)
    expect(sorted).not.toBe(rows)
    expect(rows.map(r => r.user_id)).toEqual([1, 2])
    expect(sorted.map(r => r.user_id)).toEqual([2, 1])
  })
})

// One evidence body per kind, carrying every field a code's sentence reads.
const evidence: Record<RiskKind, object> = {
  sub_spread: {
    v: 1, window_days: 2, window_start: '2026-09-18', retention_days: 2, min_days: 3, min_placed_pct: 50,
    tolerance: 1, country: 'CN', groups: 2, groups_all: 3, provinces: [], identities: [], foreign: [],
    excluded: { shared: 1, listed: 0, infra: 2, internal: 1 },
    coverage: { sources: 5, placed: 2, region_known: 1 },
  },
  devices: {
    v: 1, window_days: 2, window_start: '2026-09-18', retention_days: 2, min_days: 3, max_devices: 3,
    recurrent: 4, distinct: 5, devices: [], fetches_with_hwid: 9, fetches_without: 1, clients: [],
  },
  usage_shift: {
    v: 1, end_date: '2026-09-24', history_retention_days: 35, series: [], history_days: 9, median: 0,
    ratio: 3, floor: 3 * 2 ** 30, thresholds: [], over: [], over_days: 4, fleet_factors: [],
  },
  login_country: {
    v: 1, lookback_days: 90, hold_days: 7, warmup: 3, logins: 5, recent: 2, judged: 2,
    skipped: { infra: 0, internal: 0, listed: 0, node_country: 0, unplaced: 0 }, known: ['CN'],
    events: [{ cc: 'JP', at_ms: 3, method: 'local' }, { cc: 'US', at_ms: 2, method: 'oidc' }, { cc: 'JP', at_ms: 1, method: 'local' }],
  },
}

describe('riskCodeText', () => {
  const text = (kind: RiskKind, code: string, t = zhT) => riskCodeText(sig(kind, 'unknown', { code, evidence: evidence[kind] }), t)

  it('holds the same codes the shipped bundles carry', () => {
    // The Go side holds the bundles to domain.AllRiskCodes() in both
    // directions (TestRiskCodesHaveLocaleKeys); this holds this copy to the
    // bundles, so the copy cannot drift from the server either.
    const bundleKeys = Object.keys(flatten(zh as Nested)).filter(k => k.startsWith('risk_signals.code.')).sort()
    const copyKeys = RISK_KINDS.flatMap(k => RISK_CODES[k].map(c => `risk_signals.code.${k}.${c}`)).sort()
    expect(copyKeys).toEqual(bundleKeys)
    expect(Object.keys(RISK_CODES)).toEqual([...RISK_KINDS])
  })

  it.each(RISK_KINDS.flatMap(k => RISK_CODES[k].map(c => [k, c] as const)))(
    'fills every placeholder of %s.%s in both languages', (kind, code) => {
      for (const t of [zhT, enT]) {
        const got = text(kind, code, t)
        expect(got).not.toMatch(/\{\{|\}\}/)
        expect(got).not.toBe(code)
      }
    })

  it('reads each sentence\'s numbers from the stored evidence', () => {
    expect(text('sub_spread', 'retention_short')).toBe('订阅日志只保留 2 天，少于判断所需的 3 天')
    expect(text('sub_spread', 'all_excluded'))
      .toBe('全部 4 个拉取来源都已排除（共享出口 1、忽略名单 0、本机节点 / 中转 2、内网 1）')
    expect(text('sub_spread', 'low_placed')).toBe('5 个来源中只有 2 个能定位（至少需要 50%）')
    // A flag counts the groups holding a RECURRING province; the ramp and
    // the clean verdict count every group — the numbers each was judged on.
    expect(text('sub_spread', 'spread')).toBe('CN 有 2 组互不相连的常驻省份（容错 1）')
    expect(text('sub_spread', 'spread_building')).toBe('CN 有 3 组互不相连的省份，但还没有都出现满 3 天（容错 1）')
    expect(text('sub_spread', 'within')).toBe('3 组省份，在容错 1 内')
    expect(text('devices', 'retention_short')).toBe('订阅日志只保留 2 天，少于判断所需的 3 天')
    expect(text('devices', 'over')).toBe('4 台常用设备（上限 3）')
    expect(text('devices', 'over_building')).toBe('共 5 台设备，其中常用 4 台（上限 3）')
    expect(text('devices', 'within')).toBe('5 台设备，在上限 3 内')
    // Usage needs 36 days of hourly history, whatever the fetch window is:
    // the 35-day series plus the day the prune is partway through, so 35
    // itself is short.
    expect(text('usage_shift', 'retention_short')).toBe('流量历史只保留 35 天，少于判断所需的 36 天')
    expect(text('usage_shift', 'warmup')).toBe('用量历史只有 9 天，满 14 天后开始判断')
    expect(text('usage_shift', 'sustained')).toBe('最近 7 天有 4 天超过自身基线的 3 倍')
    expect(text('login_country', 'learning')).toBe('之前的登录不足 3 次，还在学习')
    // Each new country once, in the order the events list them.
    expect(text('login_country', 'new_country')).toBe('从新国家登录：JP, US')
  })

  it('renders a code without evidence, which carries no numbers', () => {
    expect(riskCodeText(sig('devices', 'disabled', { code: 'capture_off' }), zhT)).toBe('设备标识采集已关闭')
    expect(riskCodeText(sig('usage_shift', 'idle', { code: 'no_usage' }), enT)).toBe('No usage in the last 7 days')
  })

  it('falls back to the code itself for one this build does not know', () => {
    // A newer server may add a branch before this SPA learns it; the raw
    // code is still more than a blank tooltip.
    expect(riskCodeText(sig('devices', 'unknown', { code: 'teleported' }), zhT)).toBe('teleported')
  })
})

describe('dayBits', () => {
  it('reads bit i as window day i, oldest first', () => {
    expect(dayBits(0b1000001, 7)).toEqual([true, false, false, false, false, false, true])
    expect(dayBits(0b11, 1)).toEqual([true])
    expect(dayBits(0, 3)).toEqual([false, false, false])
  })
})

describe('dayLabels', () => {
  it('counts whole calendar days from the window start', () => {
    expect(dayLabels('2026-09-18', 3)).toEqual(['2026-09-18', '2026-09-19', '2026-09-20'])
    expect(dayLabels('2026-09-29', 3)).toEqual(['2026-09-29', '2026-09-30', '2026-10-01'])
  })

  it('labels nothing for a start it cannot read, one cell per day all the same', () => {
    expect(dayLabels('', 2)).toEqual(['', ''])
    expect(dayLabels('not-a-date', 1)).toEqual([''])
  })
})

describe('riskPolicy', () => {
  const s = (over: Partial<UISettings>) => over as UISettings

  it('reads unset (0, negative or missing) as the shipped defaults', () => {
    const def = { minDays: 3, maxDevices: 3, ratio: 3, floorGB: 3 }
    expect(riskPolicy(s({}))).toEqual(def)
    expect(riskPolicy(s({ risk_min_days: 0, risk_max_devices: 0, risk_usage_ratio: 0, risk_usage_floor_gb: 0 }))).toEqual(def)
    expect(riskPolicy(s({ risk_min_days: -2, risk_max_devices: -1, risk_usage_ratio: -3, risk_usage_floor_gb: -4 }))).toEqual(def)
  })

  it('repairs toward not accusing, as the server does', () => {
    // domain.RiskPolicyFromSettings: a ratio below 1.5 is raised to it, and
    // min_days is clamped to the seven-day window it counts within.
    expect(riskPolicy(s({ risk_usage_ratio: 1 })).ratio).toBe(1.5)
    expect(riskPolicy(s({ risk_min_days: 9 })).minDays).toBe(7)
    expect(riskPolicy(s({ risk_min_days: 1, risk_max_devices: 5, risk_usage_ratio: 2.5, risk_usage_floor_gb: 10 })))
      .toEqual({ minDays: 1, maxDevices: 5, ratio: 2.5, floorGB: 10 })
  })
})

describe('formatGB', () => {
  it('prints bytes as GiB with two decimals', () => {
    expect(formatGB(0)).toBe('0.00 GB')
    expect(formatGB(2.5 * 2 ** 30)).toBe('2.50 GB')
    expect(formatGB(1536 * 2 ** 20)).toBe('1.50 GB')
  })
})

describe('oldestUpdate', () => {
  it('is the oldest signal time, so one kind stuck for days shows', () => {
    const row = user(1, [
      sig('sub_spread', 'clean', { updated_at_ms: 300 }),
      sig('devices', 'clean', { updated_at_ms: 100 }),
      sig('usage_shift', 'clean', { updated_at_ms: 200 }),
    ])
    expect(oldestUpdate(row)).toBe(100)
  })

  it('is 0 for no signals, and ignores a kind this build does not show', () => {
    expect(oldestUpdate(user(1, []))).toBe(0)
    expect(oldestUpdate(user(1, [sig('travel', 'clean', { updated_at_ms: 5 }), sig('devices', 'clean', { updated_at_ms: 50 })]))).toBe(50)
  })
})

describe('placeLabel', () => {
  it('names the province, or the country when the database stopped there', () => {
    expect(placeLabel({ cc: 'CN', region: 'Guangdong' })).toBe('Guangdong')
    expect(placeLabel({ cc: 'CN', region: '' })).toBe('CN')
  })

  it('names the province through the namer it is given', () => {
    // The one hook: the tab passes regionNamer, which maps the ISO code to
    // the admin's language, and the country stays the fallback for a namer
    // with nothing to say.
    expect(placeLabel({ cc: 'CN', region: 'Guangdong', rc: 'GD' }, () => '广东')).toBe('广东')
    expect(placeLabel({ cc: 'CN', region: 'Guangdong', rc: 'GD' }, () => '')).toBe('CN')
  })
})

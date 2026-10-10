import { describe, expect, it, vi } from 'vitest'

// The wire modules import the shared axios client, which reads the document
// at import time. These are pure-function tests; nothing here makes a request.
vi.mock('@/api/client', () => ({ client: {} }))

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import { flatten, type Nested } from '@/i18n/options'
import type { GeoAnomaly, GeoEvidence, GeoSpot } from '@/api/geoAnomalies'
import type { QueueRow } from '@/api/riskCenter'
import type { RiskSignal } from '@/api/riskSignals'
import { formatMsDualTz } from '@/utils/datetime'
import { reasonText } from '@/utils/geoAnomaly'
import { regionNamer } from '@/utils/regionName'
import { listSeparator } from '@/utils/riskCenter'
import { riskCodeText } from '@/utils/riskSignals'
import { headlineText, headlineTip, placesText, sourceChipLabel } from './queueText'

// t over a real bundle, flattened as the SPA registers it: a key the helpers
// ask for but the bundle lacks comes back raw, and fails the assertion.
function translator(bundle: unknown) {
  const dict = flatten(bundle as Nested)
  return (k: string, o?: Record<string, unknown>) => {
    const flat = k.startsWith('admin:') ? k.slice('admin:'.length) : k
    const raw = dict[flat] ?? (typeof o?.defaultValue === 'string' ? o.defaultValue : k)
    return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
  }
}
const t = translator(zh)
const tEn = translator(en)
const ctx = { nameRegion: regionNamer(t, 'zh-CN'), panelTz: 'Asia/Shanghai', sep: listSeparator('zh-CN') }
const ctxEn = { nameRegion: regionNamer(tEn, 'en-US'), panelTz: 'Asia/Shanghai', sep: listSeparator('en-US') }

it('explains destination block attention from its stored lower bound', () => {
  const r = row({ level: 'flagged', sources: [{ source: 'dest_block', level: 'flagged' }], signals: [{
    kind: 'dest_block' as RiskSignal['kind'], state: 'flagged', code: 'over', updated_at_ms: 1,
    evidence: { v: 1, total: 37, threshold: 20, window_hours: 24 },
  }] })
  expect(headlineText(r, t, ctx)).toBe('最近 24 小时至少 37 次目的地阻断（阈值 20 次）')
  expect(headlineText(r, tEn, ctxEn)).toBe('At least 37 destination blocks in the last 24 hours (threshold 20)')
  expect(sourceChipLabel('dest_block', r, t)).toBe('访问拦截')
})

const NO_REVIEW = { dismissed: false, reopened: false, lapsed: false, trusted: false, escalated: [] }

function evidence(spots: GeoSpot[], over: Partial<GeoEvidence> = {}): GeoEvidence {
  return {
    v: 3, spots, excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
    coverage: { placed: spots.length, unplaced: 0, region_known: spots.length, city_known: spots.length },
    networks: spots.length,
    spread: { countries: 1, regions: 0, region_country: '', cities: 0, city_country: '' },
    why: { code: 'suspect', tier: 'region', scope: 'city', tol: { countries: 1, regions: 1, cities: 2 },
      flag_after: 3, clear_after: 6, min_placed_ratio: 0.5 },
    ...over,
  }
}

function geo(over: Partial<GeoAnomaly> = {}): GeoAnomaly {
  return {
    user_id: 7, upn: 'alice', state: 'suspect', reason: 'stored English', tier: 'region', flagged: false,
    places: ['CN'], live_ips: 5, concurrent_ips: 5, excluded_ips: 0, complete: true, over_streak: 1,
    under_streak: 0, ban_streak: 0, updated_at_ms: 1, evidence: evidence([]), ...over,
  }
}

// Four provinces of one country, Guangdong the most sources.
const FOUR_REGIONS: GeoSpot[] = [
  { cc: 'CN', region: 'Guangdong', rc: 'GD', city: 'Shenzhen', n: 2 },
  { cc: 'CN', region: 'Hunan', rc: 'HN', city: 'Changsha', n: 1 },
  { cc: 'CN', region: 'Zhejiang', rc: 'ZJ', city: 'Hangzhou', n: 1 },
  { cc: 'CN', region: 'Sichuan', rc: 'SC', city: 'Chengdu', n: 1 },
]

function row(over: Partial<QueueRow> = {}): QueueRow {
  return {
    user_id: 7, upn: 'alice', display_name: 'Alice', group_id: 1, group_name: 'Team A', level: 'suspect',
    auto_suspended: false, urgent: false, service_state: 'active', sources: [], geo: null, signals: [],
    changed_at_ms: 1, review: NO_REVIEW, ...over,
  }
}

const devicesFlagged: RiskSignal = {
  kind: 'devices', state: 'flagged', code: 'over', updated_at_ms: 1,
  evidence: { v: 1, window_days: 7, window_start: '2026-09-21', min_days: 3, max_devices: 3, recurrent: 5,
    distinct: 6, devices: [], fetches_with_hwid: 10, fetches_without: 0, clients: [] },
}

describe('placesText', () => {
  it('names the provinces of a region-tier verdict, at most three', () => {
    const g = geo({ evidence: evidence(FOUR_REGIONS, {
      spread: { countries: 1, regions: 4, region_country: 'CN', cities: 4, city_country: 'CN' } }) })
    expect(placesText(g, t, ctx)).toBe('广东、湖南、四川 等 1 处')
  })

  it('names three or fewer places without a tail', () => {
    const g = geo({ evidence: evidence(FOUR_REGIONS.slice(0, 2), {
      spread: { countries: 1, regions: 2, region_country: 'CN', cities: 2, city_country: 'CN' } }) })
    expect(placesText(g, t, ctx)).toBe('广东、湖南')
  })

  it('names cities for a city-tier verdict, the most sources first', () => {
    const g = geo({ tier: 'city', evidence: evidence([
      { cc: 'CN', region: 'Guangdong', rc: 'GD', city: 'Shenzhen', n: 1 },
      { cc: 'CN', region: 'Guangdong', rc: 'GD', city: 'Guangzhou', n: 3 },
      { cc: 'CN', region: 'Guangdong', rc: 'GD', city: '', n: 2 },
      { cc: 'US', region: 'California', city: 'San Jose', n: 1 },
    ], { spread: { countries: 2, regions: 1, region_country: 'CN', cities: 2, city_country: 'CN' } }) })
    // Only the country the tier counted; the unnamed bucket is no city.
    expect(placesText(g, t, ctx)).toBe('Guangzhou、Shenzhen')
  })

  it('names countries for a country-tier verdict', () => {
    const g = geo({ tier: 'country', evidence: evidence([
      { cc: 'CN', region: 'Guangdong', city: '', n: 2 },
      { cc: 'US', region: '', city: '', n: 1 },
    ]) })
    expect(placesText(g, tEn, ctxEn)).toBe('🇨🇳 CN, 🇺🇸 US')
  })

  // A row an older build wrote carries no spots: its countries are all
  // there is.
  it('falls back to the stored countries without evidence', () => {
    const g = geo({ tier: 'country', places: ['CN', 'US', 'JP', 'DE'], evidence: evidence([], { v: 0 }) })
    expect(placesText(g, tEn, ctxEn)).toBe('CN, US, JP and 1 more')
  })
})

describe('headlineText', () => {
  it('reads a geo verdict as its places and the distance, the reason in the tooltip', () => {
    const g = geo({ evidence: evidence(FOUR_REGIONS.slice(0, 2), {
      spread: { countries: 1, regions: 2, region_country: 'CN', cities: 2, city_country: 'CN', max_km: 640 } }) })
    const r = row({ sources: [{ source: 'geo', level: 'suspect' }], geo: g })
    expect(headlineText(r, t, ctx)).toBe('广东、湖南 · 相距约 640 公里')
    expect(headlineTip(r, t)).toBe(reasonText(g, t))
  })

  // Disconnecting freezes the streak: a latched account that went idle is
  // still flagged, and the headline says so before it says "no connection".
  it('reads a latched idle verdict as still flagged', () => {
    const g = geo({ state: 'idle', flagged: true, tier: 'region', evidence: evidence([], {
      why: { code: 'idle_none', scope: 'city', tol: { countries: 1, regions: 1, cities: 2 }, flag_after: 3,
        clear_after: 6, min_placed_ratio: 0.5 } }) })
    const r = row({ level: 'flagged', sources: [{ source: 'geo', level: 'flagged' }], geo: g })
    expect(headlineText(r, t, ctx)).toBe('仍在标记中 · 跨省 · 此刻没有连接')
    // The reason is already in the text.
    expect(headlineTip(r, t)).toBe('')
  })

  it('reads a risk kind as its code sentence', () => {
    const r = row({ level: 'flagged', sources: [{ source: 'devices', level: 'flagged' }], signals: [devicesFlagged] })
    expect(headlineText(r, t, ctx)).toBe(riskCodeText(devicesFlagged, t))
    expect(headlineText(r, t, ctx)).toBe('5 台常用设备（上限 3）')
    expect(headlineTip(r, t)).toBe('')
  })

  // The hold has its own chip; the headline names the first signal that is
  // not the hold, and the hold only when nothing else is there.
  it('skips the hold for the first other source', () => {
    const r = row({ level: 'flagged', auto_suspended: true, service_disabled_reason: 'geo_auto',
      service_disabled_at_ms: 1_790_000_000_000,
      sources: [{ source: 'geo_auto', level: 'suspended' }, { source: 'devices', level: 'flagged' }],
      signals: [devicesFlagged] })
    expect(headlineText(r, t, ctx)).toBe('5 台常用设备（上限 3）')
  })

  it('reads a hold alone as the time it began', () => {
    const r = row({ level: '', auto_suspended: true, service_state: 'manual_suspended',
      service_disabled_reason: 'geo_auto', service_disabled_at_ms: 1_790_000_000_000,
      sources: [{ source: 'geo_auto', level: 'suspended' }] })
    const time = formatMsDualTz(1_790_000_000_000, 'Asia/Shanghai')
    expect(headlineText(r, t, ctx)).toBe(`自 ${time} 起自动暂停，到期自动恢复`)
  })

  // A trusted account with nothing at attention, listed under 已信任 or 全部.
  it('says there is no signal when there is none', () => {
    const r = row({ level: '', sources: [], review: { ...NO_REVIEW, trusted: true } })
    expect(headlineText(r, t, ctx)).toBe('当前无信号')
  })
})

describe('sourceChipLabel', () => {
  it('adds the tier to the geo chip', () => {
    const r = row({ sources: [{ source: 'geo', level: 'suspect' }], geo: geo({ tier: 'city' }) })
    expect(sourceChipLabel('geo', r, t)).toBe('异地并发 · 跨城')
  })

  it('names a geo verdict without a tier, and a risk kind, by the source alone', () => {
    const r = row({ sources: [{ source: 'geo', level: 'suspect' }], geo: geo({ tier: '' }) })
    expect(sourceChipLabel('geo', r, t)).toBe('异地并发')
    expect(sourceChipLabel('sub_spread', r, t)).toBe('订阅多地')
  })
})

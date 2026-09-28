import { describe, expect, it, vi } from 'vitest'

// The wire modules import the shared axios client, which reads the document
// at import time. These are pure-function tests; nothing here makes a request.
vi.mock('@/api/client', () => ({ client: {} }))

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import type { GeoEvidence, GeoWhy } from '@/api/geoAnomalies'
import { EXCLUSION_REASONS, FLAG_EVENTS, FLAG_LEVELS, type FlagRecord } from '@/api/riskCenter'
import { flatten, type Nested } from '@/i18n/options'
import {
  FLAG_SOURCES, agoText, exclusionLabelKey, flagEventKey, flagLevelKey, flagSourceKey, flagText, judgementColor,
} from './riskCenter'

const zhDict = flatten(zh as Nested)
const enDict = flatten(en as Nested)

// t over the REAL zh-CN bundle: a key the helper asks for but the bundle
// lacks comes back as its defaultValue (or the raw key), never as a sentence.
function zhT(k: string, o?: Record<string, unknown>): string {
  const flat = k.startsWith('admin:') ? k.slice('admin:'.length) : k
  const raw = zhDict[flat] ?? (typeof o?.defaultValue === 'string' ? o.defaultValue : k)
  return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
}

const why = (over: Partial<GeoWhy>): GeoWhy => ({
  code: 'suspect', tier: 'region', scope: 'city', tol: { countries: 1, regions: 2, cities: 3 },
  flag_after: 6, clear_after: 6, min_placed_ratio: 0.5, ...over,
})

const evidence = (w: GeoWhy): GeoEvidence => ({
  v: 3, spots: [{ cc: 'CN', region: 'Guangdong', city: 'Shenzhen', n: 1 }],
  excluded: { shared: 0, listed: 0, infra: 0, internal: 0 }, stale: 0,
  coverage: { placed: 3, unplaced: 0, region_known: 3, city_known: 3 }, networks: 3,
  spread: { countries: 1, regions: 3, region_country: 'CN', cities: 3, city_country: 'CN' },
  why: w,
})

const rec = (over: Partial<FlagRecord>): FlagRecord => ({
  id: 1, user_id: 7, upn: 'alice', display_name: '', source: 'geo', event: 'enter_suspect',
  level: 'suspect', prev_level: '', state: 'suspect', code: 'suspect', params: null, at_ms: 1_790_000_000_000,
  ...over,
})

describe('flagText', () => {
  // Each record is read the way its source's own tab reads it, from the
  // numbers stored WITH the record — the verdict, the policy and the streak
  // of that moment — so the sentence says why it moved then, not now.
  it.each<[string, FlagRecord, string]>([
    ['a geo suspect record, with the over/need suffix from its params',
      rec({ params: { over: 2, under: 0, ban_over: 0, flagged: false, tier: 'region', evidence: evidence(why({})) } }),
      '同时在 CN 的 3 个省 / 州（容错 2），连续 2 / 6 次'],
    ['a latch clearing, with the under count from its params',
      rec({ event: 'enter_flagged', level: 'flagged', state: 'clean', code: 'flagged_clearing',
        params: { over: 0, under: 3, ban_over: 0, flagged: true, tier: 'region',
          evidence: evidence(why({ code: 'flagged_clearing', tier: 'region' })) } }),
      '已连续 3 / 6 次在容错内，满 6 次后解除；标记原因：跨省'],
    ['a risk record, through its kind\'s own sentence and the stored evidence',
      rec({ source: 'devices', event: 'enter_flagged', level: 'flagged', state: 'flagged', code: 'over',
        params: { v: 1, recurrent: 5, max_devices: 3, distinct: 6 } }),
      '5 台常用设备（上限 3）'],
    ['a risk record that left because it could not judge, with no evidence',
      rec({ source: 'login_country', event: 'leave_suspect', level: '', prev_level: 'suspect', state: 'unknown',
        code: 'geo_unavailable', params: null }),
      '地区库不可用，无法判断'],
    ['an automatic suspension, by its tier',
      rec({ source: 'geo_auto', event: 'auto_suspended', level: 'suspended', state: '', code: 'country',
        params: { tier: 'country', spread: 3, duration_minutes: 60 } }),
      '同时在 3 个国家或地区，暂停 60 分钟'],
    ['a suspension lifted on expiry',
      rec({ source: 'geo_auto', event: 'auto_lifted_expiry', level: '', prev_level: 'suspended', state: '',
        code: 'expired', params: { duration_minutes: 60, suspended_at_ms: 1 } }),
      '暂停 60 分钟到期'],
    ['a suspension staff lifted',
      rec({ source: 'geo_auto', event: 'auto_lifted_admin', level: '', prev_level: 'suspended', state: '',
        code: 'admin_resume', params: null }),
      '管理员或运维员恢复了服务'],
    ['a suspension replaced, naming the hold that replaced it',
      rec({ source: 'geo_auto', event: 'auto_replaced', level: '', prev_level: 'suspended', state: '',
        code: 'replaced', params: { replaced_by: 'traffic_exceeded' } }),
      '被「流量已用尽」暂停替换'],
    // A person's suspension from the location evidence, kept apart from
    // service_manual in the domain (resuming it counts a false positive), so
    // it is named as itself rather than as a generic staff suspension — and
    // by the hold's one name, the word the Users page and the drawer use.
    ['a suspension replaced by a manual location suspension, by its name',
      rec({ source: 'geo_auto', event: 'auto_replaced', level: '', prev_level: 'suspended', state: '',
        code: 'replaced', params: { replaced_by: 'geo_anomaly' } }),
      '被「异地人工暂停」暂停替换'],
    ['a replacement by a hold this build cannot name, as the raw reason',
      rec({ source: 'geo_auto', event: 'auto_replaced', level: '', prev_level: 'suspended', state: '',
        code: 'replaced', params: { replaced_by: 'future_hold' } }),
      '被「future_hold」暂停替换'],
    // A record whose numbers are missing, or whose source or code this build
    // does not know, still says something — the change it records, in the
    // admin's words. Never the code: 'suspect' or 'odd' in the 当时的依据
    // column reads as a broken page, not as a reason.
    ['a geo record without params, as its event', rec({ params: null }), '进入疑似'],
    ['a source this build does not know, as its event', rec({ source: 'future', code: 'odd' }), '进入疑似'],
    ['an automatic suspension with a code this build does not know, as its event',
      rec({ source: 'geo_auto', event: 'auto_suspended', level: 'suspended', state: '', code: 'planet',
        params: { tier: 'planet', spread: 2, duration_minutes: 60 } }),
      '自动临时暂停'],
    ['an automatic suspension whose numbers are missing, as its event',
      rec({ source: 'geo_auto', event: 'auto_suspended', level: 'suspended', state: '', code: 'country',
        params: null }),
      '自动临时暂停'],
    ['a risk record with a code this build does not know, as its event',
      rec({ source: 'devices', event: 'enter_flagged', level: 'flagged', state: 'flagged', code: 'future_code',
        params: { v: 1, recurrent: 5, max_devices: 3, distinct: 6 } }),
      '进入已标记'],
    // A judged verdict (flagged, suspect, clean) is always stored with its
    // evidence; without it the sentence would print its numbers as blanks.
    ['a judged risk record without its evidence, as its event',
      rec({ source: 'devices', event: 'enter_flagged', level: 'flagged', state: 'flagged', code: 'over',
        params: null }),
      '进入已标记'],
  ])('%s', (_name, r, want) => {
    expect(flagText(r, zhT, 'zh-CN')).toBe(want)
  })
})

// An admin's review action: who did it — by the admin's CURRENT name, read
// when listed (the record itself stores only the id: flag records are
// name-free) — and, for a dismissal, the levels the admin accepted.
describe('flagText of a review record', () => {
  const review = (over: Partial<FlagRecord>): FlagRecord => rec({
    source: 'review', event: 'dismissed', level: '', prev_level: '', state: '', code: 'dismissed',
    params: { by: 3, levels: { sub_spread: 'suspect', geo: 'flagged' } }, actor_upn: 'root', ...over,
  })

  it.each<[string, FlagRecord, string]>([
    // The levels in the queue's source order, whatever order the JSON had.
    ['a dismissal, with the levels it accepted', review({}), '由 root 忽略（当时：异地并发 已标记、订阅多地 疑似）'],
    ['a dismissal of a hold', review({ params: { by: 3, levels: { geo_auto: 'suspended' } } }),
      '由 root 忽略（当时：自动临时暂停 已暂停）'],
    ['a dismissal undone', review({ event: 'undismissed', code: 'undismissed', params: { by: 3 } }), '由 root 取消忽略'],
    ['a trust', review({ event: 'trusted', code: 'trusted', params: { by: 3 } }), '由 root 设为信任'],
    ['a trust withdrawn', review({ event: 'untrusted', code: 'untrusted', params: { by: 3 } }), '由 root 取消信任'],
    // The admin's account is gone: the id is all that is left to name.
    ['an admin no longer on file, by id', review({ event: 'trusted', code: 'trusted', params: { by: 3 }, actor_upn: undefined }),
      '由 #3 设为信任'],
    ['a review record without params, as its event', review({ params: null }), '忽略'],
  ])('%s', (_name, r, want) => {
    expect(flagText(r, zhT, 'zh-CN')).toBe(want)
  })

  it('joins the levels in the UI language', () => {
    expect(flagText(review({}), enT, 'en-US'))
      .toBe('Dismissed by root (at the time: Concurrent locations Flagged, Subscription spread Suspect)')
  })
})

// Every service hold that can replace geo_auto: domain.ServiceSuspensionReason
// minus geo_auto itself (user.SetServiceSuspendedAndSync records the others).
// Each is named in both shipped languages; a raw code in the sentence means
// a hold this build forgot, not one it cannot know.
const REPLACING_HOLDS = ['service_manual', 'blocked_client', 'traffic_exceeded', 'expired', 'geo_anomaly']

function enT(k: string, o?: Record<string, unknown>): string {
  const flat = k.startsWith('admin:') ? k.slice('admin:'.length) : k
  const raw = enDict[flat] ?? (typeof o?.defaultValue === 'string' ? o.defaultValue : k)
  return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
}

describe('a replaced automatic suspension', () => {
  it.each(REPLACING_HOLDS)('names %s in zh-CN and en-US, never as its code', hold => {
    const r = rec({ source: 'geo_auto', event: 'auto_replaced', level: '', prev_level: 'suspended', state: '',
      code: 'replaced', params: { replaced_by: hold } })
    for (const [lang, t] of [['zh-CN', zhT], ['en-US', enT]] as const) {
      const text = flagText(r, t)
      expect(text, lang).not.toContain(hold)
      expect(text, lang).not.toMatch(/admin:|risk_center\.|users\.status\./)
    }
  })
})

// The first reading after a restart waives only the "has the node rescanned"
// check (domain.FreshLiveIPsWithin with no previous reference); the live
// window still applies to each node's newest scan. The notice must not claim
// that everything the upstream remembered for 30 minutes is shown.
describe('the unreferenced-nodes notice', () => {
  it('claims only what a missing reference trusts', () => {
    const zhText = zhDict['risk_center.live.unreferenced']
    const enText = enDict['risk_center.live.unreferenced']
    expect(zhText).not.toMatch(/30 分钟见过的地址都/)
    expect(enText).not.toMatch(/last 30 minutes are all shown/)
    expect(zhText).toMatch(/停止扫描/)
    expect(enText).toMatch(/stopped/)
  })
})

describe('the record labels', () => {
  // Every value the server can send has a label in both shipped languages; a
  // missing one would render its raw key in the Flags table.
  it('names every event, level and source in zh-CN and en-US', () => {
    const keys = [
      ...FLAG_EVENTS.map(flagEventKey),
      ...FLAG_LEVELS.map(flagLevelKey),
      ...FLAG_SOURCES.map(flagSourceKey),
      ...EXCLUSION_REASONS.map(r => exclusionLabelKey(r) as string),
    ]
    expect(keys.length).toBe(12 + 4 + 7 + 4)
    for (const k of keys) {
      const flat = k.replace(/^admin:/, '')
      expect(zhDict[flat], `zh-CN ${flat}`).toBeTruthy()
      expect(enDict[flat], `en-US ${flat}`).toBeTruthy()
    }
  })

  // domain.FlagSources(): the admin's review actions are recorded too, and
  // come last — they are never attention.
  it('lists the sources the server records, geo first and review last', () => {
    expect(FLAG_SOURCES).toEqual(['geo', 'geo_auto', 'sub_spread', 'devices', 'usage_shift', 'login_country', 'review'])
  })

  it('lists the review events after the attention ones', () => {
    expect(FLAG_EVENTS.slice(-4)).toEqual(['dismissed', 'undismissed', 'trusted', 'untrusted'])
  })

  // The record's level is '' when it moved to no attention; the filter calls
  // that "cleared", and so must the label.
  it('labels a record that left attention as cleared', () => {
    expect(flagLevelKey('')).toBe('admin:risk_center.flags.level.cleared')
    expect(flagLevelKey('flagged')).toBe('admin:risk_center.flags.level.flagged')
  })
})

describe('exclusion labels and colours', () => {
  it('labels each exclusion reason and nothing for a judged source', () => {
    expect(exclusionLabelKey('')).toBeNull()
    expect(exclusionLabelKey('shared')).toBe('admin:risk_center.live.exclusion.shared')
    // A reason a newer server sent before this build learned it.
    expect(exclusionLabelKey('future')).toBeNull()
  })

  // A source the detector set aside was not judged at all. Drawn in the
  // success colour it would read as "checked and fine", the one thing it is
  // not; a judged source is not "fine" either, only counted.
  it('never colours a source as a success', () => {
    for (const x of ['', ...EXCLUSION_REASONS, 'future']) {
      expect(judgementColor(x)).not.toBe('success')
    }
    expect(judgementColor('')).toBe('primary')
    for (const x of EXCLUSION_REASONS) expect(judgementColor(x)).toBe('default')
  })
})

describe('agoText', () => {
  it('says how long ago in the coarsest whole unit', () => {
    expect(agoText(30, zhT)).toBe('刚刚')
    expect(agoText(125, zhT)).toBe('2 分钟前')
    expect(agoText(2 * 3600 + 5, zhT)).toBe('2 小时前')
    expect(agoText(3 * 86400, zhT)).toBe('3 天前')
  })
})

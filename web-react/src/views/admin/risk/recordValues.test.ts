import { describe, expect, it, vi } from 'vitest'

// The wire modules import the shared axios client, which reads the document
// at import time. These are pure-function tests; nothing here makes a request.
vi.mock('@/api/client', () => ({ client: {} }))

import zh from '@/locales/zh-CN/admin.json'
import en from '@/locales/en-US/admin.json'
import type { FlagRecord } from '@/api/riskCenter'
import { flatten, type Nested } from '@/i18n/options'
import { formatMsDualTz } from '@/utils/datetime'
import { RECORD_PARAM_KEYS, paramRows } from './recordValues'

const zhDict = flatten(zh as Nested)
const enDict = flatten(en as Nested)

// t over the REAL bundles: a key the helper asks for but a bundle lacks comes
// back as the raw key, which the assertions below would show.
function tOver(dict: Record<string, string>) {
  return (k: string, o?: Record<string, unknown>): string => {
    const flat = k.startsWith('admin:') ? k.slice('admin:'.length) : k
    const raw = dict[flat] ?? (typeof o?.defaultValue === 'string' ? o.defaultValue : k)
    return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (o && name in o ? String(o[name]) : m))
  }
}
const zhT = tOver(zhDict)
const enT = tOver(enDict)

// A panel timezone no CI browser runs in, so a time rendered in the
// browser's zone cannot pass for one rendered in the panel's.
const PANEL_TZ = 'Pacific/Chatham'

const rec = (over: Partial<FlagRecord>): FlagRecord => ({
  id: 1, user_id: 7, upn: 'alice', display_name: '', source: 'geo', event: 'enter_suspect',
  level: 'suspect', prev_level: '', state: 'suspect', code: 'suspect', params: null, at_ms: 1_790_000_000_000,
  ...over,
})

/** The rows as [label, value] pairs, the way an admin reads them. */
const rows = (r: FlagRecord, t = zhT, language = 'zh-CN') =>
  paramRows(r, t, PANEL_TZ, language).map(x => [x.label, x.value])

describe('paramRows', () => {
  // The streak counters are numbers, the tier is its label, a latch is 是/否;
  // the evidence object is not a row — the page draws it as places.
  it('lays out a geo record: counters, tier and latch', () => {
    const r = rec({ params: { over: 2, under: 0, ban_over: 1, flagged: true, tier: 'region', evidence: { v: 3 } } })
    expect(rows(r)).toEqual([
      ['连续超限次数', '2'], ['连续在容错内次数', '0'], ['连续超过暂停阈值次数', '1'], ['级别', '跨省'], ['标记锁定', '是'],
    ])
  })

  it('says 否 for a false boolean and leaves out a tier with no name', () => {
    const r = rec({ params: { over: 1, flagged: false, tier: '' } })
    expect(rows(r)).toEqual([['连续超限次数', '1'], ['标记锁定', '否']])
  })

  it('lays out an automatic suspension', () => {
    const r = rec({ source: 'geo_auto', event: 'auto_suspended', level: 'suspended', state: '', code: 'country',
      params: { tier: 'country', spread: 3, duration_minutes: 60 } })
    expect(rows(r)).toEqual([['级别', '跨国'], ['地点数', '3'], ['暂停时长（分钟）', '60']])
  })

  // Every other time in the admin reads in the panel's timezone; so does a
  // time stored inside a record.
  it('prints a stored instant in panel time', () => {
    const r = rec({ source: 'geo_auto', event: 'auto_lifted_expiry', level: '', prev_level: 'suspended', state: '',
      code: 'expired', params: { duration_minutes: 60, suspended_at_ms: 1_789_990_000_000 } })
    expect(rows(r)).toEqual([
      ['暂停时长（分钟）', '60'], ['暂停开始', formatMsDualTz(1_789_990_000_000, PANEL_TZ)],
    ])
  })

  // The hold that replaced an automatic suspension, by its one name; a hold
  // this build cannot name reads as the generic suspension, never its code.
  it.each([
    ['geo_anomaly', '异地人工暂停'],
    ['traffic_exceeded', '流量已用尽'],
    ['service_manual', '代理服务暂停'],
    ['future_hold', '代理服务暂停'],
  ])('names the replacing hold %s', (hold, want) => {
    const r = rec({ source: 'geo_auto', event: 'auto_replaced', level: '', prev_level: 'suspended', state: '',
      code: 'replaced', params: { replaced_by: hold } })
    expect(rows(r)).toEqual([['替换为', want]])
  })

  // An admin's action names the admin by the current name the server read
  // for it (#id once that account is gone), and a dismissal the levels it
  // accepted, in the queue's source order.
  it('lays out a review record: the admin and the accepted levels', () => {
    const r = rec({ source: 'review', event: 'dismissed', level: '', state: '', code: 'dismissed',
      params: { by: 3, levels: { sub_spread: 'suspect', geo: 'flagged' } }, actor_upn: 'root' })
    expect(rows(r)).toEqual([['操作人', 'root'], ['当时的级别', '异地并发 已标记、订阅多地 疑似']])
    expect(rows({ ...r, actor_upn: undefined })[0]).toEqual(['操作人', '#3'])
    expect(rows(r, enT, 'en-US')).toEqual([
      ['By', 'root'], ['Levels at the time', 'Concurrent locations Flagged, Subscription spread Suspect'],
    ])
  })

  // A key without a label is left out rather than shown raw: the evidence
  // object, a version number, a field a newer server added.
  it('leaves out every key it has no label for', () => {
    expect(rows(rec({ params: { v: 1, evidence: { spots: [] }, future_key: 5, note: 'x' } }))).toEqual([])
    expect(rows(rec({ params: null }))).toEqual([])
    expect(rows(rec({ params: [1, 2] }))).toEqual([])
  })

  it('names every labelled key in both bundles', () => {
    for (const key of RECORD_PARAM_KEYS) {
      const flat = `risk_center.flags.param.${key}`
      expect(zhDict[flat], `zh-CN ${flat}`).toBeTruthy()
      expect(enDict[flat], `en-US ${flat}`).toBeTruthy()
    }
  })
})

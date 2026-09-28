import { beforeAll, describe, expect, it } from 'vitest'
import i18next from 'i18next'

import { I18N_STATIC_OPTIONS } from '@/i18n/options'
import type { Translate } from './geoAnomaly'
import { sentence } from './sentence'

// A stand-in for i18next's t: `{{name}}` interpolation, a variable it was not
// given left in place (i18next's skipOnVariables), and the defaultValue only
// when the key is absent.
function translator(dict: Record<string, string>): Translate {
  return (key, opts = {}) => {
    const raw = dict[key] ?? (typeof opts.defaultValue === 'string' ? opts.defaultValue : key)
    return raw.replace(/\{\{(\w+)\}\}/g, (m, name: string) => (name in opts ? String(opts[name]) : m))
  }
}

const t = translator({
  full: '{{country}} 有 {{groups}} 组（容错 {{tolerance}}）',
  bare: '有多组',
  none: '此刻没有连接',
})
const bare = () => t('bare')

describe('sentence', () => {
  it('fills a sentence whose every value is there, zero included', () => {
    expect(sentence(t, 'full', { country: 'CN', groups: 0, tolerance: 1 }, bare)).toBe('CN 有 0 组（容错 1）')
  })

  it('ignores values the sentence does not name', () => {
    expect(sentence(t, 'none', { groups: undefined, country: '' }, bare)).toBe('此刻没有连接')
    expect(sentence(t, 'full', { country: 'CN', groups: 2, tolerance: 1, spare: undefined }, bare))
      .toBe('CN 有 2 组（容错 1）')
  })

  // 「有 组互不相连的常驻省份（容错 ）」: a verdict or record stored without
  // a number reads its sentence without numbers, never with holes.
  it.each<[string, Record<string, unknown>]>([
    ['absent', { country: 'CN', tolerance: 1 }],
    ['undefined', { country: 'CN', groups: undefined, tolerance: 1 }],
    ['null', { country: 'CN', groups: null, tolerance: 1 }],
    ['not a number', { country: 'CN', groups: Number.NaN, tolerance: 1 }],
    ['an empty string', { country: '', groups: 2, tolerance: 1 }],
    ['an object', { country: 'CN', groups: {}, tolerance: 1 }],
  ])('reads the bare sentence when a named value is %s', (_name, values) => {
    expect(sentence(t, 'full', values, bare)).toBe('有多组')
  })

  it('returns the caller\'s default for a missing string, as it is', () => {
    expect(sentence(t, 'absent', { groups: 2 }, bare, 'STORED')).toBe('STORED')
  })

  // The check leans on i18next leaving a variable it was not given in place.
  // Held here with the app's own options, so a config change that prints it
  // as nothing instead fails this test rather than the admin's page.
  describe('through i18next with the app\'s options', () => {
    const i18n = i18next.createInstance()
    beforeAll(async () => {
      await i18n.init({
        ...I18N_STATIC_OPTIONS,
        lng: 'zh-CN',
        resources: { 'zh-CN': { admin: { full: '{{country}} 有 {{groups}} 组（容错 {{tolerance}}）', bare: '有多组' } } },
      })
    })
    const real: Translate = (key, opts) => i18n.t(key, opts ?? {})

    it('fills and falls back the same way', () => {
      const realBare = () => real('admin:bare')
      expect(sentence(real, 'admin:full', { country: 'CN', groups: 2, tolerance: 1 }, realBare)).toBe('CN 有 2 组（容错 1）')
      expect(sentence(real, 'admin:full', { country: 'CN', groups: undefined, tolerance: 1 }, realBare)).toBe('有多组')
    })
  })
})

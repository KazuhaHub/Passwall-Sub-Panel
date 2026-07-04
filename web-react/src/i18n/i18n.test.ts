import { describe, it, expect, beforeAll } from 'vitest'
import i18next from 'i18next'

import { I18N_STATIC_OPTIONS, flatten } from './options'

describe('flatten', () => {
  it('flattens nested objects into dotted keys', () => {
    expect(flatten({ a: { b: { c: 'x' }, d: 'y' } })).toEqual({ 'a.b.c': 'x', 'a.d': 'y' })
  })

  it('keeps already-flat dotted keys intact', () => {
    expect(flatten({ 'servers.title': '服务器' })).toEqual({ 'servers.title': '服务器' })
  })
})

describe('language-pack missing-key fallback', () => {
  // Build a standalone i18next instance with the REAL shared options
  // (I18N_STATIC_OPTIONS) plus a built-in base bundle (zh-CN) and a PARTIAL
  // uploaded pack (xx-XX) — mirroring what the app registers via
  // addResourceBundle. This locks the core guarantee that an untranslated key
  // resolves to the built-in language, never a raw key.
  const i18n = i18next.createInstance()

  beforeAll(async () => {
    await i18n.init({
      ...I18N_STATIC_OPTIONS,
      supportedLngs: ['zh-CN', 'xx-XX'],
      lng: 'xx-XX',
      resources: {
        'zh-CN': { common: { greeting: '你好', 'only.in.base': '仅中文' } },
        // Partial pack: translates `greeting` but omits `only.in.base`.
        'xx-XX': { common: { greeting: 'Salut' } },
      },
    })
  })

  it('uses the pack value when the key is present', () => {
    expect(i18n.t('common:greeting')).toBe('Salut')
  })

  it('falls back to the built-in language for a key the pack omits', () => {
    expect(i18n.t('common:only.in.base')).toBe('仅中文')
  })

  it('surfaces the raw key only when it is missing everywhere', () => {
    expect(i18n.t('common:truly.absent')).toContain('truly.absent')
  })
})

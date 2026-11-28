import i18next, { type i18n as I18n } from 'i18next'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18N_STATIC_OPTIONS } from './options'
import { BASE_NAMESPACES, SHELL_NAMESPACES, createResources, type LocaleLoaders } from './resources'
import { NAMESPACES } from './options'

// THE LOADING POLICY: the login page needs four namespaces, a shell adds its own when it is
// entered, a language switch carries the shell's namespaces over, and only an uploaded pack
// (which may be partial) pulls in the zh-CN fallback. Driven with fake loaders so the
// assertions are about WHICH bundles are requested, not about the size of the real ones.

function fakeLoaders(requested: string[]): LocaleLoaders {
  const out: LocaleLoaders = {}
  for (const lang of ['zh-CN', 'en-US', 'zh-TW']) {
    for (const ns of NAMESPACES) {
      out[`/src/locales/${lang}/${ns}.json`] = async () => {
        requested.push(`${lang}/${ns}`)
        return { default: { [`${ns}_key`]: `${lang} ${ns}` } }
      }
    }
  }
  return out
}

let requested: string[]
let instance: I18n
let fetchPack: ReturnType<typeof makePackFetcher>
const makePackFetcher = () => vi.fn(async (code: string): Promise<{ namespaces: Record<string, Record<string, unknown>> }> => ({ namespaces: { common: { pack_key: `${code} common` } } }))
const manifestReady = vi.fn(async () => {})

async function boot(lang: string, opts: { builtin?: (l: string) => boolean } = {}) {
  requested = []
  fetchPack = makePackFetcher()
  instance = i18next.createInstance()
  const res = createResources({
    loaders: fakeLoaders(requested),
    fetchPack,
    isBuiltin: opts.builtin ?? (l => ['zh-CN', 'zh-TW', 'en-US'].includes(l)),
    manifestReady,
    instance,
  })
  const resources = await res.initial(lang)
  await instance.init({ ...I18N_STATIC_OPTIONS, resources, lng: lang, supportedLngs: ['zh-CN', 'en-US', 'zh-TW', 'xx-XX'] })
  return res
}

beforeEach(() => manifestReady.mockClear())

describe('the namespace split', () => {
  it('covers exactly the closed namespace set', () => {
    expect([...BASE_NAMESPACES, ...SHELL_NAMESPACES].sort()).toEqual([...NAMESPACES].sort())
  })
})

describe('first paint (login and the other pages outside the shells)', () => {
  it('requests no admin and no user bundle', async () => {
    await boot('zh-CN')
    expect(requested.sort()).toEqual(BASE_NAMESPACES.map(ns => `zh-CN/${ns}`).sort())
    expect(requested.some(r => r.endsWith('/admin') || r.endsWith('/user'))).toBe(false)
  })

  it('does not load zh-CN as the fallback for en-US', async () => {
    await boot('en-US')
    expect(requested.every(r => r.startsWith('en-US/'))).toBe(true)
    expect(instance.hasResourceBundle('zh-CN', 'common')).toBe(false)
  })

  it('does not load zh-CN as the fallback for the generated zh-TW', async () => {
    await boot('zh-TW')
    expect(requested.every(r => r.startsWith('zh-TW/'))).toBe(true)
  })

  it('still loads the zh-CN fallback for an uploaded pack, for the namespaces in use', async () => {
    await boot('xx-XX')
    expect(fetchPack).toHaveBeenCalledTimes(1)
    expect(requested.sort()).toEqual(BASE_NAMESPACES.map(ns => `zh-CN/${ns}`).sort())
    // A key the pack omits resolves to Chinese, not to its raw name.
    expect(instance.t('common:common_key')).toBe('zh-CN common')
    expect(instance.t('common:pack_key')).toBe('xx-XX common')
  })
})

describe('entering a shell', () => {
  it('loads admin once, in the active language only, however many times it is asked', async () => {
    const res = await boot('en-US')
    await res.ensureNamespaces(['admin'])
    await res.ensureNamespaces(['admin'])
    expect(requested.filter(r => r.endsWith('/admin'))).toEqual(['en-US/admin'])
    expect(instance.hasResourceBundle('en-US', 'admin')).toBe(true)
    expect(requested.some(r => r.startsWith('zh-CN/'))).toBe(false)
  })

  it('leaves user unloaded when only the admin shell is entered', async () => {
    const res = await boot('zh-CN')
    await res.ensureNamespaces(['admin'])
    expect(instance.hasResourceBundle('zh-CN', 'user')).toBe(false)
  })

  it('loads the zh-CN fallback of that namespace for an uploaded pack', async () => {
    const res = await boot('xx-XX')
    await res.ensureNamespaces(['admin'])
    expect(requested).toContain('zh-CN/admin')
    expect(instance.hasResourceBundle('xx-XX', 'admin')).toBe(true)
  })
})

describe('switching language', () => {
  it('has the new language\'s admin bundle registered before changeLanguage runs', async () => {
    const res = await boot('zh-CN')
    await res.ensureNamespaces(['admin'])
    const seen: boolean[] = []
    const change = instance.changeLanguage.bind(instance)
    instance.changeLanguage = (async (lng: string) => { seen.push(instance.hasResourceBundle('en-US', 'admin')); return change(lng) }) as typeof instance.changeLanguage
    await res.setLanguage('en-US')
    expect(seen).toEqual([true])
    expect(instance.language).toBe('en-US')
    expect(instance.t('admin:admin_key')).toBe('en-US admin')
  })

  it('does not load admin when switching from a page outside the shells', async () => {
    const res = await boot('zh-CN')
    await res.setLanguage('en-US')
    expect(requested.some(r => r === 'en-US/admin')).toBe(false)
    expect(instance.hasResourceBundle('en-US', 'common')).toBe(true)
  })

  it('switches back without fetching again', async () => {
    const res = await boot('zh-CN')
    await res.setLanguage('en-US')
    const n = requested.length
    await res.setLanguage('zh-CN')
    await res.setLanguage('en-US')
    expect(requested.length).toBe(n)
  })

  it('waits for the manifest before switching to an uploaded pack, and not for a built-in', async () => {
    const res = await boot('zh-CN')
    await res.setLanguage('en-US')
    expect(manifestReady).not.toHaveBeenCalled()
    await res.setLanguage('xx-XX')
    expect(manifestReady).toHaveBeenCalledTimes(1)
    expect(instance.language).toBe('xx-XX')
  })

  it('leaves the language alone when a bundle fails to load', async () => {
    const res = await boot('zh-CN')
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    fetchPack.mockRejectedValueOnce(new Error('offline'))
    await res.setLanguage('xx-XX')
    expect(instance.language).toBe('zh-CN')
  })
})

// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'

// THE REAL BOOT, with the real locale files behind Vite's glob: what is registered after
// i18nReady, and what a shell entry and a language switch add. (./resources.test.ts covers
// the policy with fakes; this proves the wiring in index.ts follows it.)

const api = vi.hoisted(() => ({
  fetchLanguages: vi.fn(async () => [] as { code: string; name: string }[]),
  fetchLanguageBundle: vi.fn(async (_code: string) => ({ namespaces: { common: { 'actions.save': 'Speichern' } } })),
}))
vi.mock('@/api/i18n', () => api)

async function freshBoot(lang: string) {
  vi.resetModules()
  localStorage.setItem('psp-lang', lang)
  const mod = await import('./index')
  await mod.i18nReady
  return mod
}

beforeEach(() => {
  localStorage.clear()
  api.fetchLanguages.mockClear().mockResolvedValue([])
  api.fetchLanguageBundle.mockClear()
})

describe('boot', () => {
  it('registers only the pre-login namespaces, and no zh-CN fallback, for en-US', async () => {
    const { default: i18n } = await freshBoot('en-US')
    for (const ns of ['common', 'appearance', 'language', 'auth', 'nav']) expect(i18n.hasResourceBundle('en-US', ns), ns).toBe(true)
    expect(i18n.hasResourceBundle('en-US', 'admin')).toBe(false)
    expect(i18n.hasResourceBundle('en-US', 'user')).toBe(false)
    expect(i18n.hasResourceBundle('zh-CN', 'common')).toBe(false)
  })

  it('does not wait for the language manifest when the stored language is built in', async () => {
    let release!: () => void
    api.fetchLanguages.mockImplementation(() => new Promise(res => { release = () => res([]) }))
    vi.resetModules()
    localStorage.setItem('psp-lang', 'zh-CN')
    const mod = await import('./index')
    // i18nReady settles while the manifest request is still pending.
    await mod.i18nReady
    expect(api.fetchLanguages).toHaveBeenCalledTimes(1)
    release()
  })

  it('waits for the manifest when the stored language is an uploaded pack', async () => {
    api.fetchLanguages.mockResolvedValue([{ code: 'de-DE', name: 'Deutsch' }])
    const { default: i18n, SUPPORTED_LANGUAGES } = await freshBoot('de-DE')
    expect(SUPPORTED_LANGUAGES).toContain('de-DE')
    expect(i18n.resolvedLanguage).toBe('de-DE')
    expect(api.fetchLanguageBundle).toHaveBeenCalledWith('de-DE')
    // The pack is partial: the zh-CN fallback is registered for the namespaces in use.
    expect(i18n.hasResourceBundle('zh-CN', 'common')).toBe(true)
    expect(i18n.t('common:actions.save')).toBe('Speichern')
    expect(i18n.t('common:actions.cancel')).toBe('取消')
  })
})

describe('shells and language switching', () => {
  it('loads admin on entering the admin shell and keeps it across a language switch', async () => {
    const { default: i18n, ensureNamespaces, setLanguage } = await freshBoot('zh-CN')
    await ensureNamespaces(['admin'])
    expect(i18n.hasResourceBundle('zh-CN', 'admin')).toBe(true)
    expect(i18n.hasResourceBundle('zh-CN', 'user')).toBe(false)
    await setLanguage('en-US')
    expect(i18n.resolvedLanguage).toBe('en-US')
    expect(i18n.hasResourceBundle('en-US', 'admin')).toBe(true)
    expect(i18n.t('admin:servers.title')).not.toBe('servers.title')
    expect(i18n.hasResourceBundle('en-US', 'user')).toBe(false)
  })
})

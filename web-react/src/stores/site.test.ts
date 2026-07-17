// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ getAuthMethods: vi.fn() }))
vi.mock('@/api/auth', () => ({ getAuthMethods: mocks.getAuthMethods }))
vi.mock('@/panelPath', () => ({ panelURL: (route = '/') => `/panel${route}` }))

import { selectIcon, selectLogoDark, selectLogoLight, useSiteStore } from './site'

const initial = {
  siteTitle: 'Kazuha Hub Passwall',
  appTitle: 'Passwall',
  logoUrl: '',
  logoUrlDark: '',
  iconUrl: '',
  footerText: '漏 Kazuha Hub Passwall',
  themeColor: undefined,
  themeDefaultMode: undefined,
  timezone: '',
  loaded: false,
} as const

beforeEach(() => {
  mocks.getAuthMethods.mockReset()
  useSiteStore.setState(initial)
  document.title = ''
  document.head.querySelectorAll("link[rel~='icon']").forEach(node => node.remove())
})

describe('site store', () => {
  it('loads public branding once and applies it to the document', async () => {
    mocks.getAuthMethods.mockResolvedValue({
      site_title: 'My Panel',
      app_title: 'My App',
      logo_url: '/light.png',
      logo_url_dark: '/dark.png',
      icon_url: '/icon.png',
      footer_text: 'Footer',
      theme_color: '#123456',
      theme_default_mode: 'dark',
      timezone: 'Asia/Taipei',
    })

    await useSiteStore.getState().load()
    await useSiteStore.getState().load()
    expect(mocks.getAuthMethods).toHaveBeenCalledTimes(1)
    expect(useSiteStore.getState()).toMatchObject({
      siteTitle: 'My Panel', appTitle: 'My App', loaded: true, timezone: 'Asia/Taipei',
    })
    expect(document.title).toBe('My Panel')
    expect(document.querySelector<HTMLLinkElement>("link[rel~='icon']")?.href).toContain('/icon.png')
  })

  it('marks loading complete and retains defaults after an API failure', async () => {
    mocks.getAuthMethods.mockRejectedValue(new Error('offline'))
    await useSiteStore.getState().load()

    expect(useSiteStore.getState()).toMatchObject({ loaded: true, siteTitle: 'Kazuha Hub Passwall' })
    expect(document.title).toBe('Kazuha Hub Passwall')
    expect(document.querySelector<HTMLLinkElement>("link[rel~='icon']")?.href)
      .toContain('/panel/images/HeadPicture.png')
  })

  it('updates branding immediately and resolves logo fallbacks', () => {
    useSiteStore.getState().update({ siteTitle: '', appTitle: 'Fallback App', logoUrl: '/same.png' })
    const state = useSiteStore.getState()
    expect(document.title).toBe('Fallback App')
    expect(selectLogoLight(state)).toBe('/same.png')
    expect(selectLogoDark(state)).toBe('/same.png')
    expect(selectIcon(state)).toBe('/panel/images/HeadPicture.png')

    useSiteStore.setState({ logoUrl: '', logoUrlDark: '', iconUrl: '' })
    expect(selectLogoLight(useSiteStore.getState())).toBe('/panel/images/logo-title-circle.png')
    expect(selectLogoDark(useSiteStore.getState())).toBe('/panel/images/logo-title-circle-darkmode.png')
  })
})

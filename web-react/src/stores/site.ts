import { create } from 'zustand'
import { getAuthMethods } from '@/api/auth'

const DEFAULT_LOGO_LIGHT = '/images/logo+title-circle.png'
const DEFAULT_LOGO_DARK = '/images/logo+title-circle-darkmode.png'
const DEFAULT_ICON = '/images/HeadPicture.png'

interface SiteState {
  siteTitle: string
  appTitle: string
  logoUrl: string
  logoUrlDark: string
  iconUrl: string
  footerText: string
  themeColor: string | undefined
  themeDefaultMode: 'light' | 'dark' | undefined
  // panel timezone (IANA name). Empty = backend hasn't been configured, so
  // the frontend falls back to the browser tz for any display.
  timezone: string
  loaded: boolean
  load: () => Promise<void>
  update: (patch: Partial<Pick<SiteState,
    'siteTitle' | 'appTitle' | 'logoUrl' | 'logoUrlDark' | 'iconUrl' | 'footerText' | 'themeColor' | 'themeDefaultMode' | 'timezone'
  >>) => void
}

function applyDocumentBranding(siteTitle: string, appTitle: string, iconUrl: string) {
  document.title = siteTitle || appTitle || 'Kazuha Hub Passwall'
  let link = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }
  link.href = iconUrl || DEFAULT_ICON
}

export const useSiteStore = create<SiteState>((set, get) => ({
  siteTitle: 'Kazuha Hub Passwall',
  appTitle: 'Passwall',
  logoUrl: '',
  logoUrlDark: '',
  iconUrl: '',
  footerText: '© Kazuha Hub Passwall',
  themeColor: undefined,
  themeDefaultMode: undefined,
  timezone: '',
  loaded: false,

  async load() {
    if (get().loaded) return
    try {
      const m = await getAuthMethods()
      set({
        siteTitle: m.site_title || 'Kazuha Hub Passwall',
        appTitle: m.app_title || 'Passwall',
        logoUrl: m.logo_url || '',
        logoUrlDark: m.logo_url_dark || '',
        iconUrl: m.icon_url || '',
        footerText: m.footer_text || '© Kazuha Hub Passwall',
        themeColor: m.theme_color,
        themeDefaultMode: m.theme_default_mode,
        timezone: m.timezone || '',
        loaded: true,
      })
    } catch {
      set({ loaded: true })
    }
    const s = get()
    applyDocumentBranding(s.siteTitle, s.appTitle, s.iconUrl)
  },

  update(patch) {
    set(patch)
    const s = get()
    applyDocumentBranding(s.siteTitle, s.appTitle, s.iconUrl)
  },
}))

export const selectLogoLight = (s: SiteState) => s.logoUrl || DEFAULT_LOGO_LIGHT
export const selectLogoDark = (s: SiteState) => s.logoUrlDark || s.logoUrl || DEFAULT_LOGO_DARK
export const selectIcon = (s: SiteState) => s.iconUrl || DEFAULT_ICON

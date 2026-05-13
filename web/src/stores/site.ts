import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getAuthMethods } from '@/api/auth'

const DEFAULT_LOGO_LIGHT = '/images/logo+title-circle.png'
const DEFAULT_LOGO_DARK = '/images/logo+title-circle-darkmode.png'
const DEFAULT_ICON = '/images/HeadPicture.png'

/**
 * Shared store for site-wide branding. Loaded once on layout mount
 * so the sidebar / header can display the configurable site title.
 * Uses the public /auth/methods endpoint (no auth required) instead
 * of the admin-only settings API.
 */
export const useSiteStore = defineStore('site', () => {
  const siteTitle = ref('Passwall')
  const appTitle = ref('Passwall')
  const logoUrl = ref('')
  const logoUrlDark = ref('')
  const iconUrl = ref('')
  const loaded = ref(false)

  const logoLight = computed(() => logoUrl.value || DEFAULT_LOGO_LIGHT)
  const logoDark = computed(() => logoUrlDark.value || logoUrl.value || DEFAULT_LOGO_DARK)
  const icon = computed(() => iconUrl.value || DEFAULT_ICON)

  function applyDocumentBranding() {
    document.title = siteTitle.value || appTitle.value || 'Passwall'
    let link = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
    if (!link) {
      link = document.createElement('link')
      link.rel = 'icon'
      document.head.appendChild(link)
    }
    link.href = icon.value
  }

  async function load() {
    if (loaded.value) return
    try {
      const m = await getAuthMethods()
      siteTitle.value = m.site_title || 'Passwall'
      appTitle.value = m.app_title || 'Passwall'
      logoUrl.value = m.logo_url || ''
      logoUrlDark.value = m.logo_url_dark || ''
      iconUrl.value = m.icon_url || ''
    } catch {
      // keep defaults
    }
    applyDocumentBranding()
    loaded.value = true
  }

  /** Called after admin saves settings so the UI updates immediately. */
  function update(site: string, app: string, iconValue: string, logo: string, logoDk: string) {
    siteTitle.value = site || 'Passwall'
    appTitle.value = app || 'Passwall'
    iconUrl.value = iconValue || ''
    logoUrl.value = logo || ''
    logoUrlDark.value = logoDk || ''
    applyDocumentBranding()
  }

  return {
    siteTitle,
    appTitle,
    logoUrl,
    logoUrlDark,
    iconUrl,
    logoLight,
    logoDark,
    icon,
    loaded,
    load,
    update,
  }
})

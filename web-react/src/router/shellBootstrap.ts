import { useEffect } from 'react'

import type { Brand } from '@/components/shell/BrandMark'
import { selectIcon, selectLogoDark, selectLogoLight, useSiteStore } from '@/stores/site'
import { useAppearanceStore } from '@/stores/appearance'
import { DEFAULT_PRESET_HEX } from '@/theme'

// WHAT BOTH SIGNED-IN SHELLS NEED FROM THE SITE SETTINGS, kept out of the shell components
// themselves (they take their brand as data and read no store).

/**
 * Loads the site branding once, and re-syncs the system theme colour from it when it
 * arrives. An empty `theme_color` falls back to the compiled default.
 */
export function useSiteBootstrap(): void {
  const loaded = useSiteStore(s => s.loaded)
  const themeColor = useSiteStore(s => s.themeColor)
  const load = useSiteStore(s => s.load)

  // Depend on the action reference only: it is stable, and `[site]` (the whole store
  // object) is a new value on every update and would re-fire the load on every render.
  useEffect(() => { void load() }, [load])

  // The setter comes from getState() rather than a subscription: putting the whole
  // appearance store in the dependency list loops (set -> new store -> effect -> set).
  useEffect(() => {
    if (loaded) useAppearanceStore.getState().setSystemColor(themeColor || DEFAULT_PRESET_HEX)
  }, [loaded, themeColor])
}

/** The brand the shell draws: the app title (the site title when there is none) and the site's logos. */
export function useBrand(): Brand {
  const appTitle = useSiteStore(s => s.appTitle)
  const siteTitle = useSiteStore(s => s.siteTitle)
  const logoLight = useSiteStore(selectLogoLight)
  const logoDark = useSiteStore(selectLogoDark)
  const icon = useSiteStore(selectIcon)
  return { name: appTitle || siteTitle, logoLight, logoDark, icon }
}

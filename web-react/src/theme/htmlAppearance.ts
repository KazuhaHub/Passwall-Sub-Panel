import { useLayoutEffect, useState, useEffect } from 'react'
import { COLOR_PRESETS } from './presets'
import { brandTones } from './brandColor'
import { resolveEffectiveMode, selectEffectiveColor, useAppearanceStore, type Density } from '@/stores/appearance'

export interface HtmlAppearance {
  // The concrete mode, after 'auto' has been resolved against the OS preference
  // (useAppearanceSync does that).
  mode: 'light' | 'dark'
  density: Density
  // Effective source colour: the user's choice, else the site default.
  color: string
}

// brandPresetFor returns the preset id a hex belongs to, or null for a custom
// colour. The store accepts any #RRGGBB (colour picker, or whatever the site
// admin configured), so presets are the common case, not the only one.
export function brandPresetFor(hex: string): string | null {
  const upper = hex.toUpperCase()
  return COLOR_PRESETS.find(p => p.hex.toUpperCase() === upper)?.id ?? null
}

const CUSTOM_BRAND_VARS = ['--brand', '--sidebar-primary', '--brand-foreground', '--sidebar-primary-foreground']

// applyHtmlAppearance mirrors the appearance store onto <html>, which is where
// everything reads it: the `dark` class, data-density and data-brand. One-way:
// this never reads back into the store.
export function applyHtmlAppearance(root: HTMLElement, a: HtmlAppearance): void {
  root.classList.toggle('dark', a.mode === 'dark')
  // Native widgets (scrollbars, date/select pickers, autofill) follow the mode.
  root.style.colorScheme = a.mode
  root.dataset.density = a.density

  const preset = brandPresetFor(a.color)
  if (preset) {
    // Presets are defined in the stylesheet (light and dark), so no inline
    // value: an inline --brand would beat the stylesheet and pin one mode.
    root.dataset.brand = preset
    for (const v of CUSTOM_BRAND_VARS) root.style.removeProperty(v)
    return
  }
  // A custom colour has no stylesheet block: derive the fill and its label for this
  // mode (see brandColor.ts) and set them inline.
  root.dataset.brand = 'custom'
  // The foregrounds matter as much as the fills: without them the :root
  // defaults (light text) would sit on a possibly light dark-mode brand colour.
  const t = brandTones(a.color, a.mode)
  root.style.setProperty('--brand', t.primary)
  root.style.setProperty('--sidebar-primary', t.primary)
  root.style.setProperty('--brand-foreground', t.onPrimary)
  root.style.setProperty('--sidebar-primary-foreground', t.onPrimary)
}

function systemPrefersDarkNow(): boolean {
  if (typeof window === 'undefined' || !window.matchMedia) return false
  return window.matchMedia('(prefers-color-scheme: dark)').matches
}

// Follows prefers-color-scheme so 'auto' mode flips live when the OS theme changes
// (e.g. macOS switching at sunset). Defaults to light where matchMedia is missing, so the
// first render does not flash dark on a system that is not dark.
function useSystemDark(): boolean {
  const [dark, setDark] = useState(systemPrefersDarkNow)
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return
    const mql = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = (e: MediaQueryListEvent) => setDark(e.matches)
    // Some older Safari versions only expose addListener.
    if (mql.addEventListener) mql.addEventListener('change', onChange)
    else mql.addListener(onChange)
    return () => {
      if (mql.removeEventListener) mql.removeEventListener('change', onChange)
      else mql.removeListener(onChange)
    }
  }, [])
  return dark
}

// Layout effect, not a passive one: the class has to be on <html> before the
// first paint after a mode change, or the page flashes the old palette.
export function useHtmlAppearance(a: HtmlAppearance): void {
  const { mode, density, color } = a
  useLayoutEffect(() => {
    applyHtmlAppearance(document.documentElement, { mode, density, color })
  }, [mode, density, color])
}

// The whole appearance wiring in one hook: the stored choice (mode, density, the user's
// colour or else the site's), the OS preference for 'auto', and the write to <html>.
export function useAppearanceSync(): void {
  const mode = useAppearanceStore(s => s.mode)
  const density = useAppearanceStore(s => s.density)
  const color = useAppearanceStore(selectEffectiveColor)
  const systemDark = useSystemDark()
  useHtmlAppearance({ mode: resolveEffectiveMode(mode, systemDark), density, color })
}

import { useLayoutEffect } from 'react'
import { COLOR_PRESETS } from './presets'
import { tokensFromSource } from './tokensFromSource'
import type { Density } from '@/stores/appearance'

export interface HtmlAppearance {
  // The concrete mode, after 'auto' has been resolved against the OS
  // preference. App.tsx already owns that resolution (it also feeds MUI), so
  // it is passed in rather than re-derived here.
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

// applyHtmlAppearance mirrors the appearance store onto <html>, which is where
// the Tailwind/shadcn side reads it: the `dark` class, data-density and
// data-brand. MUI keeps reading the store through its own ThemeProvider; this
// is one-way and never reads back.
export function applyHtmlAppearance(root: HTMLElement, a: HtmlAppearance): void {
  root.classList.toggle('dark', a.mode === 'dark')
  root.dataset.density = a.density

  const preset = brandPresetFor(a.color)
  if (preset) {
    // Presets are defined in the stylesheet (light and dark), so no inline
    // value: an inline --brand would beat the stylesheet and pin one mode.
    root.dataset.brand = preset
    root.style.removeProperty('--brand')
    root.style.removeProperty('--sidebar-primary')
    return
  }
  // A custom colour has no stylesheet block. Use the same primary tone the MUI
  // theme derives for this mode, so both stacks agree on what "brand" is.
  root.dataset.brand = 'custom'
  const primary = tokensFromSource(a.color, a.mode).primary
  root.style.setProperty('--brand', primary)
  root.style.setProperty('--sidebar-primary', primary)
}

// Layout effect, not a passive one: the class has to be on <html> before the
// first paint after a mode change, or the page flashes the old palette.
export function useHtmlAppearance(a: HtmlAppearance): void {
  const { mode, density, color } = a
  useLayoutEffect(() => {
    applyHtmlAppearance(document.documentElement, { mode, density, color })
  }, [mode, density, color])
}

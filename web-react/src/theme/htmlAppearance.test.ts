// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest'
import { COLOR_PRESETS } from './presets'
import { applyHtmlAppearance, brandPresetFor } from './htmlAppearance'
import { tokensFromSource } from './tokensFromSource'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const root = document.documentElement

beforeEach(() => {
  root.removeAttribute('class')
  root.removeAttribute('data-density')
  root.removeAttribute('data-brand')
  root.removeAttribute('style')
})

describe('brandPresetFor', () => {
  it('maps every preset hex to its id, case-insensitively', () => {
    for (const p of COLOR_PRESETS) {
      expect(brandPresetFor(p.hex)).toBe(p.id)
      expect(brandPresetFor(p.hex.toLowerCase())).toBe(p.id)
    }
  })
  it('returns null for a colour that is not a preset', () => {
    expect(brandPresetFor('#123456')).toBeNull()
  })
})

describe('applyHtmlAppearance', () => {
  it('carries the dark class exactly when the effective mode is dark', () => {
    applyHtmlAppearance(root, { mode: 'dark', density: 'comfortable', color: '#0061A4' })
    expect(root.classList.contains('dark')).toBe(true)
    applyHtmlAppearance(root, { mode: 'light', density: 'comfortable', color: '#0061A4' })
    expect(root.classList.contains('dark')).toBe(false)
  })

  it('leaves unrelated classes on <html> alone', () => {
    root.classList.add('keep-me')
    applyHtmlAppearance(root, { mode: 'dark', density: 'compact', color: '#0061A4' })
    applyHtmlAppearance(root, { mode: 'light', density: 'compact', color: '#0061A4' })
    expect(root.classList.contains('keep-me')).toBe(true)
  })

  it('mirrors density into data-density', () => {
    applyHtmlAppearance(root, { mode: 'light', density: 'compact', color: '#0061A4' })
    expect(root.dataset.density).toBe('compact')
    applyHtmlAppearance(root, { mode: 'light', density: 'comfortable', color: '#0061A4' })
    expect(root.dataset.density).toBe('comfortable')
  })

  it('uses data-brand and no inline colour for a preset', () => {
    applyHtmlAppearance(root, { mode: 'light', density: 'compact', color: '#006A6B' })
    expect(root.dataset.brand).toBe('teal')
    expect(root.style.getPropertyValue('--brand')).toBe('')
    expect(root.style.getPropertyValue('--sidebar-primary')).toBe('')
  })

  it('sets --brand inline for a custom colour, from the same tone the MUI theme uses', () => {
    applyHtmlAppearance(root, { mode: 'dark', density: 'compact', color: '#123456' })
    expect(root.dataset.brand).toBe('custom')
    const primary = tokensFromSource('#123456', 'dark').primary
    expect(root.style.getPropertyValue('--brand')).toBe(primary)
    expect(root.style.getPropertyValue('--sidebar-primary')).toBe(primary)
  })

  it('clears the inline colour again when the colour goes back to a preset', () => {
    applyHtmlAppearance(root, { mode: 'light', density: 'compact', color: '#123456' })
    applyHtmlAppearance(root, { mode: 'light', density: 'compact', color: '#6750A4' })
    expect(root.dataset.brand).toBe('purple')
    expect(root.style.getPropertyValue('--brand')).toBe('')
  })
})

// The stylesheet is where each preset's --brand lives; a preset added to
// presets.ts without a matching block would silently fall back to the default
// brand colour.
describe('stylesheet', () => {
  // vitest runs from web-react/. (import.meta.url is a jsdom URL here, and a
  // ?raw css import is stubbed empty by vitest's css handling.)
  const css = readFileSync(resolve(process.cwd(), 'src/styles/ui.css'), 'utf8')
  it.each(COLOR_PRESETS.map(p => p.id))('defines light and dark --brand for preset %s', id => {
    expect(css).toContain(`:root[data-brand="${id}"]`)
    expect(css).toContain(`:root.dark[data-brand="${id}"]`)
  })
  it('defines both density variables for both densities', () => {
    for (const d of ['compact', 'comfortable']) expect(css).toContain(`:root[data-density="${d}"]`)
    expect(css).toContain('--control-h')
    expect(css).toContain('--row-h')
  })
})

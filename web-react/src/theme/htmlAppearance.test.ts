// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest'
import { COLOR_PRESETS } from './presets'
import { applyHtmlAppearance, brandPresetFor } from './htmlAppearance'
import { brandTones } from './brandColor'
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

  it('sets color-scheme from the resolved mode so native widgets follow it', () => {
    applyHtmlAppearance(root, { mode: 'dark', density: 'compact', color: '#0061A4' })
    expect(root.style.colorScheme).toBe('dark')
    applyHtmlAppearance(root, { mode: 'light', density: 'compact', color: '#0061A4' })
    expect(root.style.colorScheme).toBe('light')
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

  it('sets --brand inline for a custom colour, from brandTones', () => {
    applyHtmlAppearance(root, { mode: 'dark', density: 'compact', color: '#123456' })
    expect(root.dataset.brand).toBe('custom')
    const primary = brandTones('#123456', 'dark').primary
    expect(root.style.getPropertyValue('--brand')).toBe(primary)
    expect(root.style.getPropertyValue('--sidebar-primary')).toBe(primary)
  })

  it('sets the foreground colours inline too, from brandTones', () => {
    applyHtmlAppearance(root, { mode: 'light', density: 'compact', color: '#123456' })
    const on = brandTones('#123456', 'light').onPrimary
    expect(root.style.getPropertyValue('--brand-foreground')).toBe(on)
    expect(root.style.getPropertyValue('--sidebar-primary-foreground')).toBe(on)
    applyHtmlAppearance(root, { mode: 'dark', density: 'compact', color: '#123456' })
    expect(root.style.getPropertyValue('--brand-foreground')).toBe(brandTones('#123456', 'dark').onPrimary)
  })

  it('removes the inline foregrounds when going back to a preset', () => {
    applyHtmlAppearance(root, { mode: 'light', density: 'compact', color: '#123456' })
    applyHtmlAppearance(root, { mode: 'light', density: 'compact', color: '#0061A4' })
    expect(root.style.getPropertyValue('--brand-foreground')).toBe('')
    expect(root.style.getPropertyValue('--sidebar-primary-foreground')).toBe('')
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
    expect(css).toContain(`.dark[data-brand="${id}"]`)
  })
  it('defines both density variables for both densities', () => {
    for (const d of ['compact', 'comfortable']) expect(css).toContain(`:root[data-density="${d}"]`)
    expect(css).toContain('--control-h')
    expect(css).toContain('--row-h')
  })
})

// A preset's four brand variables are written out in the stylesheet, for both modes; a
// preset with a missing one would fall back to the default brand colour in that mode only.
describe('stylesheet brand variables', () => {
  const css = readFileSync(resolve(process.cwd(), 'src/styles/ui.css'), 'utf8')
  const declared = (selector: string): Record<string, string> => {
    const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const m = new RegExp(`${escaped}\\s*\\{([^}]*)\\}`).exec(css)
    if (!m) throw new Error(`no rule for ${selector}`)
    return Object.fromEntries(
      m[1].split(';').map(d => d.trim()).filter(Boolean).map(d => {
        const i = d.indexOf(':')
        return [d.slice(0, i).trim(), d.slice(i + 1).trim().toLowerCase()]
      }),
    )
  }
  for (const preset of COLOR_PRESETS) {
    for (const mode of ['light', 'dark'] as const) {
      it(`${preset.id} ${mode} defines fill and foreground as hex`, () => {
        const got = declared(mode === 'dark' ? `.dark[data-brand="${preset.id}"]` : `:root[data-brand="${preset.id}"]`)
        for (const v of ['--brand', '--sidebar-primary', '--brand-foreground', '--sidebar-primary-foreground']) {
          expect(got[v]).toMatch(/^#[0-9a-f]{6}$/)
        }
        expect(got['--sidebar-primary']).toBe(got['--brand'])
        expect(got['--sidebar-primary-foreground']).toBe(got['--brand-foreground'])
      })
    }
  }
})

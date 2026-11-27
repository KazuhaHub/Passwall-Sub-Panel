import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { COLOR_PRESETS } from './presets'
import { brandTones, contrastRatio, oklchToHex } from './brandColor'

const HEX = /^#[0-9a-f]{6}$/

describe('brandTones', () => {
  const samples = ['#123456', '#FFFFFF', '#000000', '#FFFF00', '#00FF00', '#FF0000', '#808080', '#6750A4', '#EEEEFF']

  it.each(samples)('returns valid hex colours for %s in both modes', hex => {
    for (const mode of ['light', 'dark'] as const) {
      const t = brandTones(hex, mode)
      expect(t.primary).toMatch(HEX)
      expect(t.onPrimary).toMatch(HEX)
    }
  })

  it.each(samples)('keeps the label on the fill legible (4.5:1) for %s in both modes', hex => {
    for (const mode of ['light', 'dark'] as const) {
      const t = brandTones(hex, mode)
      expect(contrastRatio(t.primary, t.onPrimary)).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('keeps white on the fill at 4.5:1 in light mode for every hue and chroma', () => {
    // A fixed lightness is not a contrast guarantee: the luminance of an OKLCH colour moves with its hue.
    const worst: string[] = []
    for (let hue = 0; hue < 360; hue += 5) {
      for (const chroma of [0.02, 0.1, 0.17, 0.3]) {
        const source = oklchToHex(0.6, chroma, (hue * Math.PI) / 180)
        const t = brandTones(source, 'light')
        if (contrastRatio(t.primary, t.onPrimary) < 4.5) worst.push(`${source} -> ${t.primary}`)
      }
    }
    expect(worst).toEqual([])
  })

  it('makes the fill lighter in dark mode than in light mode', () => {
    const light = brandTones('#123456', 'light').primary
    const dark = brandTones('#123456', 'dark').primary
    expect(contrastRatio(dark, '#000000')).toBeGreaterThan(contrastRatio(light, '#000000'))
  })

  it('keeps a grey grey rather than tinting it', () => {
    const { primary } = brandTones('#808080', 'light')
    const n = parseInt(primary.slice(1), 16)
    const [r, g, b] = [(n >> 16) & 255, (n >> 8) & 255, n & 255]
    expect(Math.max(r, g, b) - Math.min(r, g, b)).toBeLessThanOrEqual(2)
  })

  it('keeps the hue of the chosen colour', () => {
    // #123456 is a blue: the derived fill is blue-dominant in both modes.
    for (const mode of ['light', 'dark'] as const) {
      const n = parseInt(brandTones('#123456', mode).primary.slice(1), 16)
      expect(n & 255).toBeGreaterThan((n >> 16) & 255)
    }
  })

  it('fits any lightness and chroma into sRGB without throwing', () => {
    expect(oklchToHex(0.5, 0.4, 2)).toMatch(HEX)
    expect(oklchToHex(0.99, 0.3, 5)).toMatch(HEX)
  })
})

// The presets are written out in the stylesheet. Re-deriving a preset from its own hex is
// not meant to reproduce those values exactly (a preset's colour is a designed one), but it
// must land in the same neighbourhood, or a custom colour would look like a different
// system from the presets next to it in the menu.
describe('calibration against the stylesheet presets', () => {
  const css = readFileSync(resolve(process.cwd(), 'src/styles/ui.css'), 'utf8')
  const declared = (selector: string): string => {
    const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
    const m = new RegExp(`${escaped}\\s*\\{[^}]*--brand:\\s*(#[0-9a-fA-F]{6})`).exec(css)
    if (!m) throw new Error(`no rule for ${selector}`)
    return m[1].toLowerCase()
  }
  const dist = (a: string, b: string) => {
    const p = (h: string) => [1, 3, 5].map(i => parseInt(h.slice(i, i + 2), 16))
    const [x, y] = [p(a), p(b)]
    return Math.hypot(x[0] - y[0], x[1] - y[1], x[2] - y[2])
  }
  for (const preset of COLOR_PRESETS) {
    for (const mode of ['light', 'dark'] as const) {
      it(`${preset.id} ${mode} is within reach of the stylesheet value`, () => {
        const want = declared(mode === 'dark' ? `.dark[data-brand="${preset.id}"]` : `:root[data-brand="${preset.id}"]`)
        expect(dist(brandTones(preset.hex, mode).primary, want)).toBeLessThan(70)
      })
    }
  }
})

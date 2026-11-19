import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

import { TONES, TONE_STYLE } from './tones'

// EVERY BADGE'S WORDS READ AT 4.5:1 in both themes (WCAG AA for the 12px text a
// badge uses), and its dot or icon at 3:1 (WCAG's bar for a graphical object).
// This is the port of ToneBadge.contrast.test.ts to the Tailwind tokens: the old
// test read the colours off the MUI theme object, this one reads them off
// styles/ui.css, which is where the new stack defines them, so a token edited
// there is held to the same bar. The words are --foreground on the tinted fill
// (the hue is on the icon), which is what makes the amber and red tones pass.

const css = readFileSync(fileURLToPath(new URL('../../styles/ui.css', import.meta.url)), 'utf8')

type Linear = [number, number, number]

function block(selector: RegExp): Record<string, string> {
  const m = selector.exec(css)
  if (!m) throw new Error(`no block for ${selector}`)
  const out: Record<string, string> = {}
  for (const d of m[1].matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) out[d[1]] = d[2].trim()
  return out
}

// :root also carries the density variables further down; the first block is the
// palette, and `.dark` has no attribute selector in front of it.
const light = block(/(?:^|\n):root\s*\{([^}]*--background[^}]*)\}/)
const dark = { ...light, ...block(/(?:^|\n)\.dark\s*\{([^}]*)\}/) }

function oklch(value: string): { rgb: Linear; alpha: number } {
  const m = /^oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)(?:\s*\/\s*([\d.]+)(%?))?\s*\)$/.exec(value)
  if (!m) throw new Error(`not an oklch colour: ${value}`)
  const [L, C, h] = [Number(m[1]), Number(m[2]), Number(m[3])]
  const a = C * Math.cos((h * Math.PI) / 180)
  const b = C * Math.sin((h * Math.PI) / 180)
  const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const mm = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3
  const clamp = (v: number) => Math.min(1, Math.max(0, v))
  const rgb: Linear = [
    clamp(4.0767416621 * l - 3.3077115913 * mm + 0.2309699292 * s),
    clamp(-1.2684380046 * l + 2.6097574011 * mm - 0.3413193965 * s),
    clamp(-0.0041960863 * l - 0.7034186147 * mm + 1.707614701 * s),
  ]
  const alpha = m[4] === undefined ? 1 : m[5] ? Number(m[4]) / 100 : Number(m[4])
  return { rgb, alpha }
}

const luminance = ([r, g, b]: Linear) => 0.2126 * r + 0.7152 * g + 0.0722 * b

function contrast(a: Linear, b: Linear): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

// Alpha compositing happens in linear light here; browsers composite in sRGB
// space by default, which differs by a hair for a 12% tint. The margins below
// are wide enough that it does not matter, and the test says so by being strict
// about the thresholds rather than the arithmetic.
function over(top: Linear, alpha: number, bottom: Linear): Linear {
  return top.map((v, i) => v * alpha + bottom[i] * (1 - alpha)) as Linear
}

describe.each([['light', light], ['dark', dark]] as const)('badge contrast, %s theme', (_mode, vars) => {
  const colour = (name: string) => oklch(vars[name])
  const surfaces = ['--background', '--card'] as const

  describe.each(TONES)('%s', tone => {
    const t = TONE_STYLE[tone].tokens
    const tint = colour(t.tint)

    it.each(surfaces)('words (--foreground) read at 4.5:1 on the tint over %s', surface => {
      const fill = over(tint.rgb, t.tintAlpha * tint.alpha, colour(surface).rgb)
      expect(contrast(colour('--foreground').rgb, fill)).toBeGreaterThanOrEqual(4.5)
    })

    it.each(surfaces)('the dot or icon reads at 3:1 on the tint over %s', surface => {
      const fill = over(tint.rgb, t.tintAlpha * tint.alpha, colour(surface).rgb)
      expect(contrast(colour(t.ink).rgb, fill)).toBeGreaterThanOrEqual(3)
    })

    it.each(surfaces)('the dot reads at 3:1 on the bare %s, where a quiet status sits', surface => {
      expect(contrast(colour(t.dot).rgb, colour(surface).rgb)).toBeGreaterThanOrEqual(3)
    })

    it('names the same tokens in its Tailwind classes as in its token spec', () => {
      const s = TONE_STYLE[tone]
      const bare = (v: string) => v.replace(/^--/, '')
      // Neutral's dot is a softened muted-foreground, so match on the token, not the whole class.
      expect(s.ink).toContain(bare(t.ink))
      expect(s.tint).toContain(bare(t.tint))
      if (t.tintAlpha < 1) expect(s.tint).toContain(`/${Math.round(t.tintAlpha * 100)}`)
    })
  })

  // The toast icons (components/feedback/ToastHost.tsx) sit on the toast's own surface, --popover.
  it.each([['success', '--success'], ['warning', '--warning-ink'], ['error', '--destructive'], ['info', '--info']])(
    'the %s toast icon (%s) reads at 3:1 on the toast surface', (_n, token) => {
      expect(contrast(colour(token).rgb, colour('--popover').rgb)).toBeGreaterThanOrEqual(3)
    })

  it('keeps info distinct from the brand colour, so an orange brand cannot make info look like a warning', () => {
    expect(vars['--info']).not.toBe(vars['--brand'])
    expect(vars['--info']).not.toBe(vars['--warning-ink'])
  })

  it('keeps the plain-words (quiet) tones readable on the bare surface too', () => {
    for (const surface of surfaces) {
      expect(contrast(colour('--foreground').rgb, colour(surface).rgb)).toBeGreaterThanOrEqual(4.5)
    }
  })
})

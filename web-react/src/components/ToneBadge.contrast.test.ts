import { describe, expect, it } from 'vitest'
import type { Theme } from '@mui/material'

import { createAppTheme, DEFAULT_PRESET_HEX } from '@/theme'
import { severityTone, stateTone } from './ToneBadge'
import type { CardState, Severity } from '@/utils/diagnostics'

// EVERY BADGE'S WORDS READ AT 4.5:1 in both themes (WCAG AA for the 12.5px
// text a badge uses). The amber and ok tones keep the hue on the icon and
// draw the words in onSurface; this is what holds them to it.

type RGBA = [number, number, number, number]

function parse(color: string): RGBA {
  const hex = /^#([0-9a-f]{3,8})$/i.exec(color)
  if (hex) {
    let h = hex[1]
    if (h.length === 3 || h.length === 4) h = [...h].map(c => c + c).join('')
    const n = (i: number) => parseInt(h.slice(i, i + 2), 16)
    return [n(0), n(2), n(4), h.length === 8 ? n(6) / 255 : 1]
  }
  const rgb = /^rgba?\(([^)]+)\)$/i.exec(color)
  if (rgb) {
    const p = rgb[1].split(/[,\s/]+/).filter(Boolean).map(Number)
    return [p[0], p[1], p[2], p[3] ?? 1]
  }
  throw new Error(`unparseable colour ${color}`)
}

function over(top: RGBA, bottom: RGBA): RGBA {
  const a = top[3] + bottom[3] * (1 - top[3])
  const ch = (i: number) => (top[i] * top[3] + bottom[i] * bottom[3] * (1 - top[3])) / (a || 1)
  return [ch(0), ch(1), ch(2), a]
}

function luminance([r, g, b]: RGBA): number {
  const f = (v: number) => {
    const s = v / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
}

export function contrast(text: string, bg: string, surface: string): number {
  const base = parse(surface)
  const fill = over(parse(bg), base)
  const fg = over(parse(text), fill)
  const [hi, lo] = [luminance(fg), luminance(fill)].sort((a, b) => b - a)
  return (hi + 0.05) / (lo + 0.05)
}

const STATES: CardState[] = ['failing', 'attention', 'ok', 'idle', 'measuring', 'inhibited', 'not_applicable', 'none', 'recorded']
const SEVERITIES: Severity[] = ['critical', 'error', 'warn', 'notice']

describe.each(['light', 'dark'] as const)('badge contrast, %s theme', mode => {
  const theme: Theme = createAppTheme({ mode, sourceColor: DEFAULT_PRESET_HEX, language: 'en-US' })
  const surface = theme.palette.md.surface

  it.each(STATES)('card state %s reads at 4.5:1', state => {
    const tone = stateTone(theme, state)
    expect(contrast(tone.text ?? tone.fg, tone.bg, surface)).toBeGreaterThanOrEqual(4.5)
  })

  it.each(SEVERITIES)('severity %s reads at 4.5:1', severity => {
    const tone = severityTone(theme, severity)
    expect(contrast(tone.text ?? tone.fg, tone.bg, surface)).toBeGreaterThanOrEqual(4.5)
  })

  it('keeps the hue on the icon and uses onSurface for the words on the amber and ok tones', () => {
    for (const state of ['attention', 'ok'] as const) {
      const tone = stateTone(theme, state)
      expect(tone.text).toBe(theme.palette.md.onSurface)
      expect(tone.fg).not.toBe(tone.text)
    }
  })
})

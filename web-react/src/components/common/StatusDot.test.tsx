// @vitest-environment jsdom
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'

import { StatusDot, ToneDot } from './StatusDot'
import { TONES, TONE_STYLE } from './tones'

afterEach(cleanup)

describe('the one status dot', () => {
  it('draws the tone colour from tones.ts and hides itself from assistive tech', () => {
    render(<ToneDot tone="danger" />)
    const dot = document.querySelector('[data-slot="tone-dot"]')!
    expect(dot.className).toContain(TONE_STYLE.danger.ink)
    expect(dot.getAttribute('aria-hidden')).toBe('true')
  })

  it('is what StatusDot draws, so the words always come with a dot that is the same one', () => {
    render(<StatusDot tone="success" label="在线" />)
    expect(document.querySelector('[data-slot="status-dot"] [data-slot="tone-dot"]')).not.toBeNull()
    expect(screen.getByText('在线')).toBeTruthy()
  })

  it('gives every tone a connector border, so line drawings need no colour map of their own', () => {
    for (const tone of TONES) expect(TONE_STYLE[tone].line).toMatch(/^border-/)
  })

  it('draws a connector in the same ink as its icon, so one edge never shows two ambers', () => {
    for (const tone of TONES) {
      const ink = TONE_STYLE[tone].ink.replace(/^text-/, '')
      if (tone === 'neutral') continue // the neutral line is a softened muted-foreground by design
      expect(TONE_STYLE[tone].line, tone).toBe(`border-${ink}`)
    }
  })

  // WCAG 1.4.1: warning, danger and neutral differ little in lightness, so colour alone cannot
  // be what tells a row's state. Each tone has its own SHAPE, drawn at the same size.
  it('gives every tone its own shape, so a status is never told by colour alone', () => {
    const shapes = TONES.map(tone => {
      const { container, unmount } = render(<ToneDot tone={tone} />)
      const dot = container.querySelector('[data-slot="tone-dot"]')!
      const shape = dot.getAttribute('data-shape')
      expect(shape, tone).toBeTruthy()
      expect(dot.querySelector('svg'), tone).not.toBeNull()
      unmount()
      return shape
    })
    expect(new Set(shapes).size).toBe(TONES.length)
  })

  it('draws the shapes in the tone colour at the dot size, hidden from assistive tech', () => {
    render(<ToneDot tone="warning" />)
    const dot = document.querySelector('[data-slot="tone-dot"]')!
    expect(dot.className).toContain('size-2')
    expect(dot.className).toContain(TONE_STYLE.warning.ink)
    expect(dot.getAttribute('aria-hidden')).toBe('true')
  })

  // The most urgent tone must not be the faintest: compare how much of the 8-unit box each shape inks.
  it('inks danger at least as much as every other tone, and every shape stays visible at 8px', () => {
    const num = (el: Element, name: string) => Number(el.getAttribute(name))
    function area(tone: (typeof TONES)[number]): number {
      const { container, unmount } = render(<ToneDot tone={tone} />)
      const svg = container.querySelector('svg')!
      const shape = svg.firstElementChild!
      let a = 0
      if (shape.tagName === 'polygon') {
        const pts = shape.getAttribute('points')!.trim().split(/\s+/).map(p => p.split(',').map(Number))
        for (let i = 0; i < pts.length; i++) { const [x1, y1] = pts[i]; const [x2, y2] = pts[(i + 1) % pts.length]; a += x1 * y2 - x2 * y1 }
        a = Math.abs(a) / 2
      } else if (shape.tagName === 'rect') {
        a = num(shape, 'width') * num(shape, 'height')
      } else if (shape.getAttribute('fill') === 'none') {
        const r = num(shape, 'r'); const w = num(shape, 'stroke-width')
        a = Math.PI * ((r + w / 2) ** 2 - (r - w / 2) ** 2)
      } else {
        a = Math.PI * num(shape, 'r') ** 2
      }
      unmount()
      return a
    }
    const areas = Object.fromEntries(TONES.map(t => [t, area(t)])) as Record<(typeof TONES)[number], number>
    for (const t of TONES) {
      expect(areas.danger, `danger vs ${t}`).toBeGreaterThanOrEqual(areas[t])
      expect(areas[t], t).toBeGreaterThan(25)
    }
  })
})

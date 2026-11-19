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
})

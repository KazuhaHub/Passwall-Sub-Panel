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
    expect(dot.className).toContain(TONE_STYLE.danger.dot)
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
})

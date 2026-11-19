import { describe, expect, it } from 'vitest'
import { landingColorIndex, landingColorVar } from './landingColor'

describe('landing colours', () => {
  it('gives the same landing the same colour every time, whatever the type of its id', () => {
    expect(landingColorIndex(12)).toBe(landingColorIndex(12))
    expect(landingColorIndex(12)).toBe(landingColorIndex('12'))
    expect(landingColorVar(12)).toBe(landingColorVar('12'))
  })

  it('stays inside the 8-colour palette', () => {
    for (let id = 0; id < 500; id++) {
      const n = landingColorIndex(id)
      expect(n).toBeGreaterThanOrEqual(1)
      expect(n).toBeLessThanOrEqual(8)
    }
    expect(landingColorVar(3)).toMatch(/^var\(--landing-[1-8]\)$/)
  })

  it('spreads typical ids over most of the palette instead of piling onto a few colours', () => {
    const used = new Set(Array.from({ length: 12 }, (_, i) => landingColorIndex(i + 1)))
    expect(used.size).toBeGreaterThanOrEqual(6)
  })
})

import { describe, expect, it } from 'vitest'
import { assignLandingColors, landingColorStyle, landingColorVar } from './landingColor'

describe('assignLandingColors', () => {
  it('gives the first eight landings eight different colours, whatever their ids', () => {
    // ids 4 and 10, 5 and 11 collided under a hash
    const map = assignLandingColors([4, 10, 5, 11, 1, 2, 3, 12])
    const indexes = [...map.values()].map(c => c.index)
    expect(new Set(indexes).size).toBe(8)
    expect(indexes.every(i => i >= 1 && i <= 8)).toBe(true)
  })

  it('assigns by first appearance in the list, so the same list always gives the same colours', () => {
    const a = assignLandingColors([30, 10, 20])
    const b = assignLandingColors([30, 10, 20])
    expect(a.get('30')!.index).toBe(1)
    expect(a.get('10')!.index).toBe(2)
    expect(a.get('20')!.index).toBe(3)
    expect([...a]).toEqual([...b])
  })

  it('treats a numeric id and its string form as one landing, and counts a repeat once', () => {
    const map = assignLandingColors([12, '12', 13])
    expect(map.size).toBe(2)
    expect(map.get('13')!.index).toBe(2)
  })

  it('cycles after eight and marks the cycle, so a second cue can tell repeats apart', () => {
    const map = assignLandingColors(Array.from({ length: 10 }, (_, i) => i + 1))
    expect(map.get('1')).toEqual({ index: 1, cycle: 0 })
    expect(map.get('9')).toEqual({ index: 1, cycle: 1 })
    expect(map.get('10')).toEqual({ index: 2, cycle: 1 })
  })
})

describe('landing colour rendering', () => {
  it('uses the theme variable', () => {
    expect(landingColorVar({ index: 3, cycle: 0 })).toBe('var(--landing-3)')
  })

  it('adds a stripe pattern from the second cycle on, as a cue that is not colour', () => {
    expect(landingColorStyle({ index: 1, cycle: 0 }).backgroundImage).toBeUndefined()
    expect(landingColorStyle({ index: 1, cycle: 1 }).backgroundImage).toContain('repeating-linear-gradient')
  })
})

import { describe, expect, it } from 'vitest'
import { carriedDistance, settleDeltas, slideShift, translateYOf } from './reorderMotion'

describe('slideShift: how far each other row moves to open the gap', () => {
  // five rows, a..e at 0..4, each 40px tall
  const shifts = (from: number, to: number) => [0, 1, 2, 3, 4].map(i => slideShift(i, from, to, 40))

  it('moves nothing while the row is over its own slot', () => {
    expect(shifts(2, 2)).toEqual([0, 0, 0, 0, 0])
  })
  it('lifts the rows it passes by one row when it moves down', () => {
    // b goes to slot 3: c and d move up
    expect(shifts(1, 3)).toEqual([0, 0, -40, -40, 0])
  })
  it('pushes the rows it passes down by one row when it moves up', () => {
    // d goes to slot 1: b and c move down
    expect(shifts(3, 1)).toEqual([0, 40, 40, 0, 0])
  })
  it('does not shift the dragged row itself (it follows the pointer)', () => {
    expect(slideShift(1, 1, 3, 40)).toBe(0)
    expect(slideShift(3, 3, 1, 40)).toBe(0)
  })
  it('reaches the ends of the list', () => {
    expect(shifts(0, 4)).toEqual([0, -40, -40, -40, -40])
    expect(shifts(4, 0)).toEqual([40, 40, 40, 40, 0])
  })
})

describe('settleDeltas: where rows were drawn against where they now are', () => {
  it('gives each row the distance to travel back, and drops rows that did not move', () => {
    const before = new Map([['a', 100], ['b', 140], ['c', 180]])
    const after = new Map([['a', 140], ['b', 100], ['c', 180]])
    expect(settleDeltas(before, after)).toEqual(new Map([['a', -40], ['b', 40]]))
  })
  it('ignores sub-pixel differences and rows only one side knows', () => {
    const before = new Map([['a', 100], ['gone', 10]])
    const after = new Map([['a', 100.3], ['new', 50]])
    expect(settleDeltas(before, after).size).toBe(0)
  })
})

describe('translateYOf: the vertical offset a computed transform applies', () => {
  it('reads a matrix', () => expect(translateYOf('matrix(1, 0, 0, 1, 0, -40)')).toBe(-40))
  it('reads translateY', () => expect(translateYOf('translateY(12.5px)')).toBe(12.5))
  it('is 0 for none or nothing', () => {
    expect(translateYOf('none')).toBe(0)
    expect(translateYOf('')).toBe(0)
  })
})

describe('carriedDistance: how far a carried row has travelled with rows of unequal height', () => {
  const hs = [40, 60, 30, 50]
  it('sums the heights of the rows passed going down', () => expect(carriedDistance(hs, 0, 2)).toBe(90))
  it('is negative and sums the rows passed going up', () => expect(carriedDistance(hs, 3, 1)).toBe(-90))
  it('is 0 in place', () => expect(carriedDistance(hs, 2, 2)).toBe(0))
})

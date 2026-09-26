import { describe, expect, it } from 'vitest'
import vectors from './goParseFloat.vectors.json'
import { goParseFloat } from './goParseFloat'

// The vectors are what Go's strconv.ParseFloat returned for each input, and
// the sqlstore test runs the same file through floatField — so this test is
// against the server's decoder, not against a reading of its documentation.
// A second table here would be free to drift from that one.

// JSON has no infinity or NaN, so the file spells them, and -0 too: Vite's
// JSON import re-serializes a -0 literal as 0. null is "Go returned an
// error", which the settings layer answers by skipping the stored value.
function expected(out: number | string | null): number | undefined {
  switch (out) {
    case null: return undefined
    case '+Inf': return Infinity
    case '-Inf': return -Infinity
    case 'NaN': return NaN
    case '-0': return -0
    default:
      if (typeof out !== 'number') throw new Error(`unknown vector output ${JSON.stringify(out)}`)
      return out
  }
}

describe('goParseFloat', () => {
  for (const group of vectors.groups) {
    it(`agrees with strconv.ParseFloat: ${group.name}`, () => {
      for (const tc of group.cases) {
        const want = expected(tc.out)
        const got = goParseFloat(tc.in)
        // Object.is, so -0 must stay -0 and NaN must be NaN: the vectors pin
        // the exact float64, not merely an equal one.
        expect(Object.is(got, want), `${JSON.stringify(tc.in)}: got ${String(got)}, want ${String(want)}`).toBe(true)
      }
    })
  }

  it('reads every group the vectors carry', () => {
    // A renamed or emptied group would otherwise pass by testing nothing.
    expect(vectors.groups.map(g => g.name)).toEqual(['decimal', 'not decimal', 'range', 'special', 'underscore', 'hex'])
    for (const g of vectors.groups) expect(g.cases.length, g.name).toBeGreaterThan(0)
  })
})

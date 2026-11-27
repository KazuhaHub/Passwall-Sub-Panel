import { describe, expect, it } from 'vitest'
import { diffLines, diffStats, hunksOf } from './diff'

const lines = (s: string) => (s === '' ? [] : s.split(','))
const apply = (ops: ReturnType<typeof diffLines>) => ({
  a: ops.filter(o => o.kind !== 'add').map(o => o.text),
  b: ops.filter(o => o.kind !== 'del').map(o => o.text),
})

describe('diffLines', () => {
  it.each([
    ['', ''],
    ['a,b,c', 'a,b,c'],
    ['a,b,c', 'a,x,c'],
    ['a,b,c', 'a,c'],
    ['a,c', 'a,b,c'],
    ['', 'a,b'],
    ['a,b', ''],
    ['a,b,c,d,e', 'e,d,c,b,a'],
    ['a,a,a,b', 'a,b,a,a'],
    ['1,2,3,4,5,6,7,8', '1,2,x,4,5,6,y,8'],
  ])('%j -> %j reproduces both sides', (x, y) => {
    const a = lines(x); const b = lines(y)
    expect(apply(diffLines(a, b))).toEqual({ a, b })
  })

  it('counts a single change as one removed and one added', () => {
    expect(diffStats(['a', 'b', 'c'], ['a', 'x', 'c'])).toEqual({ added: 1, removed: 1 })
  })

  it('sees a moved run as a small change, not a rewrite', () => {
    const base = Array.from({ length: 6000 }, (_, i) => `r${i}`)
    const moved = [...base.slice(3000, 3600), ...base.slice(0, 3000), ...base.slice(3600)]
    const { added, removed } = diffStats(base, moved)
    // The shorter side of the swap moves; the 600 lines are the change, not the 3000.
    expect(added).toBeLessThanOrEqual(3000)
    expect(added).toBe(removed)
    expect(apply(diffLines(base, moved))).toEqual({ a: base, b: moved })
  })

  it('groups changes into hunks with their neighbours, numbered from 1', () => {
    const a = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '10']
    const b = ['1', '2', '3', 'x', '5', '6', '7', '8', '9', 'y']
    const hunks = hunksOf(a, b, 1)
    expect(hunks).toHaveLength(2)
    expect(hunks[0].lines.map(l => l.kind + l.text)).toEqual(['same3', 'del4', 'addx', 'same5'])
    expect(hunks[0]).toMatchObject({ oldStart: 3, newStart: 3 })
    expect(hunks[1].lines.map(l => l.kind + l.text)).toEqual(['same9', 'del10', 'addy'])
  })
})

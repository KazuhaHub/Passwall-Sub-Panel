import { describe, expect, it } from 'vitest'
import {
  PAGE_SIZE_OPTIONS, applyReorder, clampPage, filterRows, insertionIndex, pageCount, pageRange, reorderCall, sliceRows,
} from './dataTableLogic'

describe('paging maths', () => {
  it('offers the page sizes the app already uses', () => {
    expect([...PAGE_SIZE_OPTIONS]).toEqual([10, 25, 50, 100])
  })

  it('counts pages and never reports fewer than one', () => {
    expect(pageCount(0, 25)).toBe(1)
    expect(pageCount(25, 25)).toBe(1)
    expect(pageCount(26, 25)).toBe(2)
  })

  it('clamps a stale page after the data shrinks', () => {
    expect(clampPage(9, 30, 25)).toBe(2)
    expect(clampPage(0, 30, 25)).toBe(1)
    expect(clampPage(Number.NaN, 30, 25)).toBe(1)
  })

  it('reports the visible range, 0-0 when empty', () => {
    expect(pageRange(1, 25, 3421, 25)).toEqual({ from: 1, to: 25 })
    expect(pageRange(2, 25, 30, 5)).toEqual({ from: 26, to: 30 })
    expect(pageRange(1, 25, 0, 0)).toEqual({ from: 0, to: 0 })
  })

  it('slices a page out of in-memory rows', () => {
    const rows = Array.from({ length: 12 }, (_, i) => i)
    expect(sliceRows(rows, 2, 5)).toEqual([5, 6, 7, 8, 9])
    expect(sliceRows(rows, 3, 5)).toEqual([10, 11])
  })
})

describe('filterRows', () => {
  const rows = [{ n: 'Irvine Home' }, { n: 'Taiwan HiNet Dynamic' }]
  it('matches every word case-insensitively', () => {
    expect(filterRows(rows, r => r.n, 'hinet dyn')).toEqual([rows[1]])
    expect(filterRows(rows, r => r.n, '  ')).toEqual(rows)
    expect(filterRows(rows, r => r.n, 'tokyo')).toEqual([])
  })
})

describe('insertionIndex', () => {
  // Row midpoints at 10, 30, 50: a pointer above the first midpoint inserts at 0.
  const mids = [10, 30, 50]
  it('counts the midpoints the pointer has passed', () => {
    expect(insertionIndex(mids, 0)).toBe(0)
    expect(insertionIndex(mids, 31)).toBe(2)
    expect(insertionIndex(mids, 999)).toBe(3)
  })
})

describe('reorderCall', () => {
  const ids = ['a', 'b', 'c', 'd']
  it('reports the row the moved one now sits before', () => {
    expect(reorderCall(ids, 'a', 2)).toEqual({ movedId: 'a', beforeId: 'd' })
    expect(reorderCall(ids, 'd', 0)).toEqual({ movedId: 'd', beforeId: 'a' })
  })
  it('reports null as the anchor when the row goes last', () => {
    expect(reorderCall(ids, 'a', 3)).toEqual({ movedId: 'a', beforeId: null })
  })
  it('returns null when nothing moved or the id is unknown', () => {
    expect(reorderCall(ids, 'b', 1)).toBeNull()
    expect(reorderCall(ids, 'zzz', 1)).toBeNull()
  })
  it('clamps a target outside the list', () => {
    expect(reorderCall(ids, 'b', 99)).toEqual({ movedId: 'b', beforeId: null })
    expect(reorderCall(ids, 'c', -4)).toEqual({ movedId: 'c', beforeId: 'a' })
  })
})

describe('applyReorder', () => {
  const items = [{ id: 'a' }, { id: 'b' }, { id: 'c' }, { id: 'd' }]
  const ids = (xs: { id: string }[]) => xs.map(x => x.id).join('')
  it('puts the moved item before the anchor', () => {
    expect(ids(applyReorder(items, x => x.id, { movedId: 'a', beforeId: 'd' }))).toBe('bcad')
    expect(ids(applyReorder(items, x => x.id, { movedId: 'd', beforeId: 'b' }))).toBe('adbc')
  })
  it('puts it last when the anchor is null', () => {
    expect(ids(applyReorder(items, x => x.id, { movedId: 'a', beforeId: null }))).toBe('bcda')
  })
  it('leaves the list alone for an unknown id', () => {
    expect(ids(applyReorder(items, x => x.id, { movedId: 'zz', beforeId: 'a' }))).toBe('abcd')
  })
})

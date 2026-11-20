import { describe, expect, it } from 'vitest'
import {
  WINDOW_THRESHOLD, autoScrollStep, carryTarget, insertionAtOffset, pageRows, renderPlan, revealScrollTop,
  rowIndexAtOffset, shouldWindow, windowRange,
} from './windowing'

describe('when to window', () => {
  it('windows only an unpaginated list above 200 rows', () => {
    expect(WINDOW_THRESHOLD).toBe(200)
    expect(shouldWindow(200, false)).toBe(false)
    expect(shouldWindow(201, false)).toBe(true)
    expect(shouldWindow(5000, true)).toBe(false)
  })
})

describe('window range', () => {
  const base = { rowH: 36, count: 5000, overscan: 5 }
  it('covers the viewport plus overscan on both sides', () => {
    // 10 rows from row 100 (offset 3600), viewport 360 high
    expect(windowRange({ ...base, offset: 3600, viewport: 360 })).toEqual({ start: 95, end: 115 })
  })
  it('clamps at the top, including a negative offset (table below the fold)', () => {
    // body starts 500px below the viewport top: nothing visible yet, only the overscan is drawn
    expect(windowRange({ ...base, offset: -500, viewport: 360 })).toEqual({ start: 0, end: 2 })
    expect(windowRange({ ...base, offset: 0, viewport: 360 })).toEqual({ start: 0, end: 15 })
  })
  it('clamps at the bottom and never inverts when scrolled past the end', () => {
    expect(windowRange({ ...base, offset: 5000 * 36 - 100, viewport: 360 }).end).toBe(5000)
    const past = windowRange({ ...base, offset: 5000 * 36 + 9999, viewport: 360 })
    expect(past.start).toBeLessThanOrEqual(past.end)
    expect(past.end).toBe(5000)
  })
  it('is empty for no rows', () => {
    expect(windowRange({ ...base, count: 0, offset: 0, viewport: 360 })).toEqual({ start: 0, end: 0 })
  })
})

describe('geometry of rows', () => {
  it('maps an offset to a row index, clamped to the list', () => {
    expect(rowIndexAtOffset(0, 36, 100)).toBe(0)
    expect(rowIndexAtOffset(35.9, 36, 100)).toBe(0)
    expect(rowIndexAtOffset(36, 36, 100)).toBe(1)
    expect(rowIndexAtOffset(-50, 36, 100)).toBe(0)
    expect(rowIndexAtOffset(99999, 36, 100)).toBe(99)
    expect(rowIndexAtOffset(10, 36, 0)).toBe(0)
  })
  it('maps an offset to an insertion slot with the midpoint rule of the DOM version', () => {
    expect(insertionAtOffset(0, 40, 4)).toBe(0)
    expect(insertionAtOffset(20, 40, 4)).toBe(0) // exactly on the first midpoint: not passed yet
    expect(insertionAtOffset(20.1, 40, 4)).toBe(1)
    expect(insertionAtOffset(125, 40, 4)).toBe(3)
    expect(insertionAtOffset(-30, 40, 4)).toBe(0)
    expect(insertionAtOffset(9999, 40, 4)).toBe(4)
  })
})

describe('auto-scroll', () => {
  const o = { top: 100, bottom: 500, edge: 40, maxStep: 20, heldMs: 0 }
  it('does nothing in the middle of the frame', () => {
    expect(autoScrollStep({ ...o, y: 300 })).toBe(0)
  })
  it('scrolls up near the top and down near the bottom, faster deeper into the zone', () => {
    const shallow = autoScrollStep({ ...o, y: 130 })
    const deep = autoScrollStep({ ...o, y: 105 })
    expect(shallow).toBeLessThan(0)
    expect(deep).toBeLessThan(shallow)
    expect(autoScrollStep({ ...o, y: 495 })).toBeGreaterThan(autoScrollStep({ ...o, y: 470 }))
    expect(autoScrollStep({ ...o, y: 470 })).toBeGreaterThan(0)
  })
  it('never exceeds maxStep at the base rate, also outside the frame', () => {
    expect(autoScrollStep({ ...o, y: 20 })).toBe(-20)
    expect(autoScrollStep({ ...o, y: 900 })).toBe(20)
  })
  it('accelerates the longer the pointer is held in the zone, up to a cap', () => {
    const slow = autoScrollStep({ ...o, y: 495, heldMs: 0 })
    const later = autoScrollStep({ ...o, y: 495, heldMs: 1500 })
    const forever = autoScrollStep({ ...o, y: 495, heldMs: 600000 })
    expect(later).toBeGreaterThan(slow)
    expect(forever).toBeGreaterThanOrEqual(later)
    expect(forever).toBeLessThanOrEqual(20 * 12)
  })
  it('is zero for a frame with no height (not laid out)', () => {
    expect(autoScrollStep({ ...o, top: 0, bottom: 0, y: 0 })).toBe(0)
  })
})

describe('scrolling a row into view', () => {
  const o = { rowH: 36, viewport: 400, topInset: 40 }
  it('leaves the scroll alone when the row is already visible', () => {
    expect(revealScrollTop({ ...o, rowTop: 1000, scrollTop: 900 })).toBe(900)
  })
  it('brings a row above the visible band to just under the sticky header', () => {
    expect(revealScrollTop({ ...o, rowTop: 500, scrollTop: 900 })).toBe(460)
  })
  it('brings a row below the band to the bottom edge', () => {
    expect(revealScrollTop({ ...o, rowTop: 2000, scrollTop: 900 })).toBe(2000 + 36 - 400)
  })
  it('centres on request, never below 0', () => {
    expect(revealScrollTop({ ...o, rowTop: 5000, scrollTop: 0, align: 'center' })).toBe(5000 - 40 - (400 - 40 - 36) / 2)
    expect(revealScrollTop({ ...o, rowTop: 10, scrollTop: 0, align: 'center' })).toBe(0)
  })
})

describe('what is rendered', () => {
  it('renders the window with spacers before and after', () => {
    const p = renderPlan(100, 10, 14, [])
    expect(p.items.map(i => i.index)).toEqual([10, 11, 12, 13])
    expect(p.items[0].gapBefore).toBe(10)
    expect(p.items[1].gapBefore).toBe(0)
    expect(p.tail).toBe(86)
  })
  it('keeps pinned rows (focus, the carried row) even far outside the window', () => {
    const p = renderPlan(5000, 100, 103, [4, 4000, 101])
    expect(p.items.map(i => i.index)).toEqual([4, 100, 101, 102, 4000])
    expect(p.items.map(i => i.gapBefore)).toEqual([4, 95, 0, 0, 3897])
    expect(p.tail).toBe(999)
    // rows plus gaps always add up to the whole list
    const sum = p.items.length + p.items.reduce((s, i) => s + i.gapBefore, 0) + p.tail
    expect(sum).toBe(5000)
  })
  it('clamps a stale window to a list that has just shrunk', () => {
    // the window state lags one render behind the data: 10 000 rows became 50
    const p = renderPlan(50, 9000, 9040, [])
    expect(p.items).toEqual([])
    expect(renderPlan(50, 40, 90, []).items.map(i => i.index)).toEqual([40, 41, 42, 43, 44, 45, 46, 47, 48, 49])
  })
  it('ignores pinned indices that do not exist', () => {
    expect(renderPlan(10, 0, 2, [-1, 99]).items.map(i => i.index)).toEqual([0, 1])
  })
})

describe('keyboard carrying', () => {
  it('moves by one with the arrows, to the ends with Home and End, by a page with PageUp and PageDown', () => {
    expect(carryTarget('ArrowDown', 5, 100, 10)).toBe(6)
    expect(carryTarget('ArrowUp', 5, 100, 10)).toBe(4)
    expect(carryTarget('Home', 50, 100, 10)).toBe(0)
    expect(carryTarget('End', 50, 100, 10)).toBe(99)
    expect(carryTarget('PageDown', 50, 100, 10)).toBe(60)
    expect(carryTarget('PageUp', 50, 100, 10)).toBe(40)
  })
  it('clamps to the list and ignores other keys', () => {
    expect(carryTarget('PageUp', 3, 100, 10)).toBe(0)
    expect(carryTarget('PageDown', 95, 100, 10)).toBe(99)
    expect(carryTarget('ArrowUp', 0, 100, 10)).toBe(0)
    expect(carryTarget('a', 5, 100, 10)).toBeNull()
  })
})

describe('rows per viewport', () => {
  it('counts the rows that fit under the sticky header, at least one', () => {
    expect(pageRows(400, 40, 36)).toBe(10)
    expect(pageRows(30, 0, 36)).toBe(1)
  })
})

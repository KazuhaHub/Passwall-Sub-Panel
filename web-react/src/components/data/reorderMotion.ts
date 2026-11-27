// Pure arithmetic behind the physical feel of row reordering (useRowReorder): which rows
// slide out of the way, and how a row that was drawn somewhere else settles into its place.
// No DOM and no React, so it is tested without layout.

/**
 * How far the row at `index` slides while the row at `from` is carried over slot `to`.
 * Rows between the two slide one row-height toward `from`; every other row, and the carried
 * row itself (it follows the pointer, not this offset), stays put.
 */
export function slideShift(index: number, from: number, to: number, rowH: number): number {
  if (index === from) return 0
  if (to > from && index > from && index <= to) return -rowH
  if (to < from && index >= to && index < from) return rowH
  return 0
}

/**
 * FLIP's "invert" step: for each row, how far it must be drawn from its new place so that it
 * still appears where it was, keyed by row id. `before` and `after` are viewport tops. Rows
 * that did not move (under half a pixel) and rows only one side knows are left out.
 */
export function settleDeltas(before: ReadonlyMap<string, number>, after: ReadonlyMap<string, number>): Map<string, number> {
  const out = new Map<string, number>()
  for (const [id, was] of before) {
    const now = after.get(id)
    if (now === undefined) continue
    const d = was - now
    if (Math.abs(d) >= 0.5) out.set(id, d)
  }
  return out
}

/**
 * The vertical offset of a computed `transform` value. Browsers report `matrix(...)`; a plain
 * `translateY(..)` is read too (that is how jsdom keeps an inline style).
 */
export function translateYOf(transform: string): number {
  const m = /^matrix\(([^)]*)\)$/.exec(transform.trim())
  if (m) {
    const parts = m[1].split(',').map(Number)
    return Number.isFinite(parts[5]) ? parts[5] : 0
  }
  const t = /translateY\((-?[\d.]+)px\)/.exec(transform)
  return t ? Number(t[1]) : 0
}

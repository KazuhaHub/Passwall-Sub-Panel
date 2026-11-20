// Pure geometry behind the windowed DataTable and useRowReorder. Everything here
// is arithmetic on pixel offsets and row heights, with no DOM and no React, so it
// can be tested without layout (jsdom has none). All offsets are in CSS px.

/** Above this many rows an unpaginated table draws only the rows near the viewport. */
export const WINDOW_THRESHOLD = 200
/** Rows drawn beyond each edge of the viewport, so a fast scroll does not show blanks. */
export const OVERSCAN = 8

export function shouldWindow(count: number, paginated: boolean): boolean {
  return !paginated && count > WINDOW_THRESHOLD
}

const clamp = (n: number, lo: number, hi: number) => Math.min(Math.max(n, lo), hi)

export interface RangeInput {
  /** Content y, measured from the top of the first row, that sits at the top of the viewport. Negative while the table is still below the fold. */
  offset: number
  viewport: number
  rowH: number
  count: number
  overscan: number
}

/** The half-open range [start, end) of row indices to draw. */
export function windowRange({ offset, viewport, rowH, count, overscan }: RangeInput): { start: number; end: number } {
  if (count <= 0 || rowH <= 0) return { start: 0, end: 0 }
  const start = clamp(Math.floor(offset / rowH) - overscan, 0, count)
  const end = clamp(Math.ceil((offset + viewport) / rowH) + overscan, start, count)
  return { start, end }
}

/** The row under content y (0 at the top of the first row). */
export function rowIndexAtOffset(y: number, rowH: number, count: number): number {
  if (count <= 0 || rowH <= 0) return 0
  return clamp(Math.floor(y / rowH), 0, count - 1)
}

/**
 * The slot a dragged row would drop into, from content y alone: the number of row
 * midpoints the pointer has passed, as insertionIndex does from measured midpoints,
 * but computed from the fixed row height so it holds over rows that are not drawn.
 * 0 is before everything and `count` after everything.
 */
export function insertionAtOffset(y: number, rowH: number, count: number): number {
  if (count <= 0 || rowH <= 0) return 0
  return clamp(Math.ceil(y / rowH - 0.5), 0, count)
}

// Held at the edge, the speed grows by this much per second of holding, up to MAX_RAMP
// times the base: 5 000 rows are 180 000 px, which a fixed step would take minutes.
const RAMP_PER_SECOND = 2.5
const MAX_RAMP = 12

export interface AutoScrollInput {
  /** Pointer y in the same coordinates as top and bottom (the viewport's). */
  y: number
  top: number
  bottom: number
  /** Depth of the zone at each edge in which scrolling starts. */
  edge: number
  /** px per frame at the very edge, before any ramp. */
  maxStep: number
  /** How long the pointer has been in the zone. */
  heldMs: number
}

/** px to scroll this frame: negative up, positive down, 0 when the pointer is clear of both edges. */
export function autoScrollStep({ y, top, bottom, edge, maxStep, heldMs }: AutoScrollInput): number {
  if (bottom <= top || edge <= 0) return 0
  let ratio = 0
  let sign = 0
  if (y < top + edge) { ratio = (top + edge - y) / edge; sign = -1 }
  else if (y > bottom - edge) { ratio = (y - (bottom - edge)) / edge; sign = 1 }
  if (sign === 0) return 0
  const ramp = Math.min(1 + (Math.max(heldMs, 0) / 1000) * RAMP_PER_SECOND, MAX_RAMP)
  // At least one px, or a pointer just inside the zone would round to a standstill.
  return sign * Math.max(1, Math.round(maxStep * Math.min(ratio, 1) * ramp))
}

export interface RevealInput {
  /** Content y of the row's top in the scroller's own coordinates. */
  rowTop: number
  rowH: number
  scrollTop: number
  viewport: number
  /** Height covered by sticky chrome at the top of the viewport (shell bar and table header). */
  topInset: number
  align?: 'nearest' | 'center'
}

/** The scrollTop that makes a row visible, or the current one when it already is. */
export function revealScrollTop({ rowTop, rowH, scrollTop, viewport, topInset, align = 'nearest' }: RevealInput): number {
  if (align === 'center') return Math.max(0, rowTop - topInset - (viewport - topInset - rowH) / 2)
  if (rowTop < scrollTop + topInset) return Math.max(0, rowTop - topInset)
  if (rowTop + rowH > scrollTop + viewport) return rowTop + rowH - viewport
  return scrollTop
}

export interface RenderPlan {
  items: { index: number; /** Whole rows of blank space to put before this one. */ gapBefore: number }[]
  /** Whole rows of blank space after the last drawn one. */
  tail: number
}

/**
 * Which rows to draw and where the blank space goes. `pinned` are rows that must stay
 * mounted wherever they are: the one holding focus, the tab stop, the one being
 * carried. Unmounting a focused row drops focus to the page, and a touch drag whose
 * target is unmounted stops receiving events.
 */
export function renderPlan(count: number, start: number, end: number, pinned: readonly number[]): RenderPlan {
  const set = new Set<number>()
  // `end` can be stale for one render after the list shrank: never draw past the data.
  for (let i = Math.max(start, 0); i < Math.min(end, count); i++) set.add(i)
  for (const p of pinned) if (Number.isInteger(p) && p >= 0 && p < count) set.add(p)
  const sorted = [...set].sort((a, b) => a - b)
  let prev = -1
  const items = sorted.map(index => {
    const gapBefore = index - prev - 1
    prev = index
    return { index, gapBefore }
  })
  return { items, tail: count - 1 - prev }
}

/** Where a carried row's target slot goes for a key, or null if the key is not a carrying key. */
export function carryTarget(key: string, to: number, total: number, page: number): number | null {
  let next: number
  switch (key) {
    case 'ArrowUp': next = to - 1; break
    case 'ArrowDown': next = to + 1; break
    case 'Home': next = 0; break
    case 'End': next = total - 1; break
    case 'PageUp': next = to - page; break
    case 'PageDown': next = to + page; break
    default: return null
  }
  return clamp(next, 0, Math.max(total - 1, 0))
}

/** How many rows one PageUp / PageDown covers: the rows that fit under the sticky chrome. */
export function pageRows(viewport: number, topInset: number, rowH: number): number {
  if (rowH <= 0) return 1
  return Math.max(1, Math.floor((viewport - topInset) / rowH))
}

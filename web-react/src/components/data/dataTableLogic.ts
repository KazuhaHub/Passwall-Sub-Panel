// Pure helpers behind DataTable, kept free of React so the paging and
// reordering rules can be tested without a DOM.

/** The page sizes every paged list in the app offers (PagedTableFooter's set). */
export const PAGE_SIZE_OPTIONS = [10, 25, 50, 100] as const
export const DEFAULT_PAGE_SIZE = 25

export function pageCount(total: number, pageSize: number): number {
  if (pageSize <= 0) return 1
  return Math.max(1, Math.ceil(total / pageSize))
}

/** A stale or malformed page number (1-based) becomes the nearest page that exists. */
export function clampPage(page: number, total: number, pageSize: number): number {
  const p = Number.isFinite(page) ? Math.floor(page) : 1
  return Math.min(Math.max(1, p), pageCount(total, pageSize))
}

/** 1-based position of the first and last row on the page; 0-0 when nothing is shown. */
export function pageRange(page: number, pageSize: number, total: number, shown: number): { from: number; to: number } {
  if (total <= 0 || shown <= 0) return { from: 0, to: 0 }
  const from = (page - 1) * pageSize + 1
  return { from, to: Math.min(total, from + shown - 1) }
}

export function sliceRows<T>(rows: readonly T[], page: number, pageSize: number): T[] {
  if (pageSize <= 0) return rows.slice()
  const start = (page - 1) * pageSize
  return rows.slice(start, start + pageSize)
}

/**
 * Rows whose text contains every whitespace-separated word of `q`, ignoring
 * case. DataTable's client mode takes rows already filtered; this is the
 * default way for a page to do it, so every list matches words the same way.
 */
export function filterRows<T>(rows: readonly T[], textOf: (row: T) => string, q: string): T[] {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return rows.slice()
  return rows.filter(r => {
    const text = textOf(r).toLowerCase()
    return words.every(w => text.includes(w))
  })
}

/**
 * Where a dragged row would be inserted, given the vertical midpoint of every
 * row in order: the number of midpoints the pointer has passed. The result
 * indexes the list INCLUDING the dragged row, so 0 is "before everything" and
 * `rows.length` is "after everything".
 */
export function insertionIndex(midpoints: readonly number[], y: number): number {
  let n = 0
  for (const m of midpoints) if (y > m) n++
  return n
}

/** Converts an insertion index (see insertionIndex) into the dragged row's final index. */
export function finalIndexFromInsertion(fromIndex: number, insertion: number): number {
  return insertion > fromIndex ? insertion - 1 : insertion
}

export interface ReorderCall {
  movedId: string
  /** The row the moved one now sits directly before; null when it goes last. */
  beforeId: string | null
}

/**
 * What a reorder reports to the page: the moved row and the row it now sits
 * before, which is enough to rewrite sort_order without sending the whole
 * list. `toIndex` is the moved row's final index and is clamped to the list.
 * Null means nothing changed, so the caller sends no request.
 */
export function reorderCall(ids: readonly string[], movedId: string, toIndex: number): ReorderCall | null {
  const from = ids.indexOf(movedId)
  if (from < 0) return null
  const to = Math.min(Math.max(0, toIndex), ids.length - 1)
  if (to === from) return null
  const rest = ids.filter(id => id !== movedId)
  return { movedId, beforeId: rest[to] ?? null }
}

/**
 * Applies a reorder report to a list the caller holds, for the optimistic
 * update before the request returns. The list order, not sort_order numbers,
 * is what a page then renumbers (in steps of 10, as the nodes page does).
 */
export function applyReorder<T>(items: readonly T[], idOf: (item: T) => string, call: ReorderCall): T[] {
  const moved = items.find(x => idOf(x) === call.movedId)
  if (!moved) return items.slice()
  const rest = items.filter(x => idOf(x) !== call.movedId)
  const at = call.beforeId === null ? rest.length : rest.findIndex(x => idOf(x) === call.beforeId)
  rest.splice(at < 0 ? rest.length : at, 0, moved)
  return rest
}

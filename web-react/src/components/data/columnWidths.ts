/**
 * Column widths an admin chose by dragging, remembered in the browser per table and column.
 *
 * Storage may be disabled, full, or throw on access (private windows, blocked site data), and the table
 * must render the same without it: every read and write is wrapped, and a failure just means the widths
 * last for this visit. A saved value is validated field by field, so one damaged entry (or a column that
 * no longer exists) never costs the rest. The key carries a version, so a changed shape is ignored.
 */
export type ColumnWidths = Record<string, number>

const VERSION = 1

/** The smallest a column can be dragged to, in px: room for an icon button, a short label or a number. */
export const MIN_COLUMN_WIDTH = 64
/** An absurd stored value (a typo, another screen's size) must not make the table a mile wide. */
export const MAX_COLUMN_WIDTH = 2000

export function columnWidthsKey(tableId: string): string {
  return `psp.colWidths.v${VERSION}.${tableId}`
}

/** Stored text -> valid widths: finite numbers within the allowed range only. */
export function parseColumnWidths(raw: string | null): ColumnWidths {
  if (!raw) return {}
  let data: unknown
  try { data = JSON.parse(raw) } catch { return {} }
  if (typeof data !== 'object' || data === null || (data as { v?: unknown }).v !== VERSION) return {}
  const w = (data as { w?: unknown }).w
  if (typeof w !== 'object' || w === null || Array.isArray(w)) return {}
  const out: ColumnWidths = {}
  for (const [id, v] of Object.entries(w)) {
    if (typeof v === 'number' && Number.isFinite(v) && v >= MIN_COLUMN_WIDTH && v <= MAX_COLUMN_WIDTH) out[id] = Math.round(v)
  }
  return out
}

export function loadColumnWidths(tableId: string | undefined): ColumnWidths {
  if (!tableId) return {}
  try { return parseColumnWidths(localStorage.getItem(columnWidthsKey(tableId))) } catch { return {} }
}

/** An empty set removes the entry, so "reset" leaves nothing behind. */
export function saveColumnWidths(tableId: string | undefined, widths: ColumnWidths): void {
  if (!tableId) return
  try {
    if (Object.keys(widths).length === 0) localStorage.removeItem(columnWidthsKey(tableId))
    else localStorage.setItem(columnWidthsKey(tableId), JSON.stringify({ v: VERSION, w: widths }))
  } catch { /* storage may be disabled: the widths then last for this visit only */ }
}

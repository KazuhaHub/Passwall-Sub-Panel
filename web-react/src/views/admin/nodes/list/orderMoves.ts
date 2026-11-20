import type { OrderRef, OrderRow } from '@/api/nodes'
import { applyReorder, type ReorderCall } from '@/components/data/dataTableLogic'

// Pure rules of reordering the global subscription order (design section 3). The list is always the COMPLETE,
// unfiltered one: the server takes the whole arrangement back, and a drop between two visible rows would be
// ambiguous when hidden rows sit between them.

/** A row's id in the table and in `?line=`: separators and lines come from independent id sequences. */
export function rowId(row: OrderRow): string {
  return row.kind === 'separator' ? `sep:${row.id}` : `${row.node_id}.${row.line_id}`
}

export function refOf(row: OrderRow): OrderRef {
  return row.kind === 'separator' ? { kind: 'separator', id: row.id ?? 0 } : { kind: 'line', node_id: row.node_id ?? 0, line_id: row.line_id ?? '' }
}

/** The PUT body's items: the whole list, top to bottom. */
export function itemsOf(rows: readonly OrderRow[]): OrderRef[] {
  return rows.map(refOf)
}

/**
 * What the person meant, kept so it can be repeated on a newer order: the rows moved (several when "also move
 * this node's other lines" is on) and the row they were put after (null: the top). It is recorded by id, never
 * by index, because the list may change under it.
 */
export interface MoveIntent {
  moved: string[]
  after: string | null
}

/** Places `moved` (in their current relative order) right after `after`, or first when null. Null if a row is gone. */
export function applyIntent(rows: readonly OrderRow[], intent: MoveIntent): OrderRow[] | null {
  const byId = new Map(rows.map(r => [rowId(r), r]))
  const movedRows = rows.filter(r => intent.moved.includes(rowId(r)))
  if (movedRows.length !== intent.moved.length) return null
  if (intent.after !== null && (!byId.has(intent.after) || intent.moved.includes(intent.after))) return null
  const rest = rows.filter(r => !intent.moved.includes(rowId(r)))
  const at = intent.after === null ? 0 : rest.findIndex(r => rowId(r) === intent.after) + 1
  return [...rest.slice(0, at), ...movedRows, ...rest.slice(at)]
}

/** The intent behind a drag report (movedId, beforeId): it lands after the row that now precedes it. */
export function intentOfDrag(rows: readonly OrderRow[], call: ReorderCall): MoveIntent | null {
  const next = applyReorder(rows, rowId, call)
  const i = next.findIndex(r => rowId(r) === call.movedId)
  if (i < 0) return null
  return { moved: [call.movedId], after: i === 0 ? null : rowId(next[i - 1]) }
}

/** Move to...: the rows that go with a direct row when the box is ticked: the node's other lines, in list order. */
export function otherLinesOf(rows: readonly OrderRow[], direct: OrderRow): string[] {
  return rows.filter(r => r.kind === 'line' && r.node_id === direct.node_id && rowId(r) !== rowId(direct)).map(rowId)
}

/** True when the move changes nothing (a row put after the row it already follows). */
export function isNoop(rows: readonly OrderRow[], intent: MoveIntent): boolean {
  const next = applyIntent(rows, intent)
  return next === null || next.every((r, i) => rowId(r) === rowId(rows[i]))
}

/** What changed between the list the person saw and the current one (design 3.4). */
export interface OrderDiff {
  added: OrderRow[]
  removed: OrderRow[]
  /** Rows present in both whose place relative to the others changed. */
  moved: number
}

/** Length of the longest increasing subsequence: the rows that kept their relative order. */
function lisLength(seq: number[]): number {
  const tails: number[] = []
  for (const v of seq) {
    let lo = 0
    let hi = tails.length
    while (lo < hi) { const mid = (lo + hi) >> 1; if (tails[mid] < v) lo = mid + 1; else hi = mid }
    tails[lo] = v
  }
  return tails.length
}

export function diffOrders(seen: readonly OrderRow[], current: readonly OrderRow[]): OrderDiff {
  const seenIds = new Set(seen.map(rowId))
  const currentIds = new Set(current.map(rowId))
  const added = current.filter(r => !seenIds.has(rowId(r)))
  const removed = seen.filter(r => !currentIds.has(rowId(r)))
  const seenIndex = new Map(seen.map((r, i) => [rowId(r), i]))
  const common = current.filter(r => seenIndex.has(rowId(r))).map(r => seenIndex.get(rowId(r)) as number)
  return { added, removed, moved: common.length - lisLength(common) }
}

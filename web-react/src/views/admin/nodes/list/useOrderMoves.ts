import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { OrderStaleError, putNodeOrder, type NodeOrder, type OrderRow } from '@/api/nodes'
import { describeError } from '@/components/feedback/ErrorState'
import { pushToast } from '@/components/feedback/toasts'
import type { ReorderCall } from '@/components/data/dataTableLogic'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { useNodesT } from '../shared/useNodesT'
import { applyIntent, intentOfDrag, isNoop, itemsOf, rowId, type MoveIntent } from './orderMoves'

/** How long "撤销" stays on offer after a move (design 3.3). */
export const UNDO_MS = 10_000
/** How long a moved row keeps its highlight. */
const HIGHLIGHT_MS = 1500

/** What the 409 dialog needs: the person's intent, the list they saw and the list as it is now. */
export interface StaleState {
  intent: MoveIntent
  seen: OrderRow[]
  current: NodeOrder
  /** The refused write was an Undo: there is no move to repeat, only the latest order to keep. */
  undo?: boolean
}

/** What Undo puts back: the EXACT previous arrangement (a move of several rows from different places cannot be undone as one move), and the intent used only to explain a 409. */
interface Undo { restore: OrderRow[]; intent: MoveIntent }

/**
 * Optimistic reordering of the global order (design section 3.3 and 3.4).
 *
 * One write at a time: while a PUT is in flight `saving` is true and the handles say so, because queueing moves
 * against a version the first one is about to change invites the stale case. The cache is the source of truth
 * for the rows, so a rollback or a 409 is just another write to it.
 */
export function useOrderMoves() {
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const t = useNodesT()
  const [saving, setSaving] = useState(false)
  const [stale, setStale] = useState<StaleState | null>(null)
  const [highlighted, setHighlighted] = useState<string[]>([])
  const [undoable, setUndoable] = useState<Undo | null>(null)
  const timers = useRef<ReturnType<typeof setTimeout>[]>([])
  const savingRef = useRef(false)
  useEffect(() => () => timers.current.forEach(clearTimeout), [])

  const read = useCallback(() => queryClient.getQueryData<NodeOrder>(nodeKeys.order(scope)), [queryClient, scope])
  const write = useCallback((o: NodeOrder) => queryClient.setQueryData<NodeOrder>(nodeKeys.order(scope), o), [queryClient, scope])
  const later = (fn: () => void, ms: number) => { timers.current.push(setTimeout(fn, ms)) }

  const flash = (ids: string[]) => {
    setHighlighted(ids)
    later(() => setHighlighted(cur => (cur === ids ? [] : cur)), HIGHLIGHT_MS)
  }
  const offerUndo = (u: Undo) => {
    setUndoable(u)
    later(() => setUndoable(cur => (cur === u ? null : cur)), UNDO_MS)
  }

  /** The previous arrangement as rows of the CURRENT order where they still exist; a set that differs is left for the server to refuse (409). */
  const restoreOf = (current: readonly OrderRow[], previous: readonly OrderRow[]): OrderRow[] => {
    const byId = new Map(current.map(r => [rowId(r), r]))
    return previous.map(r => byId.get(rowId(r)) ?? r)
  }

  /** Applies `intent` to `base` and sends it. `undo` says this move is itself an undo (no second undo is offered). */
  const send = useCallback(async (base: NodeOrder, intent: MoveIntent, undo?: Undo) => {
    const next = undo ? restoreOf(base.rows, undo.restore) : applyIntent(base.rows, intent)
    if (!next || (!undo && isNoop(base.rows, intent))) return
    write({ order_version: base.order_version, rows: next })
    flash(intent.moved)
    savingRef.current = true
    setSaving(true)
    try {
      const version = await putNodeOrder(base.order_version, itemsOf(next))
      write({ order_version: version, rows: next })
      // Positions changed on the server; the rows already match, so this does not move anything.
      void queryClient.invalidateQueries({ queryKey: nodeKeys.order(scope) })
      if (undo) {
        setUndoable(null)
        pushToast({ message: t('nodes.order.undone'), severity: 'success' })
      } else {
        const back: Undo = { restore: base.rows, intent: { moved: intent.moved, after: null } }
        offerUndo(back)
        pushToast({
          message: t('nodes.order.saved'), severity: 'success',
          action: { label: t('nodes.toast.undo'), onClick: () => runUndo(back) },
        })
      }
    } catch (err) {
      if (err instanceof OrderStaleError) {
        write(err.current)
        setStale({ intent, seen: base.rows, current: err.current, undo: !!undo })
      } else {
        write(base)
        pushToast({ message: t('nodes.order.failed', { reason: describeError(err) ?? '' }), severity: 'error' })
      }
    } finally {
      savingRef.current = false
      setSaving(false)
    }
  // `flash`, `offerUndo` and `runUndo` only use refs, setters and this callback.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryClient, scope, read, write, t])

  /** One write at a time, for the toast's Undo and the toolbar's alike. */
  const runUndo = (u: Undo) => {
    const cur = read()
    if (cur && !savingRef.current) void send(cur, u.intent, u)
  }

  const move = useCallback((intent: MoveIntent) => {
    const base = read()
    if (!base || savingRef.current) return
    setStale(null)
    void send(base, intent)
  }, [read, send])

  const drag = useCallback((call: ReorderCall) => {
    const base = read()
    const intent = base && intentOfDrag(base.rows, call)
    if (intent) move(intent)
  }, [read, move])

  /** The 409 dialog's answers: repeat the move on the latest order, or keep the latest order as it is. */
  const resolveStale = useCallback((answer: 'reapply' | 'keep') => {
    const s = stale
    setStale(null)
    if (answer === 'keep' || !s) return
    void send(s.current, s.intent)
  }, [stale, send])

  const undo = () => { if (undoable) runUndo(undoable) }

  return { saving, stale, highlighted, undoable, move, drag, resolveStale, undo }
}

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
}

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
  const [undoable, setUndoable] = useState<MoveIntent | null>(null)
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
  const offerUndo = (intent: MoveIntent) => {
    setUndoable(intent)
    later(() => setUndoable(cur => (cur === intent ? null : cur)), UNDO_MS)
  }

  /** The intent that puts the moved rows back where they were: after the row that preceded them. */
  const inverse = (before: readonly OrderRow[], moved: string[]): MoveIntent => {
    const at = before.findIndex(r => rowId(r) === moved[0])
    let after: string | null = null
    for (let i = at - 1; i >= 0; i--) { if (!moved.includes(rowId(before[i]))) { after = rowId(before[i]); break } }
    return { moved, after }
  }

  /** Applies `intent` to `base` and sends it. `undo` says this move is itself an undo (no second undo is offered). */
  const send = useCallback(async (base: NodeOrder, intent: MoveIntent, undo = false) => {
    const next = applyIntent(base.rows, intent)
    if (!next || isNoop(base.rows, intent)) return
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
        const back = inverse(base.rows, intent.moved)
        offerUndo(back)
        pushToast({
          message: t('nodes.order.saved'), severity: 'success',
          action: { label: t('nodes.toast.undo'), onClick: () => { const cur = read(); if (cur) void send(cur, back, true) } },
        })
      }
    } catch (err) {
      if (err instanceof OrderStaleError) {
        write(err.current)
        setStale({ intent, seen: base.rows, current: err.current })
      } else {
        write(base)
        pushToast({ message: t('nodes.order.failed', { reason: describeError(err) ?? '' }), severity: 'error' })
      }
    } finally {
      savingRef.current = false
      setSaving(false)
    }
  // `inverse`, `flash` and `offerUndo` only use refs and setters.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryClient, scope, read, write, t])

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

  const undo = useCallback(() => {
    const cur = read()
    if (cur && undoable && !savingRef.current) void send(cur, undoable, true)
  }, [read, undoable, send])

  return { saving, stale, highlighted, undoable, move, drag, resolveStale, undo }
}

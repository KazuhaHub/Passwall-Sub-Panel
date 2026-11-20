import { useMemo } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { useNodesT } from '../shared/useNodesT'
import { describeRow } from './MoveToDialog'
import { applyIntent, diffOrders, rowId } from './orderMoves'
import type { StaleState } from './useOrderMoves'

const MAX_NAMED = 5

/**
 * The 409 (design 3.4). The person's move is never dropped silently: the dialog says what changed under them
 * (up to five named rows, then counts) and offers to repeat the same move on the latest order, or to keep the
 * latest order. When a row the move names is gone it says which, and only "keep" remains.
 */
export function StaleOrderDialog({ stale, onAnswer }: { stale: StaleState; onAnswer: (a: 'reapply' | 'keep') => void }) {
  const t = useNodesT()
  const diff = useMemo(() => diffOrders(stale.seen, stale.current.rows), [stale])
  const goneId = useMemo(() => {
    if (applyIntent(stale.current.rows, stale.intent)) return null
    const have = new Set(stale.current.rows.map(rowId))
    return [...stale.intent.moved, ...(stale.intent.after ? [stale.intent.after] : [])].find(id => !have.has(id)) ?? null
  }, [stale])
  const nameOf = (id: string) => { const r = stale.seen.find(x => rowId(x) === id); return r ? describeRow(r) : id }
  const lines = [
    ...diff.added.map(r => t('nodes.order.stale_added', { name: describeRow(r) })),
    ...diff.removed.map(r => t('nodes.order.stale_removed', { name: describeRow(r) })),
  ]
  const named = lines.slice(0, MAX_NAMED)
  const more = lines.length - named.length
  const moved = diff.moved + Math.max(0, more)

  return (
    <Dialog open onOpenChange={open => { if (!open) onAnswer('keep') }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('nodes.order.stale_title')}</DialogTitle>
          <DialogDescription>{t('nodes.order.stale_body')}</DialogDescription>
        </DialogHeader>
        <ul className="flex flex-col gap-1 text-sm" data-testid="stale-diff">
          {named.map(l => <li key={l}>{l}</li>)}
          {moved > 0 && <li>{t('nodes.order.stale_moved', { count: moved })}</li>}
        </ul>
        {goneId && <p className="text-sm text-destructive" role="alert">{t('nodes.order.stale_gone', { name: nameOf(goneId) })}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={() => onAnswer('keep')}>{t('nodes.order.stale_keep')}</Button>
          {!goneId && <Button onClick={() => onAnswer('reapply')}>{t('nodes.order.stale_reapply')}</Button>}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

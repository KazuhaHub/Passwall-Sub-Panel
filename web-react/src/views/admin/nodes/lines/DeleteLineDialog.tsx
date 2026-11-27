import { useMemo, useRef, useState } from 'react'
import type { OrderRow } from '@/api/nodes'
import { pushToast } from '@/components/feedback/toasts'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import type { Node } from '@/api/types'
import type { TunnelPlanView } from '@/utils/tunnelConfirm'
import { pathOf, type PathLabels } from '../shared/pathOf'
import { useNodesT } from '../shared/useNodesT'
import { PlanLines } from './PlanDialog'
import { saveLineChange } from './lineSave'
import { useApplyNode } from './useNodeDetail'

/** The line a delete is about: its row (for the picture and the facts) and the node's server-side state, when known. */
export interface DeleteTarget {
  row: OrderRow
  node?: Node
  exitName?: string
}

const NO_COLORS = new Map()

/**
 * Deleting one line (design 5.1): the line drawn as it is, what happens to it in words, one button with the verb.
 * Focus starts on Cancel: a stray Enter typed while the dialog was coming up must not delete.
 *
 * There is no impact endpoint for a line; its own row has everything. A TUNNEL line is different: the server
 * refuses the save without confirmation and answers with the plan of the listeners it would close. That plan is
 * shown HERE, in this dialog, in place of the generic sentence, and the same button then confirms it: one dialog,
 * not two. Disabling is offered instead, because it keeps the line's id and place.
 */
export function DeleteLineDialog({ target, onClose, onDisable }: { target: DeleteTarget; onClose: () => void; onDisable: () => void }) {
  const t = useNodesT()
  const apply = useApplyNode()
  const { row } = target
  const [busy, setBusy] = useState(false)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const [plan, setPlan] = useState<{ view: TunnelPlanView; resolve: (ok: boolean) => void } | null>(null)
  const labels = useMemo<PathLabels>(() => ({ external: t('nodes.type.external'), link: t('nodes.type.link'), deleted: t('nodes.withheld.entry_missing') }), [t])
  const { nodes, edges } = pathOf(row, { colors: NO_COLORS, labels, exit: target.exitName ? { name: target.exitName } : null, ends: true, node: target.node })
  const tunnel = row.line_type === 'tunnel'

  async function remove() {
    // Confirming a plan that is on screen answers the question the save is waiting on.
    if (plan) { const p = plan; setPlan(null); p.resolve(true); return }
    setBusy(true)
    try {
      const { saved } = await saveLineChange(row.node_id ?? 0, { kind: 'remove', lineId: row.line_id ?? '' }, { present: view => new Promise<boolean>(resolve => setPlan({ view, resolve })) })
      apply(saved)
      pushToast({ message: t('nodes.line.toast.deleted'), severity: 'success' })
      onClose()
    } catch {
      // The request layer already surfaced a failure; a declined plan changed nothing. Either way the dialog is done.
      onClose()
    } finally { setBusy(false) }
  }
  const cancel = () => { if (plan) { const p = plan; setPlan(null); p.resolve(false) } else if (!busy) onClose() }

  return (
    <Dialog open onOpenChange={open => { if (!open) cancel() }}>
      <DialogContent className="sm:max-w-lg" initialFocus={cancelRef}>
        <DialogHeader>
          <DialogTitle>{t('nodes.line.delete.title', { name: row.name })}</DialogTitle>
          <DialogDescription className="sr-only">{t('nodes.line.delete.body')}</DialogDescription>
        </DialogHeader>
        <PathDiagram nodes={nodes} edges={edges} />
        <div className="flex flex-col gap-2 text-sm">
          <p>{t('nodes.line.delete.body')}</p>
          {plan
            ? <PlanLines lines={plan.view.lines} />
            : tunnel && row.entry && <p data-testid="closes-listener">{t('nodes.line.delete.closes_listener', { server: row.entry.panel_name, port: row.port ?? 0 })}</p>}
          <p className="text-muted-foreground">{t('nodes.line.delete.refs')}</p>
          {!plan && (
            <p className="flex flex-wrap items-center gap-x-1">
              <span>{t('nodes.line.delete.prefer_disable')}</span>
              <Button variant="link" size="sm" className="h-auto px-0" onClick={onDisable}>{t('nodes.line.delete.disable_instead')}</Button>
            </p>
          )}
        </div>
        <DialogFooter>
          <Button ref={cancelRef} variant="outline" onClick={cancel}>{t('common:actions.cancel')}</Button>
          <Button variant="destructive" disabled={busy && !plan} onClick={() => void remove()}>{t('nodes.action.delete_line')}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

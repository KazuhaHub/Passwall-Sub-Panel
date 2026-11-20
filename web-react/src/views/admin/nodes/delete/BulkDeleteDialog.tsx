import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { DeleteImpactLine } from '@/api/nodes'
import { AsyncButton } from '@/components/common/AsyncButton'
import { ErrorState, describeError } from '@/components/feedback/ErrorState'
import { allSettledLimited } from '@/utils/promises'
import { useNodesT } from '../shared/useNodesT'
import { useBulkImpact, type BulkEntry } from './bulkImpact'
import { ImpactBody } from './ImpactBody'
import { ImpactDialogFrame } from './ImpactDialogFrame'
import { planCovered, totalLines } from './impactModel'
import { runRemoval } from './runRemoval'

/**
 * Deleting several nodes at once (design 5.3). It reads every node's impact, then asks ONE question: how many
 * nodes and how many lines go, with each node's lines drawn under a fold. Nodes that cannot be deleted (routes
 * point at them) or whose impact could not be read are listed apart and left out of the action, so the button
 * only ever covers what the admin was shown.
 */
export function BulkDeleteDialog({ nodeIds, nameOf, entryOf, serverName, onClose, onDone }: {
  nodeIds: readonly number[]
  nameOf: (nodeId: number) => string
  entryOf: (line: DeleteImpactLine) => string | undefined
  serverName: (panelId: number) => string
  onClose: () => void
  onDone: (result: { ok: number; failed: number }) => void
}) {
  const t = useNodesT()
  const { t: tc } = useTranslation('common')
  const q = useBulkImpact(nodeIds)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const [busy, setBusy] = useState(false)
  // Made on mount (see NodeDeleteDialog): a controller made with the instance is already aborted by development's second mount.
  const abort = useRef<AbortController | null>(null)
  useEffect(() => { const c = new AbortController(); abort.current = c; return () => c.abort() }, [])

  const entries = q.data ?? []
  const ready = entries.filter((e): e is Extract<BulkEntry, { kind: 'ready' }> => e.kind === 'ready')
  const skipped = entries.filter(e => e.kind !== 'ready')
  const lines = totalLines(ready.map(e => e.impact))

  const send = async () => {
    setBusy(true)
    // A plan that lists more than the node's own dialog section showed is declined: nobody is here to read it per
    // node, and a declined node is counted as failed, so it is reported rather than deleted unseen.
    const results = await allSettledLimited(ready, e => runRemoval('delete', e.id, async plan => planCovered(plan, e.impact.tunnels), abort.current?.signal), 4)
    const failed = results.filter(r => r.status === 'rejected').length
    if (!abort.current?.signal.aborted) onDone({ ok: ready.length - failed, failed })
  }

  const title = t('nodes.impact.bulk_title', { count: nodeIds.length })
  if (q.isError) {
    return (
      <ImpactDialogFrame title={title} cancelRef={cancelRef} onClose={onClose}
        actions={<AsyncButton variant="outline" onClick={() => q.refetch()}>{tc('actions.retry')}</AsyncButton>}>
        <ErrorState compact title={t('nodes.impact.failed_title')} reason={describeError(q.error)} hint={t('nodes.impact.failed_hint')} />
      </ImpactDialogFrame>
    )
  }
  if (q.isPending) {
    return <ImpactDialogFrame title={title} cancelRef={cancelRef} onClose={onClose}><p role="status" className="text-muted-foreground">{t('nodes.impact.loading')}</p></ImpactDialogFrame>
  }
  return (
    <ImpactDialogFrame title={title} cancelRef={cancelRef} onClose={() => { if (!busy) onClose() }} cancelDisabled={busy} wide
      actions={ready.length > 0 ? <AsyncButton variant="destructive" onClick={send}>{ready.length === 1 ? t('nodes.impact.confirm', { count: lines }) : t('nodes.impact.bulk_confirm', { nodes: ready.length, count: lines })}</AsyncButton> : undefined}>
      {ready.length === 0 && <p>{t('nodes.impact.bulk_none')}</p>}
      <div className="flex flex-col gap-2" data-testid="bulk-ready">
        {ready.map(e => (
          <details key={e.id} className="rounded-lg border px-3 py-2">
            <summary className="cursor-pointer font-medium">{t('nodes.impact.bulk_node_lines', { name: nameOf(e.id), count: e.impact.lines.length })}</summary>
            <div className="mt-2 flex flex-col gap-3"><ImpactBody impact={e.impact} entryOf={entryOf} serverName={serverName} mode="delete" /></div>
          </details>
        ))}
      </div>
      {skipped.length > 0 && (
        <div>
          <p className="mb-1 font-medium">{t('nodes.impact.bulk_skipped', { count: skipped.length })}</p>
          <ul className="list-disc ps-5 text-muted-foreground" data-testid="bulk-skipped">
            {skipped.map(e => (
              <li key={e.id}>
                {e.kind === 'blocked'
                  ? t('nodes.impact.bulk_blocked_item', { name: nameOf(e.id) })
                  : t('nodes.impact.bulk_unread_item', { name: nameOf(e.id), reason: e.reason })}
              </li>
            ))}
          </ul>
        </div>
      )}
    </ImpactDialogFrame>
  )
}

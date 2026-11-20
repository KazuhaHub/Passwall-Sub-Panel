import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { setNodeEnabled } from '@/api/nodes'
import type { Server } from '@/api/servers'
import { Button } from '@/components/ui/button'
import { pushToast } from '@/components/feedback/toasts'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { allSettledLimited } from '@/utils/promises'
import { useNodesT } from '../shared/useNodesT'

/**
 * The bulk bar's node actions (design 2.5). They act on the distinct landing NODES of the selected rows, filtered
 * by what each node's server allows, and the labels say "节点" with a count because a direct row's enable is the
 * whole node. Line-level bulk actions arrive with N2; delete opens the impact dialog (delete/).
 */
export function BulkNodeActions({ nodeIds, admin, serverOf, clear, onDelete }: {
  nodeIds: number[]
  admin: boolean
  /** The landing server of a node, for its capabilities. */
  serverOf: (nodeId: number) => Server | undefined
  clear: () => void
  /** Opens the bulk delete dialog for these nodes; it reads what each takes with it before asking. */
  onDelete: (nodeIds: number[], clear: () => void) => void
}) {
  const t = useNodesT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState<'enable' | 'disable' | ''>('')
  const can = (cap: string) => nodeIds.filter(id => serverOf(id)?.capabilities?.includes(cap as never))
  const enableable = can('inbound.enable')
  const deletable = can('inbound.delete')
  const refresh = () => queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) })

  const report = (ok: number, failed: number, key: string) => {
    if (failed > 0) pushToast({ message: t('nodes.toast.batch_partial', { ok, fail: failed }), severity: 'warning' })
    else pushToast({ message: t(key, { count: ok }), severity: 'success' })
  }
  const setEnabled = async (enable: boolean) => {
    setBusy(enable ? 'enable' : 'disable')
    try {
      const results = await allSettledLimited(enableable, id => setNodeEnabled(id, enable))
      const failed = results.filter(r => r.status === 'rejected').length
      report(enableable.length - failed, failed, enable ? 'nodes.toast.batch_enabled' : 'nodes.toast.batch_disabled')
      await refresh()
    } finally { setBusy('') }
  }
  return (
    <>
      <Button variant="outline" size="sm" disabled={busy !== '' || enableable.length === 0} onClick={() => void setEnabled(true)}>{t('nodes.bulk.enable_nodes', { count: enableable.length })}</Button>
      <Button variant="outline" size="sm" disabled={busy !== '' || enableable.length === 0} onClick={() => void setEnabled(false)}>{t('nodes.bulk.disable_nodes', { count: enableable.length })}</Button>
      {admin && <Button variant="destructive" size="sm" disabled={busy !== '' || deletable.length === 0} onClick={() => onDelete(deletable, clear)}>{t('nodes.bulk.delete_nodes', { count: deletable.length })}</Button>}
    </>
  )
}

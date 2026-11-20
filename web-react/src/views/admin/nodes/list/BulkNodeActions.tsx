import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
<<<<<<< HEAD
import { setNodeEnabled } from '@/api/nodes'
import type { Server } from '@/api/servers'
import { Button } from '@/components/ui/button'
=======
import { deleteNode, setNodeEnabled } from '@/api/nodes'
import type { Server } from '@/api/servers'
import { Button } from '@/components/ui/button'
import { confirm } from '@/components/ConfirmHost'
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
import { pushToast } from '@/components/feedback/toasts'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { allSettledLimited } from '@/utils/promises'
import { useNodesT } from '../shared/useNodesT'

/**
 * The bulk bar's node actions (design 2.5). They act on the distinct landing NODES of the selected rows, filtered
 * by what each node's server allows, and the labels say "节点" with a count because a direct row's enable is the
<<<<<<< HEAD
 * whole node. Line-level bulk actions arrive with N2; delete opens the impact dialog (delete/).
 */
export function BulkNodeActions({ nodeIds, admin, serverOf, clear, onDelete }: {
=======
 * whole node. Line-level bulk actions arrive with N2; delete goes through the old confirmation until N4.
 */
export function BulkNodeActions({ nodeIds, admin, serverOf, clear }: {
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
  nodeIds: number[]
  admin: boolean
  /** The landing server of a node, for its capabilities. */
  serverOf: (nodeId: number) => Server | undefined
  clear: () => void
<<<<<<< HEAD
  /** Opens the bulk delete dialog for these nodes; it reads what each takes with it before asking. */
  onDelete: (nodeIds: number[], clear: () => void) => void
=======
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
}) {
  const t = useNodesT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
<<<<<<< HEAD
  const [busy, setBusy] = useState<'enable' | 'disable' | ''>('')
=======
  const [busy, setBusy] = useState<'enable' | 'disable' | 'delete' | ''>('')
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
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
<<<<<<< HEAD
=======
  const remove = async () => {
    const ok = await confirm({
      title: t('nodes.confirm.batch_delete_title'), message: t('nodes.confirm.batch_delete_message', { count: deletable.length }),
      destructive: true, confirmText: t('nodes.action.delete'),
    })
    if (!ok) return
    setBusy('delete')
    try {
      const results = await allSettledLimited(deletable, id => deleteNode(id))
      const failed = results.filter(r => r.status === 'rejected').length
      report(deletable.length - failed, failed, 'nodes.toast.batch_deleted')
      clear()
      await refresh()
    } finally { setBusy('') }
  }

>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
  return (
    <>
      <Button variant="outline" size="sm" disabled={busy !== '' || enableable.length === 0} onClick={() => void setEnabled(true)}>{t('nodes.bulk.enable_nodes', { count: enableable.length })}</Button>
      <Button variant="outline" size="sm" disabled={busy !== '' || enableable.length === 0} onClick={() => void setEnabled(false)}>{t('nodes.bulk.disable_nodes', { count: enableable.length })}</Button>
<<<<<<< HEAD
      {admin && <Button variant="destructive" size="sm" disabled={busy !== '' || deletable.length === 0} onClick={() => onDelete(deletable, clear)}>{t('nodes.bulk.delete_nodes', { count: deletable.length })}</Button>}
=======
      {admin && <Button variant="destructive" size="sm" disabled={busy !== '' || deletable.length === 0} onClick={() => void remove()}>{t('nodes.bulk.delete_nodes', { count: deletable.length })}</Button>}
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
    </>
  )
}

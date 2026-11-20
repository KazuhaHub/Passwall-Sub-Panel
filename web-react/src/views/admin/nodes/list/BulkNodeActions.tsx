import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { deleteNode, setNodeEnabled } from '@/api/nodes'
import type { Server } from '@/api/servers'
import { Button } from '@/components/ui/button'
import { confirm } from '@/components/ConfirmHost'
import { pushToast } from '@/components/feedback/toasts'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { allSettledLimited } from '@/utils/promises'
import { useNodesT } from '../shared/useNodesT'

/**
 * The bulk bar's node actions (design 2.5). They act on the distinct landing NODES of the selected rows, filtered
 * by what each node's server allows, and the labels say "节点" with a count because a direct row's enable is the
 * whole node. The line actions (enable or disable the selected relay lines) are N2's; delete goes through the old confirmation until N4.
 */
export function BulkNodeActions({ nodeIds, lineRefs = [], toggleLines, admin, serverOf, clear }: {
  nodeIds: number[]
  /** The relay lines among the selected rows. Enabling or disabling them writes each node's lines once. */
  lineRefs?: { nodeId: number; lineId: string }[]
  toggleLines?: (nodeId: number, lineIds: readonly string[], enabled: boolean, opts?: { quiet?: boolean }) => Promise<boolean>
  admin: boolean
  /** The landing server of a node, for its capabilities. */
  serverOf: (nodeId: number) => Server | undefined
  clear: () => void
}) {
  const t = useNodesT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState<'enable' | 'disable' | 'delete' | 'enable_lines' | 'disable_lines' | ''>('')
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
  // One write per landing (the endpoint replaces a node's lines as a whole); the toast says how many lines, not how many writes.
  const setLinesEnabled = async (enable: boolean) => {
    if (!toggleLines) return
    setBusy(enable ? 'enable_lines' : 'disable_lines')
    try {
      const byNode = new Map<number, string[]>()
      for (const r of lineRefs) byNode.set(r.nodeId, [...(byNode.get(r.nodeId) ?? []), r.lineId])
      const results = await allSettledLimited([...byNode], ([id, lines]) => toggleLines(id, lines, enable, { quiet: true }).then(ok => { if (!ok) throw new Error('failed') }))
      const failed = results.filter(r => r.status === 'rejected').length
      if (failed === results.length) pushToast({ message: t('nodes.toast.batch_partial', { ok: 0, fail: lineRefs.length }), severity: 'warning' })
      else if (failed > 0) pushToast({ message: t('nodes.toast.batch_partial', { ok: results.length - failed, fail: failed }), severity: 'warning' })
      else pushToast({ message: t(enable ? 'nodes.line.toast.batch_enabled' : 'nodes.line.toast.batch_disabled', { count: lineRefs.length }), severity: 'success' })
      await refresh()
    } finally { setBusy('') }
  }
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

  return (
    <>
      <Button variant="outline" size="sm" disabled={busy !== '' || enableable.length === 0} onClick={() => void setEnabled(true)}>{t('nodes.bulk.enable_nodes', { count: enableable.length })}</Button>
      <Button variant="outline" size="sm" disabled={busy !== '' || enableable.length === 0} onClick={() => void setEnabled(false)}>{t('nodes.bulk.disable_nodes', { count: enableable.length })}</Button>
      {admin && toggleLines && lineRefs.length > 0 && (
        <>
          <Button variant="outline" size="sm" disabled={busy !== ''} onClick={() => void setLinesEnabled(true)}>{t('nodes.line.bulk.enable', { count: lineRefs.length })}</Button>
          <Button variant="outline" size="sm" disabled={busy !== ''} onClick={() => void setLinesEnabled(false)}>{t('nodes.line.bulk.disable', { count: lineRefs.length })}</Button>
        </>
      )}
      {admin && <Button variant="destructive" size="sm" disabled={busy !== '' || deletable.length === 0} onClick={() => void remove()}>{t('nodes.bulk.delete_nodes', { count: deletable.length })}</Button>}
    </>
  )
}

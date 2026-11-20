import { useCallback, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { setNodeEnabled, type OrderRow } from '@/api/nodes'
import type { Server } from '@/api/servers'
import { pushToast } from '@/components/feedback/toasts'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { useNodesT } from '../shared/useNodesT'
import { BulkDeleteDialog } from './BulkDeleteDialog'
import { NodeDeleteDialog, type NodeDeleteTarget } from './NodeDeleteDialog'
import { entryLookup, type DeleteMode } from './impactModel'

type Open =
  | { kind: 'node'; target: NodeDeleteTarget; canDisable: boolean }
  | { kind: 'bulk'; ids: number[]; clear: () => void }

/**
 * Where the nodes page asks "what does this delete take with it". It owns the one open dialog (a node, or a
 * selection), reads the names it needs from the list's own data, and refreshes the list when the server has
 * accepted the request. The row menu and the bulk bar only call `deleteNode`, `removeFromPanel` and `deleteMany`.
 */
export function useNodeDeletion({ rows, serverMap }: {
  /** The complete order: entry servers of the impact lines are named from it (design gap G12). */
  rows: readonly OrderRow[]
  serverMap: ReadonlyMap<number, Server>
}) {
  const t = useNodesT()
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState<Open | null>(null)
  const close = () => setOpen(null)
  const refresh = () => queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) })

  const ask = useCallback((row: OrderRow, mode: DeleteMode) => {
    const caps = serverMap.get(row.landing?.panel_id ?? 0)?.capabilities ?? []
    setOpen({
      kind: 'node', target: { nodeId: row.node_id ?? 0, name: row.landing?.name ?? row.name, mode },
      // "Disable instead" is only on offer for a node that is on and whose server allows switching it.
      canDisable: row.landing?.enabled !== false && caps.includes('inbound.enable'),
    })
  }, [serverMap])
  const deleteNode = useCallback((row: OrderRow) => ask(row, 'delete'), [ask])
  const removeFromPanel = useCallback((row: OrderRow) => ask(row, 'detach'), [ask])
  const deleteMany = useCallback((ids: number[], clear: () => void) => setOpen({ kind: 'bulk', ids, clear }), [])

  const entryOf = entryLookup(rows)
  const serverName = (id: number) => serverMap.get(id)?.name ?? `#${id}`
  const nameOf = (nodeId: number) => rows.find(r => r.kind === 'line' && r.node_id === nodeId)?.landing?.name ?? `#${nodeId}`

  let element = null
  if (open?.kind === 'node') {
    const { target } = open
    element = (
      <NodeDeleteDialog
        key={`${target.mode}.${target.nodeId}`} target={target} entryOf={entryOf} serverName={serverName}
        onDisable={open.canDisable ? async () => {
          await setNodeEnabled(target.nodeId, false)
          pushToast({ message: t('nodes.toast.disabled'), severity: 'success' })
          close()
          await refresh()
        } : undefined}
        onClose={close}
        onDone={() => {
          pushToast({ message: t(target.mode === 'delete' ? 'nodes.toast.deleted' : 'nodes.toast.detached'), severity: 'success' })
          // Closed first: the impact query is unmounted before the list refetch, so it is not read again for a node that is gone.
          close()
          void refresh()
        }}
      />
    )
  } else if (open?.kind === 'bulk') {
    const { ids, clear } = open
    element = (
      <BulkDeleteDialog
        nodeIds={ids} nameOf={nameOf} entryOf={entryOf} serverName={serverName} onClose={close}
        onDone={({ ok, failed }) => {
          pushToast(failed > 0
            ? { message: t('nodes.toast.batch_partial', { ok, fail: failed }), severity: 'warning' }
            : { message: t('nodes.toast.batch_deleted', { count: ok }), severity: 'success' })
          clear()
          close()
          void refresh()
        }}
      />
    )
  }
  return { deleteNode, removeFromPanel, deleteMany, element }
}

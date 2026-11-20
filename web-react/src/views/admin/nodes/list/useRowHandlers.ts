import { useMemo, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
<<<<<<< HEAD
import { deleteSeparator, recreateNodeInbound, setNodeEnabled, type OrderRow } from '@/api/nodes'
=======
import { deleteNode, deleteSeparator, detachNode, recreateNodeInbound, setNodeEnabled, type OrderRow } from '@/api/nodes'
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
import { confirm } from '@/components/ConfirmHost'
import { pushToast } from '@/components/feedback/toasts'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import type { LegacyEditorsApi } from '../legacy/LegacyEditors'
import { useNodesT } from '../shared/useNodesT'
import type { RowActionHandlers } from './rowActions'

/** A menu handler cannot be awaited; a failed request was already surfaced by the request layer, so it is not rethrown. */
const safe = <A extends unknown[]>(fn: (...a: A) => Promise<void>) => (...a: A) => { void fn(...a).catch(() => {}) }

/**
 * The row menu's handlers as ONE stable object (the columns are memoised on it, and DataTable memoises rows on
 * the columns). The current inputs are read through a ref, so the object never changes identity.
 *
 * What lives here is what N1 owns: node-level actions and the separator's. The line editor entries open the
<<<<<<< HEAD
 * legacy MUI editors until N2. Deleting a node and removing it from the panel open the impact dialogs (delete/).
 */
export function useRowHandlers({ legacy, moveTo, editSeparator, deletion }: {
  legacy: LegacyEditorsApi
  moveTo: (row: OrderRow) => void
  editSeparator: (id: number) => void
  deletion: { deleteNode: (row: OrderRow) => void; removeFromPanel: (row: OrderRow) => void }
=======
 * legacy MUI editors until N2; delete still goes through the old confirmation until N4's impact dialog.
 */
export function useRowHandlers({ legacy, moveTo, editSeparator }: {
  legacy: LegacyEditorsApi
  moveTo: (row: OrderRow) => void
  editSeparator: (id: number) => void
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
}): RowActionHandlers {
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const t = useNodesT()
<<<<<<< HEAD
  const live = useRef({ legacy, moveTo, editSeparator, deletion, t })
  live.current = { legacy, moveTo, editSeparator, deletion, t }
=======
  const live = useRef({ legacy, moveTo, editSeparator, t })
  live.current = { legacy, moveTo, editSeparator, t }
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
  const refresh = () => queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) })

  return useMemo<RowActionHandlers>(() => ({
    editLine: row => live.current.legacy.openEdit(row.node_id ?? 0, { lineId: row.line_id }),
    moveTo: row => live.current.moveTo(row),
    toggleNode: safe(async (row, enable) => {
      await setNodeEnabled(row.node_id ?? 0, enable)
      pushToast({ message: live.current.t(enable ? 'nodes.toast.enabled' : 'nodes.toast.disabled'), severity: 'success' })
      await refresh()
    }),
    recreateInbound: async row => {
      const { t } = live.current
      const ok = await confirm({
        title: t('nodes.confirm.recreate_inbound_title'),
        message: t('nodes.confirm.recreate_inbound_message', { name: row.landing?.name ?? row.name }),
        confirmText: t('nodes.action.recreate_inbound'),
      })
      if (!ok) return
      await recreateNodeInbound(row.node_id ?? 0)
      pushToast({ message: t('nodes.toast.recreated_inbound'), severity: 'success' })
      await refresh()
    },
<<<<<<< HEAD
    removeFromPanel: row => live.current.deletion.removeFromPanel(row),
    deleteNode: row => live.current.deletion.deleteNode(row),
=======
    removeFromPanel: async row => {
      const { t } = live.current
      const name = row.landing?.name ?? row.name
      const ok = await confirm({ title: t('nodes.delete.remove_title', { name }), message: t('nodes.delete.remove_body'), confirmText: t('nodes.action.remove_from_panel') })
      if (!ok) return
      await detachNode(row.node_id ?? 0)
      pushToast({ message: t('nodes.toast.detached'), severity: 'success' })
      await refresh()
    },
    deleteNode: async row => {
      const { t } = live.current
      const ok = await confirm({
        title: t('nodes.confirm.delete_title'),
        message: t('nodes.confirm.delete_message', { name: row.landing?.name ?? row.name }),
        destructive: true,
        confirmText: t('nodes.action.delete'),
      })
      if (!ok) return
      await deleteNode(row.node_id ?? 0)
      pushToast({ message: t('nodes.toast.deleted'), severity: 'success' })
      await refresh()
    },
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
    editSeparator: row => live.current.editSeparator(row.id ?? 0),
    deleteSeparator: async row => {
      const { t } = live.current
      const ok = await confirm({ title: t('nodes.delete.separator_title', { name: row.name }), message: t('nodes.delete.separator_body'), destructive: true, confirmText: t('nodes.action.delete_separator') })
      if (!ok) return
      await deleteSeparator(row.id ?? 0)
      pushToast({ message: t('nodes.toast.deleted'), severity: 'success' })
      await refresh()
    },
  // `refresh` only reads the query client and scope, which are stable for a session.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }), [queryClient, scope])
}

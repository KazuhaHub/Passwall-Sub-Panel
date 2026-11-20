import { useMemo, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { deleteNode, deleteSeparator, detachNode, recreateNodeInbound, setNodeEnabled, type OrderRow } from '@/api/nodes'
import { confirm } from '@/components/ConfirmHost'
import { pushToast } from '@/components/feedback/toasts'
import { nodeKeys } from '@/query/keys'
import { useQueryScope } from '@/query/useQueryScope'
import { useNodesT } from '../shared/useNodesT'
import type { RowActionHandlers } from './rowActions'

/** The line editor's entries of a row's menu: open it, add a line, enable or disable one, delete one. */
export interface LineRowHandlers {
  edit: (row: OrderRow) => void
  add: (row: OrderRow) => void
  toggle: (row: OrderRow, enable: boolean) => void
  remove: (row: OrderRow) => void
}

/** A menu handler cannot be awaited; a failed request was already surfaced by the request layer, so it is not rethrown. */
const safe = <A extends unknown[]>(fn: (...a: A) => Promise<void>) => (...a: A) => { void fn(...a).catch(() => {}) }

/**
 * The row menu's handlers as ONE stable object (the columns are memoised on it, and DataTable memoises rows on
 * the columns). The current inputs are read through a ref, so the object never changes identity.
 *
 * What lives here is what N1 owns: node-level actions and the separator's. The line entries (edit, enable or
 * disable, add, delete) are the line editor's (N2) and come in as `lines`; delete node still goes through the old
 * confirmation until N4's impact dialog.
 */
export function useRowHandlers({ lines, moveTo, editSeparator }: {
  lines: LineRowHandlers
  moveTo: (row: OrderRow) => void
  editSeparator: (id: number) => void
}): RowActionHandlers {
  const scope = useQueryScope()
  const queryClient = useQueryClient()
  const t = useNodesT()
  const live = useRef({ lines, moveTo, editSeparator, t })
  live.current = { lines, moveTo, editSeparator, t }
  const refresh = () => queryClient.invalidateQueries({ queryKey: nodeKeys.all(scope) })

  return useMemo<RowActionHandlers>(() => ({
    // N2: line entries.
    editLine: row => live.current.lines.edit(row),
    toggleLine: (row, enable) => live.current.lines.toggle(row, enable),
    deleteLine: row => live.current.lines.remove(row),
    addLine: row => live.current.lines.add(row),
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

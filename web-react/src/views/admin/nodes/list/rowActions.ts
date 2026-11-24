import type { TFunction } from 'i18next'
import type { OrderRow } from '@/api/nodes'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { IconDelete, IconEdit } from '@/lib/icons'

/**
 * What a row's menu can do. The handlers a later package owns are OPTIONAL and their entries appear only when
 * the package provides them, so N1 ships no button that does nothing:
 *   N2 fills toggleLine, deleteLine and addLine (the line editor and its save path);
 *   N4 replaces deleteNode / removeFromPanel with the impact dialogs.
 */
export interface RowActionHandlers {
  editLine: (row: OrderRow) => void
  moveTo: (row: OrderRow) => void
  toggleNode: (row: OrderRow, enable: boolean) => void
  recreateInbound: (row: OrderRow) => void
  removeFromPanel: (row: OrderRow) => void
  deleteNode: (row: OrderRow) => void
  editSeparator: (row: OrderRow) => void
  deleteSeparator: (row: OrderRow) => void
  // Later packages.
  toggleLine?: (row: OrderRow, enable: boolean) => void
  deleteLine?: (row: OrderRow) => void
  addLine?: (row: OrderRow) => void
}

/** What the signed-in account and the landing's server allow. */
export interface RowActionCaps {
  /** config.write: an administrator. Operators keep only node enable and disable. */
  admin: boolean
  enable: boolean
  updateInbound: boolean
  recreate: boolean
  remove: boolean
}

export interface RowMenuOptions {
  row: OrderRow
  caps: RowActionCaps
  handlers: RowActionHandlers
  t: TFunction
  /** The sheet's menu offers "open node"; the row's does not, because 编辑节点 already goes there. */
  inSheet?: boolean
  /** Reordering is off while a save is running; Move to… stays on under a filter. */
  canMove?: boolean
  openNodeHref?: (nodeId: number) => string
}

export function rowMenuItems({ row, caps, handlers: h, t, inSheet, canMove = true, openNodeHref }: RowMenuOptions): MoreMenuItem[] {
  if (row.kind === 'separator') {
    if (!caps.admin) return []
    return [
      { id: 'edit', label: t('nodes.action.edit_separator'), icon: IconEdit, onSelect: () => h.editSeparator(row) },
      { id: 'move', label: t('nodes.action.move_to'), disabled: !canMove, onSelect: () => h.moveTo(row) },
      { id: 'delete', label: t('nodes.action.delete_separator'), icon: IconDelete, destructive: true, onSelect: () => h.deleteSeparator(row) },
    ]
  }
  const direct = row.line_id === 'direct'
  const nodeOn = row.landing?.enabled !== false
  const items: MoreMenuItem[] = []
  if (caps.admin) {
    items.push({ id: 'edit-line', label: t(direct ? 'nodes.action.edit_direct' : 'nodes.action.edit_line'), icon: IconEdit, onSelect: () => h.editLine(row) })
    items.push({ id: 'edit-node', label: t('nodes.action.edit_node'), to: openNodeHref?.(row.node_id ?? 0) ?? `/admin/nodes/${row.node_id}` })
    if (!direct && h.toggleLine) items.push({ id: 'toggle-line', label: t(row.enabled ? 'nodes.action.disable_line' : 'nodes.action.enable_line'), onSelect: () => h.toggleLine?.(row, !row.enabled) })
    items.push({ id: 'move', label: t('nodes.action.move_to'), disabled: !canMove, onSelect: () => h.moveTo(row) })
    if (h.addLine) items.push({ id: 'add-line', label: t('nodes.action.add_line'), onSelect: () => h.addLine?.(row) })
  }
  items.push({ id: 'toggle-node', label: t(nodeOn ? 'nodes.action.disable_node' : 'nodes.action.enable_node'), disabled: !caps.enable, onSelect: () => h.toggleNode(row, !nodeOn) })
  if (caps.admin) {
    if (inSheet) items.push({ id: 'open-node', label: t('nodes.action.open_node'), to: openNodeHref?.(row.node_id ?? 0) ?? `/admin/nodes/${row.node_id}` })
    items.push({ id: 'recreate', label: t('nodes.action.recreate_inbound'), disabled: !caps.recreate, onSelect: () => h.recreateInbound(row) })
    items.push({ id: 'remove', label: t('nodes.action.remove_from_panel'), onSelect: () => h.removeFromPanel(row) })
    if (!direct && h.deleteLine) items.push({ id: 'delete-line', label: t('nodes.action.delete_line'), icon: IconDelete, destructive: true, onSelect: () => h.deleteLine?.(row) })
    items.push({ id: 'delete-node', label: t('nodes.action.delete_node'), icon: IconDelete, destructive: true, disabled: !caps.remove, onSelect: () => h.deleteNode(row) })
  }
  return items
}

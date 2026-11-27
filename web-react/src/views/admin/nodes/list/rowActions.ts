import type { TFunction } from 'i18next'
import type { OrderRow } from '@/api/nodes'
import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { IconAdd, IconDelete, IconEdit, IconNode, IconPower, IconReorder } from '@/lib/icons'

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
  /** Reordering is off while a save is running; Move to… stays on under a filter. */
  canMove?: boolean
  /** The by-node view's parent row: Add line is offered there, right after the line items. */
  withAddLine?: boolean
  openNodeHref?: (nodeId: number) => string
}

export function rowMenuItems({ row, caps, handlers: h, t, canMove = true, withAddLine, openNodeHref }: RowMenuOptions): MoreMenuItem[] {
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
  // An operator cannot open the node page, so the one thing they may do (switch the node on or off) stays here, as the
  // only item. It is node-level, but nothing else is offered to them, so nothing is mixed.
  if (!caps.admin) {
    return [{ id: 'toggle-node', label: t(nodeOn ? 'nodes.action.disable_node' : 'nodes.action.enable_node'), icon: IconPower, disabled: !caps.enable, onSelect: () => h.toggleNode(row, !nodeOn) }]
  }
  // A row's menu is about THAT LINE. Everything about the node (enable or disable, recreate the inbound, remove it from
  // the panel only, delete it) lives on the node page, one item away: "Open node".
  const items: MoreMenuItem[] = [
    { id: 'edit-line', label: t(direct ? 'nodes.action.edit_direct' : 'nodes.action.edit_line'), icon: IconEdit, onSelect: () => h.editLine(row) },
    { id: 'move', label: t('nodes.action.move_to'), icon: IconReorder, disabled: !canMove, onSelect: () => h.moveTo(row) },
  ]
  if (!direct && h.toggleLine) items.push({ id: 'toggle-line', label: t(row.enabled ? 'nodes.action.disable_line' : 'nodes.action.enable_line'), icon: IconPower, onSelect: () => h.toggleLine?.(row, !row.enabled) })
  if (withAddLine && h.addLine) items.push({ id: 'add-line', label: t('nodes.action.add_line'), icon: IconAdd, onSelect: () => h.addLine?.(row) })
  items.push({ id: 'open-node', label: t('nodes.action.open_node'), icon: IconNode, separatorBefore: true, to: openNodeHref?.(row.node_id ?? 0) ?? `/admin/nodes/${row.node_id}` })
  if (!direct && h.deleteLine) items.push({ id: 'delete-line', label: t('nodes.action.delete_line'), icon: IconDelete, destructive: true, onSelect: () => h.deleteLine?.(row) })
  return items
}

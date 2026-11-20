import { useMemo } from 'react'
import { Link } from 'react-router'
import type { OrderRow } from '@/api/nodes'
import { Button } from '@/components/ui/button'
import { CopyText } from '@/components/common/CopyButton'
import { MoreMenu, type MoreMenuItem } from '@/components/common/MoreMenu'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { TableSkeleton } from '@/components/feedback/LoadingState'
import { urlCodecs, useUrlState } from '@/hooks/useUrlState'
import { IconDelete, IconEdit } from '@/lib/icons'
import { FactorCell, StatusCell } from '../list/cells'
import { landingColorsOf } from '../list/listModel'
import type { ListRow } from '../list/types'
import { useNodeList } from '../list/useNodeList'
import { LineEditSheet } from '../lines/LineEditSheet'
import { useLineActions } from '../lines/useLineActions'
import { pathOf, type PathLabels } from '../shared/pathOf'
import { useNodesT } from '../shared/useNodesT'

/**
 * The node page's Lines tab (design 4.2): how users get in. One card per line of this node, the direct line first,
 * each with its full path. Editing and adding open the line editor in a sheet over the page (`?edit=<line id>`,
 * `direct`, or `new`), so the page keeps its place and a link opens the editor.
 */
export default function LinesTab({ nodeId }: { nodeId: number }) {
  const t = useNodesT()
  const { order, items, rows } = useNodeList()
  const actions = useLineActions()
  const [edit, setEdit] = useUrlState('edit', urlCodecs.string())
  const colors = useMemo(() => landingColorsOf(rows), [rows])
  const labels = useMemo<PathLabels>(() => ({ external: t('nodes.type.external'), link: t('nodes.type.link'), deleted: t('nodes.withheld.entry_missing') }), [t])
  const mine = useMemo(() => items.filter(i => i.row.kind === 'line' && i.row.node_id === nodeId), [items, nodeId])

  if (order.isError && !order.data) return <ErrorState title={t('nodes.error.load')} reason={order.error} retry={() => void order.refetch()} />
  if (order.isPending) return <TableSkeleton rows={3} cols={2} />

  const menuOf = (item: ListRow): MoreMenuItem[] => {
    const row = item.row
    const direct = row.line_id === 'direct'
    return [
      { id: 'edit', label: t(direct ? 'nodes.action.edit_direct' : 'nodes.action.edit_line'), icon: IconEdit, onSelect: () => setEdit(row.line_id ?? '') },
      ...(direct ? [] : [
        { id: 'toggle', label: t(row.enabled ? 'nodes.action.disable_line' : 'nodes.action.enable_line'), onSelect: () => void actions.toggle(nodeId, [row.line_id ?? ''], !row.enabled) },
        ...(row.line_type === 'forward' || row.line_type === 'tunnel'
          ? [{ id: 'mode', label: t(row.line_type === 'forward' ? 'nodes.line.action.to_tunnel' : 'nodes.line.action.to_forward'), onSelect: () => void actions.switchMode(nodeId, row.line_id ?? '', row.line_type === 'forward' ? 'tunnel' : 'forward') }]
          : []),
        { id: 'delete', label: t('nodes.action.delete_line'), icon: IconDelete, destructive: true, onSelect: () => actions.askDelete({ row, node: item.node, exitName: item.exitName }) },
      ]),
    ]
  }

  const card = (item: ListRow) => {
    const row: OrderRow = item.row
    const { nodes, edges } = pathOf(row, { colors, labels, exit: item.exitName ? { name: item.exitName } : null, ends: true, node: item.node })
    return (
      <li key={item.id} data-testid="node-line-card" data-line-id={row.line_id} className="flex min-w-0 flex-col gap-3 rounded-lg border p-3">
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
          <span className="min-w-0 truncate text-sm font-medium" title={row.name}>{row.line_id === 'direct' ? t('nodes.type.direct') : row.name}</span>
          <FactorCell row={row} />
          <StatusCell item={item} />
          <div className="ml-auto flex items-center gap-1">
            {row.address && <CopyText value={row.port ? `${row.address}:${row.port}` : row.address} />}
            <MoreMenu items={menuOf(item)} label={t('nodes.action.row_menu', { name: row.name })} />
          </div>
        </div>
        <PathDiagram nodes={nodes} edges={edges} />
      </li>
    )
  }

  const editing = edit !== ''
  return (
    <section className="flex flex-col gap-3" aria-label={t('nodes.line.tabs.lines_question')}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-medium">{t('nodes.line.tabs.lines_question')}</h2>
        <Button size="sm" onClick={() => setEdit('new')}>{t('nodes.action.add_line')}</Button>
      </div>
      {mine.length === 0
        ? <EmptyState compact title={t('nodes.line.tabs.no_lines')} action={<Button variant="outline" size="sm" render={<Link to="/admin/nodes" />}>{t('nodes.detail.back')}</Button>} />
        : <ul className="flex flex-col gap-3">{mine.map(card)}</ul>}
      {editing && (
        <LineEditSheet nodeId={nodeId} lineId={edit === 'new' ? null : edit} param="edit" onClose={() => setEdit('')}
          title={edit === 'direct' ? t('nodes.action.edit_direct') : t('nodes.line.edit_title', { name: mine.find(i => i.row.line_id === edit)?.row.name ?? '' })}
          onSaved={() => setEdit('')} />
      )}
      {actions.dialogs}
    </section>
  )
}

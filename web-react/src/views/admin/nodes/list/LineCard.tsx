import type { MoreMenuItem } from '@/components/common/MoreMenu'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import type { LandingColor } from '@/components/diagram/landingColor'
import { LandingColorBar } from '@/components/diagram/LandingColorBar'
import { pathOf, type PathLabels } from '../shared/pathOf'
import { AddressCell, FactorCell, RowMenu, StatusCell } from './cells'
import type { ListRow } from './types'

/** A row at phone width: the name, the state, the path laid out vertically and the menu. The card itself opens the sheet. */
export function LineCard({ item, colors, labels, menu, open }: {
  item: ListRow
  colors: Map<string, LandingColor>
  labels: PathLabels
  menu: MoreMenuItem[]
  open?: () => void
}) {
  const { nodes, edges } = pathOf(item.row, { colors, labels, exit: item.exitName ? { name: item.exitName } : null, ends: true, node: item.node })
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div className="flex min-w-0 items-center gap-2">
        <LandingColorBar color={colors.get(String(item.row.node_id))} className="h-4" />
        <button type="button" onClick={open} className="min-w-0 flex-1 truncate text-left text-sm font-medium outline-none focus-visible:underline" title={item.row.name}>{item.row.name}</button>
        <FactorCell row={item.row} />
        <StatusCell item={item} />
        <RowMenu items={menu} name={item.row.name} />
      </div>
      <PathDiagram nodes={nodes} edges={edges} size="md" orientation="vertical" />
      <AddressCell row={item.row} />
    </div>
  )
}

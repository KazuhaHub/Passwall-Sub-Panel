import { useMemo, useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import type { OrderRow } from '@/api/nodes'
import { Button } from '@/components/ui/button'
import { EmptyState } from '@/components/feedback/EmptyState'
import { RelationGraph } from '@/components/diagram/RelationGraph'
import type { LandingColor } from '@/components/diagram/landingColor'
import { useNodesT } from '../shared/useNodesT'
import { buildGraph, edgeCountOf, graphTooLarge, MAX_GRAPH_EDGES, type ExitOf, type GraphLabels } from './graphModel'

/**
 * The topology view (design 6.3): entries on the left, landing nodes in the middle, exits on the right, one edge
 * per line. It draws the rows the list is currently showing, so the list's search and facets also narrow the
 * picture. Selecting a box lights every path through it (RelationGraph), and offers the list filtered to the
 * lines through that entry, which answers "this server went down: who does it hurt?". Over MAX_GRAPH_EDGES it does
 * not draw and asks for a filter instead; RelationGraph's own connections list is the text alternative.
 */
export function TopologyView({ rows, colors, exitOf, admin, empty, filters, onShowInList, onAsList }: {
  /** The line rows to draw: the list's rows after its search and facets. */
  rows: readonly OrderRow[]
  colors: ReadonlyMap<string, LandingColor>
  exitOf: ExitOf
  admin: boolean
  /** What shows when there is nothing to draw (filtered empty, or no nodes yet). */
  empty: ReactNode
  /** The entry and landing server filters, offered inline when the picture would be too large. */
  filters?: ReactNode
  /** Switch to the order list narrowed to this entry server or line type. */
  onShowInList: (facet: 'entry' | 'type', value: string) => void
  /** Switch to the order list with the filters as they are. */
  onAsList: () => void
}) {
  const t = useNodesT()
  const [picked, setPicked] = useState<string | null>(null)
  const edgeCount = useMemo(() => edgeCountOf(rows, exitOf), [rows, exitOf])
  const tooLarge = graphTooLarge(edgeCount)
  const labels = useMemo<GraphLabels>(() => ({
    direct: t('nodes.graph.box_direct'), cdn: t('nodes.graph.box_cdn'), external: t('nodes.graph.box_external'), local: t('nodes.graph.box_local'),
    link: t('nodes.type.link'), deleted: name => t('nodes.graph.box_deleted', { name }),
    entry: t('nodes.graph.col_entry'), landing: t('nodes.graph.col_landing'), exit: t('nodes.graph.col_exit'),
  }), [t])
  // Nothing is built past the cap: the size check comes first, so a huge list costs a count, not a layout.
  const model = useMemo(() => (tooLarge || edgeCount === 0 ? null : buildGraph(rows, { colors, exitOf, labels })), [rows, colors, exitOf, labels, tooLarge, edgeCount])

  if (edgeCount === 0) return <>{empty}</>
  if (tooLarge || !model) {
    return (
      <EmptyState compact title={t('nodes.graph.too_many')} description={`${t('nodes.graph.too_many_count', { count: edgeCount, max: MAX_GRAPH_EDGES })}. ${t('nodes.graph.too_many_hint')}`}
        action={(
          <div className="flex flex-wrap items-center justify-center gap-2">
            {filters}
            <Button variant="outline" size="sm" onClick={onAsList}>{t('nodes.graph.as_list')}</Button>
          </div>
        )} />
    )
  }

  // A filter that removes the picked box (or a list that changed) drops the selection.
  const selected = picked !== null && model.actions[picked] ? picked : null
  const action = selected ? model.actions[selected] : undefined
  const box = selected ? model.columns.flatMap(c => c.boxes).find(b => b.id === selected) : undefined

  return (
    <div className="flex flex-col gap-3" data-testid="topology">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs text-muted-foreground">{t('nodes.graph.hint')}</p>
        <Button variant="outline" size="sm" onClick={onAsList}>{t('nodes.graph.as_list')}</Button>
      </div>
      <RelationGraph columns={model.columns} edges={model.edges} selectedId={selected} onSelect={setPicked} />
      {box && action && (
        <div className="flex flex-wrap items-center gap-2">
          {action.kind === 'facet' && (
            <Button variant="outline" size="sm" onClick={() => onShowInList(action.facet, action.value)}>{t('nodes.graph.show_in_list', { name: box.label, count: action.lines })}</Button>
          )}
          {action.kind === 'node' && admin && (
            <Button variant="outline" size="sm" render={<Link to={`/admin/nodes/${action.nodeId}`} />}>{t('nodes.action.open_node')}</Button>
          )}
        </div>
      )}
    </div>
  )
}

<<<<<<< HEAD
import { useMemo, type ReactNode } from 'react'
=======
import { useMemo } from 'react'
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
import type { DeleteImpactLine, OrderRow } from '@/api/nodes'
import { PathDiagram } from '@/components/diagram/PathDiagram'
import { assignLandingColors } from '@/components/diagram/landingColor'
import { cn } from '@/lib/utils'
import { pathOf } from './pathOf'
import { useNodesT } from './useNodesT'

/**
 * The lines a deletion would take with it, each drawn as its path (design 5.2). Shared by the node delete
 * dialog, the bulk delete and the server delete (N4); the delete-impact answer carries names and types but not
 * the entry server (design gap G12), so the caller passes `entryOf` from the order rows it already has.
 */
<<<<<<< HEAD
export function ImpactPathList({ lines, entryOf, extra, className }: {
  lines: readonly DeleteImpactLine[]
  entryOf?: (line: DeleteImpactLine) => string | undefined
  /** Something after a line's diagram: a link to fix it, a note about it (the server delete dialog). */
  extra?: (line: DeleteImpactLine) => ReactNode
=======
export function ImpactPathList({ lines, entryOf, className }: {
  lines: readonly DeleteImpactLine[]
  entryOf?: (line: DeleteImpactLine) => string | undefined
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
  className?: string
}) {
  const t = useNodesT()
  const labels = useMemo(() => ({ external: t('nodes.type.external'), link: t('nodes.type.link'), deleted: t('nodes.withheld.entry_missing') }), [t])
  const colors = useMemo(() => assignLandingColors([...new Set(lines.map(l => l.node_id))].sort((a, b) => a - b)), [lines])
  return (
    <ul className={cn('flex flex-col gap-1.5', className)} data-testid="impact-lines">
      {lines.map(l => {
        const entry = entryOf?.(l)
        const row: OrderRow = {
          kind: 'line', node_id: l.node_id, line_id: l.line_id, name: l.line_name, enabled: true, position: 0, positioned: true,
          line_type: l.line_type, landing: { node_id: l.node_id, name: l.landing_name, region: '', panel_id: 0, panel_name: '', enabled: true },
          entry: entry !== undefined ? { panel_id: 0, panel_name: entry, missing: false } : undefined, address: entry ?? '',
        }
        const { nodes, edges } = pathOf(row, { colors, labels })
        return (
<<<<<<< HEAD
          <li key={`${l.node_id}.${l.line_id}`} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-sm">
            <span className="w-28 shrink-0 truncate text-muted-foreground" title={l.line_name}>{l.line_name}</span>
            <PathDiagram nodes={nodes} edges={edges} size="sm" />
            {extra?.(l)}
=======
          <li key={`${l.node_id}.${l.line_id}`} className="flex min-w-0 items-center gap-3 text-sm">
            <span className="w-28 shrink-0 truncate text-muted-foreground" title={l.line_name}>{l.line_name}</span>
            <PathDiagram nodes={nodes} edges={edges} size="sm" />
>>>>>>> refs/remotes/origin/kazuha/ui-shadcn
          </li>
        )
      })}
    </ul>
  )
}

import { useMemo } from 'react'
import type { OrderRow } from '@/api/nodes'
import type { Server } from '@/api/servers'
import type { Node } from '@/api/types'
import { PathDiagram, type PathEdge, type PathNode } from '@/components/diagram/PathDiagram'
import { pathOf, type PathLabels } from '../shared/pathOf'
import { useNodesT } from '../shared/useNodesT'
import { kindOf, type LineDraft, type LineKind } from './lineModel'

/**
 * The unsaved line as a list row, so the editor's picture is drawn by the SAME function as every row and sheet
 * (design 4.4.3): the entry is the chosen server (or the CDN domain, or the address typed), the hop carries the
 * port, the landing is the node and the exit is the node's current one. The draft is not published or probed,
 * so it carries no state: a picture of what the line WILL be.
 */
export function rowOfDraft(draft: LineDraft, node: Node, servers: readonly Server[]): OrderRow {
  const kind = kindOf(draft)
  const entry = servers.find(s => s.id === draft.entry_panel_id)
  return {
    kind: 'line', node_id: node.id, line_id: draft.id ?? 'new', name: draft.name, enabled: draft.enabled, position: 0, positioned: false,
    landing: { node_id: node.id, name: node.display_name, region: node.region, panel_id: node.panel_id, panel_name: node.panel_name, enabled: node.enabled },
    entry: kind === 'forward' || kind === 'tunnel' ? { panel_id: draft.entry_panel_id ?? 0, panel_name: entry?.name ?? '', missing: false } : undefined,
    line_type: kind,
    address: kind === 'cdn' ? (draft.address.trim() || draft.domain?.trim() || '') : draft.address.trim(),
    port: Number(draft.port) || undefined,
  }
}

const NO_COLORS = new Map()

export function LineDiagram({ draft, node, servers, className }: { draft: LineDraft; node: Node; servers: readonly Server[]; className?: string }) {
  const t = useNodesT()
  const labels = useMemo<PathLabels>(() => ({ external: t('nodes.type.external'), link: t('nodes.type.link'), deleted: t('nodes.withheld.entry_missing') }), [t])
  const exit = node.egress_panel_id ? servers.find(s => s.id === node.egress_panel_id) : undefined
  const { nodes, edges } = pathOf(rowOfDraft(draft, node, servers), { colors: NO_COLORS, labels, exit: exit ? { name: exit.name } : null, ends: true })
  return <PathDiagram nodes={nodes} edges={edges} className={className} />
}

/** The small picture on a type card: the entry, the kind of hop, the landing. */
export function CardDiagram({ kind }: { kind: LineKind }) {
  const t = useNodesT()
  const entryKind = kind === 'cdn' ? 'cdn' : kind === 'external' ? 'external' : 'entry'
  const nodes: PathNode[] = [{ id: 'e', kind: entryKind }, { id: 'l', kind: 'landing' }]
  const edges: PathEdge[] = [kind === 'cdn' ? { style: 'solid', role: 'origin' } : kind === 'external' ? { style: 'solid', name: t('nodes.type.external') } : { style: kind === 'tunnel' ? 'dashed' : 'solid' }]
  return <PathDiagram nodes={nodes} edges={edges} size="sm" />
}

import type { OrderRow } from '@/api/nodes'
import type { LandingColor } from '@/components/diagram/landingColor'
import type { PathEdge, PathNode, PathTone } from '@/components/diagram/pathSummary'
import { statusOf, type NodeStatusFacts, type RowStatus } from './statusOf'

export interface PathLabels {
  /** The hop label of an external entry ("外部入口") and of the server-to-server tunnel to the exit. */
  external: string
  link: string
  /** An entry server that no longer exists. */
  deleted: string
}

export interface PathOptions {
  colors: Map<string, LandingColor>
  labels: PathLabels
  /** The server the landing leaves through, when it is not its own. */
  exit?: { name: string } | null
  /** The user and internet ends: md and the sheet draw them, a table row (sm) does not. */
  ends?: boolean
  node?: NodeStatusFacts
}

/** A status tone as the diagram's: info is the pending kind; a quiet healthy line is drawn plain. */
export function pathTone(status: RowStatus): PathTone | undefined {
  if (status.id === 'ok' || status.id === 'unchecked') return undefined
  return status.tone === 'info' ? 'pending' : status.tone === 'success' ? undefined : status.tone
}

/**
 * A list row as the nodes and edges of a PathDiagram (design 6.2). The cause of a problem is drawn on the hop
 * that causes it: a line waiting for its entry or a downed link on the entry hop, an exit that is not ready on
 * the exit hop, a deleted entry on the entry node. A line that is simply off (disabled, hidden, landing off)
 * is dimmed as a whole, so it never reads as a fault.
 */
export function pathOf(row: OrderRow, { colors, labels, exit, ends = false, node }: PathOptions): { nodes: PathNode[]; edges: PathEdge[] } {
  const status = statusOf(row, node)
  const tone = pathTone(status)
  const dimmed = status.tone === 'neutral' && tone === 'neutral'
  const landingId = row.landing?.node_id ?? row.node_id ?? 0
  const port = row.port ? `:${row.port}` : undefined
  const direct = row.line_type === 'direct'

  const whole: PathTone | undefined = dimmed ? 'neutral' : undefined
  const entryHop: PathTone | undefined = !dimmed && (status.id === 'waiting_entry' || status.id === 'probe') ? tone : whole
  const exitHop: PathTone | undefined = !dimmed && status.id === 'egress' ? tone : whole

  const landing: PathNode = {
    id: `landing-${landingId}`, kind: 'landing', label: row.landing?.name ?? row.name, landingColor: colors.get(String(landingId)),
    sublabel: direct ? port : row.landing?.panel_name, sublabelOptional: !direct, status: whole,
  }
  const user: PathNode = { id: 'user', kind: 'user' }
  const internet: PathNode = { id: 'internet', kind: 'internet' }

  let nodes: PathNode[]
  let edges: PathEdge[]
  const entryName = row.entry?.panel_name ?? row.address ?? ''
  switch (row.line_type) {
    case 'forward':
    case 'tunnel': {
      const missing = row.entry?.missing === true
      const entry: PathNode = {
        id: 'entry', kind: 'entry', label: missing ? `${entryName} (${labels.deleted})` : entryName, sublabel: port,
        status: missing ? 'danger' : whole,
      }
      nodes = [user, entry, landing]
      edges = [{ style: 'solid', name: '', status: whole }, { style: row.line_type === 'tunnel' ? 'dashed' : 'solid', status: entryHop }]
      break
    }
    case 'cdn':
      nodes = [user, { id: 'cdn', kind: 'cdn', label: row.address, status: whole }, landing]
      edges = [{ style: 'solid', status: whole }, { style: 'solid', role: 'origin', label: port, status: entryHop }]
      break
    case 'external':
      nodes = [user, { id: 'ext', kind: 'external', label: row.address, sublabel: port, status: whole }, landing]
      edges = [{ style: 'solid', status: whole }, { style: 'solid', name: labels.external, status: entryHop }]
      break
    default:
      nodes = [user, landing]
      edges = [{ style: 'solid', name: '', status: whole }]
  }
  if (exit) {
    nodes = [...nodes, { id: 'exit', kind: 'exit', label: exit.name, status: whole }]
    edges = [...edges, { style: 'dashed', name: labels.link, status: exitHop }]
  }
  if (ends) {
    nodes = [...nodes, internet]
    edges = [...edges, { style: 'solid', name: '', status: whole }]
  } else {
    // A table row has no room for the ends, and they are the same on every row.
    nodes = nodes.slice(1)
    edges = edges.slice(1)
  }
  return { nodes, edges }
}

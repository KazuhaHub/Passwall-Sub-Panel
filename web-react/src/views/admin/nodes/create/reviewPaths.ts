import type { NodeSetupBinding, NodeSetupPlan, OrderRow } from '@/api/nodes'
import type { Server } from '@/api/servers'
import type { PathEdge, PathNode } from '@/components/diagram/PathDiagram'
import { CDN_DEFAULT_PORT, kindOf } from '../lines/lineModel'
import { pathOf, type PathLabels } from '../shared/pathOf'
import type { SetupRelay } from './setupModel'

export interface ReviewPath {
  key: string
  /** The kind key under `nodes.setup.kind.`: the direct line is `landing`. */
  kind: string
  name: string
  nodes: PathNode[]
  edges: PathEdge[]
}

interface Context {
  plan: NodeSetupPlan
  entries: readonly SetupRelay[]
  direct: boolean
  peers: readonly Server[]
  landingName: string
  landingPort: number
  exitName: string | null
  labels: PathLabels
  /** `:{port}` for a hop, and the loopback wording. */
  loopbackLabel: (port: number) => string
}

const NO_COLORS = new Map()
const colon = (port: number) => `:${port}`

/**
 * The lines the plan will create, each as the path a connection takes, with the ports the plan CHOSE written on
 * the hops (the preview, not what was typed: a shared port or a taken one is already resolved there). The
 * ports come from the bindings, which name a server, not a line, and a server is entry to at most one line of
 * a new node (the planner refuses a second), so (kind, server) finds a line's own binding.
 *
 * Where each port is drawn: a direct line's landing port and a forward's entry port are the node's own ports
 * (the box under the node); the transit tunnel between an entry server and the landing server is the port
 * on the hop between them; the exit's tunnel is the port on the hop to the exit. A shared-port loopback is added
 * to the hop it belongs to.
 */
export function reviewPaths(c: Context): ReviewPath[] {
  const landing = c.plan.bindings.find(b => b.kind === 'landing')
  const landingServer = landing?.panel_id ?? 0
  const peer = (id: number) => c.peers.find(p => p.id === id)
  const exitId = c.exitName ? c.peers.find(p => p.name === c.exitName)?.id : undefined
  const exitLink = exitId ? c.plan.bindings.find(b => b.kind === 'link' && b.peer_id === landingServer && b.panel_id === exitId) : undefined
  const bindingOf = (kind: NodeSetupBinding['kind'], server: number) => c.plan.bindings.find(b => b.kind === kind && b.panel_id === server)

  const build = (row: OrderRow, hop?: string): Pick<ReviewPath, 'nodes' | 'edges'> => {
    const { nodes, edges } = pathOf(row, { colors: NO_COLORS, labels: c.labels, exit: c.exitName ? { name: c.exitName } : null, ends: true })
    const label = (at: number, text: string | undefined) => (text ? (e: PathEdge, i: number) => (i === at ? { ...e, label: text } : e) : (e: PathEdge) => e)
    const hopAt = row.line_type === 'direct' ? -1 : 1
    const exitAt = row.line_type === 'direct' ? 1 : 2
    const marked = edges.map(label(hopAt, hop)).map(label(exitAt, exitLink ? colon(exitLink.port) : undefined))
    return { nodes, edges: marked }
  }

  const landingRow = (line: Pick<OrderRow, 'line_id' | 'name' | 'line_type' | 'address' | 'port' | 'entry'>): OrderRow => ({
    kind: 'line', node_id: 0, enabled: true, position: 0, positioned: false,
    landing: { node_id: 0, name: c.landingName, region: '', panel_id: landingServer, panel_name: peer(landingServer)?.name ?? '', enabled: true },
    ...line,
  })

  const out: ReviewPath[] = []
  if (c.direct) {
    out.push({ key: 'direct', kind: 'landing', name: c.landingName, ...build(landingRow({ line_id: 'direct', name: c.landingName, line_type: 'direct', port: landing?.port ?? c.landingPort })) })
  }
  for (const { key, line } of c.entries) {
    const kind = kindOf(line)
    const name = line.name.trim() || line.domain?.trim() || line.address.trim()
    const server = line.entry_panel_id ?? 0
    const entry = server ? { panel_id: server, panel_name: peer(server)?.name ?? `#${server}`, missing: false } : undefined
    let port = line.port || (kind === 'cdn' ? CDN_DEFAULT_PORT : c.landingPort)
    let hop: string | undefined
    if (kind === 'forward') {
      const b = bindingOf('forward', server)
      port = b?.port ?? port
      // Shared by SNI: the entry hands the connection to the landing's port.
      hop = [colon(landing?.port ?? c.landingPort), b?.loopback ? c.loopbackLabel(b.loopback) : ''].filter(Boolean).join(' · ')
    } else if (kind === 'tunnel') {
      const b = bindingOf('tunnel', server)
      port = b?.port ?? port
      const link = c.plan.bindings.find(x => x.kind === 'link' && x.peer_id === server && x.panel_id === landingServer)
      hop = [link ? colon(link.port) : '', b?.loopback ? c.loopbackLabel(b.loopback) : ''].filter(Boolean).join(' · ') || undefined
    }
    const row = landingRow({
      line_id: key, name, line_type: kind, entry, port,
      address: kind === 'cdn' ? (line.address.trim() || line.domain?.trim() || '') : line.address.trim(),
    })
    out.push({ key, kind, name, ...build(row, hop) })
  }
  return out
}

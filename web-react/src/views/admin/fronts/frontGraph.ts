import type { Front, FrontTarget } from '@/api/fronts'
import type { GraphBox, GraphColumn, GraphEdge } from '@/components/diagram/RelationGraph'

/** The words a graph box or column carries, supplied by the caller so this stays free of i18n. */
export interface GraphWords {
  columns: readonly [string, string, string]
  port: (port: number) => string
  /** Under the port: how many routes it has. */
  routes: (count: number) => string
  other: string
  /** A listener behind the front: its name, and where on this machine the front hands it connections. */
  listener: (nodeId: number, loopbackPort: number | undefined) => { label: string; sublabel?: string }
  forward: (nodeId: number, lineId: string) => string
  link: (linkId: number) => string
  external: (host: string, port: number) => string
}

export interface FrontGraphModel { columns: GraphColumn[]; edges: GraphEdge[] }

/**
 * "A client connects to a shared port, the SNI in its TLS handshake picks the route, the route hands the
 * connection to a service": the three columns of RelationGraph, one chain per route. A target used by several
 * routes (or fronts) is ONE box with several lines into it, so "what is behind this listener" reads at a glance.
 * `inactive` holds the front ids the server is not sending; their boxes and lines carry the warning status, which
 * the graph speaks in words as well as colour.
 */
export function frontGraph(fronts: readonly Front[], inactive: ReadonlySet<number>, w: GraphWords): FrontGraphModel {
  const ports: GraphBox[] = []
  const snis: GraphBox[] = []
  const targets = new Map<string, GraphBox>()
  const edges: GraphEdge[] = []

  const targetBox = (f: Front, target: FrontTarget): string => {
    let id: string
    let box: Omit<GraphBox, 'id'>
    switch (target.kind) {
      case 'listener': {
        id = `target:listener:${target.node_id ?? 0}`
        const loopback = f.listeners.find(l => l.node_id === target.node_id)?.loopback_port
        box = w.listener(target.node_id ?? 0, loopback)
        break
      }
      case 'forward':
        id = `target:forward:${target.node_id ?? 0}:${target.line_id ?? ''}`
        box = { label: w.forward(target.node_id ?? 0, target.line_id ?? '') }
        break
      case 'link':
        id = `target:link:${target.link_id ?? 0}`
        box = { label: w.link(target.link_id ?? 0) }
        break
      default:
        id = `target:external:${target.host ?? ''}:${target.port ?? 0}`
        box = { label: w.external(target.host ?? '', target.port ?? 0) }
    }
    if (!targets.has(id)) targets.set(id, { id, ...box })
    return id
  }

  for (const f of fronts) {
    const status = inactive.has(f.id) ? 'warning' as const : undefined
    const portId = `port:${f.id}`
    ports.push({ id: portId, label: w.port(f.port), sublabel: w.routes(f.routes.length + 1), status })
    const hop = (sniId: string, label: string, target: FrontTarget, key: string) => {
      snis.push({ id: sniId, label, status })
      edges.push({ id: `a:${key}`, from: portId, to: sniId, status })
      edges.push({ id: `b:${key}`, from: sniId, to: targetBox(f, target), status })
    }
    f.routes.forEach((r, i) => hop(`sni:${f.id}:${i}`, r.server_names.join(', '), r.target, `${f.id}:${i}`))
    hop(`sni:${f.id}:default`, w.other, f.default, `${f.id}:default`)
  }

  return {
    columns: [
      { id: 'ports', title: w.columns[0], boxes: ports },
      { id: 'sni', title: w.columns[1], boxes: snis },
      { id: 'targets', title: w.columns[2], boxes: [...targets.values()] },
    ],
    edges,
  }
}

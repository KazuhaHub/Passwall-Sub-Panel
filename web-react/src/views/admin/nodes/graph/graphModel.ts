import type { OrderRow } from '@/api/nodes'
import type { GraphBox, GraphColumn, GraphEdge } from '@/components/diagram/RelationGraph'
import type { LandingColor } from '@/components/diagram/landingColor'
import type { PathTone } from '@/components/diagram/pathSummary'
import { rowId } from '../list/orderMoves'
import { pathTone } from '../shared/pathOf'
import { statusOf } from '../shared/statusOf'

/**
 * The topology view as data (design 6.3): which boxes, which edges, and what clicking a box offers. Free of React
 * so the mapping from list rows and the size rule have plain tests.
 */

/**
 * Most edges the view draws. RelationGraph keeps up to 200 paths per box and does its layout work over every
 * path of every box, so it is meant for "the hundreds"; past this the view asks for a filter instead of drawing
 * something nobody can read (design 6.3).
 */
export const MAX_GRAPH_EDGES = 600
export const graphTooLarge = (edges: number) => edges > MAX_GRAPH_EDGES

export interface GraphLabels {
  direct: string
  cdn: string
  external: string
  /** The exit box of a landing that leaves from its own server. */
  local: string
  /** The edge from a landing to the server it leaves through. */
  link: string
  /** An entry server that no longer exists. */
  deleted: (name: string) => string
  entry: string
  landing: string
  exit: string
}

export type ExitOf = (nodeId: number) => { id: number; name: string } | null

/** What a box offers beside the connections list: the list filtered to its lines, or its node's page. */
export type BoxAction =
  | { kind: 'facet'; facet: 'entry' | 'type'; value: string; lines: number }
  | { kind: 'node'; nodeId: number; lines: number }

type ActionSeed = { kind: 'facet'; facet: 'entry' | 'type'; value: string } | { kind: 'node'; nodeId: number }

export interface GraphModel {
  columns: GraphColumn[]
  edges: GraphEdge[]
  actions: Record<string, BoxAction>
}

const lines = (rows: readonly OrderRow[]) => rows.filter(r => r.kind === 'line')

/** Edges the view would draw for these rows: one per line, plus one per landing once there is an exit column. */
export function edgeCountOf(rows: readonly OrderRow[], exitOf: ExitOf): number {
  const ls = lines(rows)
  const landings = [...new Set(ls.map(r => r.node_id as number))]
  return ls.length + (landings.some(id => exitOf(id)) ? landings.length : 0)
}

/** The tone worth putting on a box: the worst of its edges (a quiet or merely disabled edge says nothing). */
const WORST: PathTone[] = ['danger', 'warning', 'pending']
const worst = (tones: (PathTone | undefined)[]) => WORST.find(t => tones.includes(t))

export function buildGraph(
  rows: readonly OrderRow[],
  { colors, exitOf, labels }: { colors: ReadonlyMap<string, LandingColor>; exitOf: ExitOf; labels: GraphLabels },
): GraphModel {
  const ls = lines(rows)
  const entryBoxes = new Map<string, GraphBox>()
  const landingBoxes = new Map<string, GraphBox>()
  const edges: GraphEdge[] = []
  const actions: Record<string, BoxAction> = {}
  const entryTones = new Map<string, (PathTone | undefined)[]>()
  const bump = (id: string, action: ActionSeed) => {
    const cur = actions[id]
    actions[id] = { ...action, lines: (cur?.lines ?? 0) + 1 } as BoxAction
  }

  for (const r of ls) {
    const landingId = `landing:${r.node_id}`
    if (!landingBoxes.has(landingId)) {
      landingBoxes.set(landingId, { id: landingId, label: r.landing?.name ?? r.name, sublabel: r.landing?.panel_name, landingColor: colors.get(String(r.node_id)) })
    }
    bump(landingId, { kind: 'node', nodeId: r.node_id as number })

    let from: string
    let entry: GraphBox
    switch (r.line_type) {
      case 'forward':
      case 'tunnel': {
        const id = r.entry?.panel_id ?? 0
        from = `entry:${id}`
        const name = r.entry?.panel_name || r.address || ''
        entry = { id: from, label: r.entry?.missing ? labels.deleted(name) : name }
        bump(from, { kind: 'facet', facet: 'entry', value: String(id) })
        break
      }
      case 'cdn': from = 'cdn'; entry = { id: from, label: labels.cdn }; bump(from, { kind: 'facet', facet: 'type', value: 'cdn' }); break
      case 'external': from = 'external'; entry = { id: from, label: labels.external }; bump(from, { kind: 'facet', facet: 'type', value: 'external' }); break
      default: from = 'direct'; entry = { id: from, label: labels.direct }; bump(from, { kind: 'facet', facet: 'type', value: 'direct' })
    }
    if (!entryBoxes.has(from)) entryBoxes.set(from, entry)
    const status = pathTone(statusOf(r))
    entryTones.set(from, [...(entryTones.get(from) ?? []), status])
    // CDN and external entries share one box each, so their own address is what tells two such lines apart.
    const label = r.line_type === 'cdn' || r.line_type === 'external' ? [r.name, r.address].filter(Boolean).join(' · ') : r.name
    edges.push({ id: rowId(r), label, from, to: landingId, style: r.line_type === 'tunnel' ? 'dashed' : 'solid', status })
  }
  for (const [id, box] of entryBoxes) box.status = worst(entryTones.get(id) ?? [])

  const columns: GraphColumn[] = [
    { id: 'entry', title: labels.entry, boxes: [...entryBoxes.values()] },
    { id: 'landing', title: labels.landing, boxes: [...landingBoxes.values()] },
  ]

  // The exit column exists only when some landing leaves through another server; then EVERY landing is joined to a
  // box in it, the server it leaves through or "its own server", so a landing is never left dangling.
  const nodeIds = [...new Set(ls.map(r => r.node_id as number))]
  if (nodeIds.some(id => exitOf(id))) {
    const exitBoxes = new Map<string, GraphBox>()
    for (const id of nodeIds) {
      const exit = exitOf(id)
      const to = exit ? `exit:${exit.id}` : 'exit:local'
      // Boxes appear as a landing uses them, so "its own server" is not drawn when every landing leaves elsewhere.
      if (!exitBoxes.has(to)) exitBoxes.set(to, { id: to, label: exit ? exit.name : labels.local })
      // Only a landing that leaves through ANOTHER server crosses a server-to-server tunnel; leaving from its own
      // server is a plain, unlabelled hop (drawing it as a tunnel would claim a link that does not exist).
      edges.push(exit ? { id: `exit:${id}`, from: `landing:${id}`, to, style: 'dashed', label: labels.link } : { id: `exit:${id}`, from: `landing:${id}`, to, style: 'solid' })
    }
    columns.push({ id: 'exit', title: labels.exit, boxes: [...exitBoxes.values()] })
  }
  return { columns, edges, actions }
}

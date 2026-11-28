import type { OrderRow } from '@/api/nodes'
import type { LineFactorChange } from '@/api/servers'
import { DEFAULT_TRAFFIC_FACTOR_MILLI } from '@/utils/trafficFactor'
import type { ListRow } from './types'

/** One line whose multiplier a dialog may change: the direct line stands for its node. */
export interface MultiplierLine {
  key: string
  node_id: number
  /** "direct" or a relay line id. */
  line_id: string
  server_id: number
  label: string
  old: number
}

/** Whether a row stands for a line whose multiplier can be written (it names its server, node and line). */
export function hasMultiplier(r: OrderRow): boolean {
  return r.kind === 'line' && !!r.landing?.panel_id && r.node_id !== undefined && !!r.line_id
}

export function multiplierLineOf(item: ListRow): MultiplierLine | null {
  const r = item.row
  const server = r.landing?.panel_id
  if (!hasMultiplier(r) || !server || r.node_id === undefined || !r.line_id) return null
  const direct = r.line_id === 'direct'
  const label = direct ? (r.landing?.name ?? r.name) : `${r.landing?.name ?? ''} / ${r.name}`
  return { key: item.id, node_id: r.node_id, line_id: r.line_id, server_id: server, label, old: r.traffic_factor_milli ?? DEFAULT_TRAFFIC_FACTOR_MILLI }
}

/**
 * The lines a selection stands for. A `landing:<id>` entry (the by-node view's parent row) stands for every line
 * of that node; any other id is one line. A line is listed once however it was reached.
 */
export function multiplierLinesOf(items: readonly ListRow[], ids: readonly string[]): MultiplierLine[] {
  const wanted = new Set<string>()
  for (const id of ids) {
    if (id.startsWith('landing:')) {
      const node = Number(id.slice(8))
      for (const i of items) if (i.row.node_id === node) wanted.add(i.id)
    } else wanted.add(id)
  }
  return items.filter(i => wanted.has(i.id)).flatMap(i => multiplierLineOf(i) ?? [])
}

/** One request per server (the endpoint takes a server and its lines, all or nothing). */
export function groupByServer(lines: readonly MultiplierLine[], milli: number): { serverId: number; changes: LineFactorChange[] }[] {
  const by = new Map<number, LineFactorChange[]>()
  for (const l of lines) by.set(l.server_id, [...(by.get(l.server_id) ?? []), { node_id: l.node_id, line_id: l.line_id, traffic_factor_milli: milli }])
  return [...by].map(([serverId, changes]) => ({ serverId, changes }))
}

import type { DeleteImpactLine, NodeDeleteImpact, OrderRow } from '@/api/nodes'
import type { ServerDeleteImpact } from '@/api/servers'
import type { TunnelPlanView } from '@/utils/tunnelConfirm'

// Pure rules of the deletion dialogs, apart from React so each has a plain test.

export type DeleteMode = 'delete' | 'detach'

export type ImpactView =
  | { kind: 'loading' }
  | { kind: 'failed'; reason: unknown }
  | { kind: 'blocked'; routes: string[]; impact: NodeDeleteImpact }
  | { kind: 'ready'; impact: NodeDeleteImpact }

/**
 * What a node dialog should show for the state of its impact read. A delete behind shared-port routes is refused by
 * the server (checkRemovalAgainstFronts), so the dialog offers no delete button then. "Remove from panel only" never
 * runs that check, so it is not blocked by the same routes and shows the impact like any other.
 */
export function impactViewOf(
  q: { isPending: boolean; isError: boolean; data: NodeDeleteImpact | undefined; error: unknown },
  mode: DeleteMode,
): ImpactView {
  if (q.data) {
    if (mode === 'delete' && q.data.blocked_by_routes.length > 0) return { kind: 'blocked', routes: q.data.blocked_by_routes, impact: q.data }
    return { kind: 'ready', impact: q.data }
  }
  if (q.isError) return { kind: 'failed', reason: q.error }
  return { kind: 'loading' }
}

/**
 * Whether the server's tunnel plan is exactly what the dialog already showed: a node delete that only closes the
 * listeners listed as "entry listeners that close". Then answering the plan again would be a second question about
 * the same thing, so the dialog accepts it. Anything else (a listener that was not listed, something created, a
 * different action) is a plan the admin has not seen and is shown to them.
 */
export function planCovered(plan: TunnelPlanView, shown: NodeDeleteImpact['tunnels']): boolean {
  if (plan.action !== 'delete_node') return false
  const p = plan.error.params
  const list = (v: unknown) => (Array.isArray(v) ? v as Record<string, unknown>[] : [])
  if (list(p.creates).length > 0 || list(p.diff).length > 0) return false
  return list(p.removes).every(r => shown.some(s => s.line_id === r.line_id && s.entry_panel_id === r.entry_panel_id && (s.port ?? 0) === ((r.port as number | undefined) ?? 0)))
}

/**
 * The entry server of an impact line. The delete-impact answer has no entry field (design gap G12), so it is read
 * from the order rows the list already holds: a managed entry by its server's name, a CDN or external entry by the
 * address it is reached at, and nothing for the direct line.
 */
export function entryLookup(rows: readonly OrderRow[]): (line: DeleteImpactLine) => string | undefined {
  const byKey = new Map<string, OrderRow>()
  for (const r of rows) if (r.kind === 'line') byKey.set(`${r.node_id}.${r.line_id}`, r)
  return line => {
    if (line.line_id === 'direct') return undefined
    const r = byKey.get(`${line.node_id}.${line.line_id}`)
    return r?.entry?.panel_name || r?.address || undefined
  }
}

export const totalLines = (impacts: readonly { lines: readonly unknown[] }[]) => impacts.reduce((n, i) => n + i.lines.length, 0)

/** The raw records of a server's delete-impact answer, typed for the dialog. */
export interface ServerNodeRef { node_id: number; node: string; tunnel_line_of_node_id?: number; tunnel_line_of?: string; tunnel_line?: string }
export interface ServerRouteUser { node_id: number; node: string; routes: string[] }

export function serverImpactModel(impact: ServerDeleteImpact) {
  const nodes = impact.nodes as unknown as ServerNodeRef[]
  const routes = impact.route_users as unknown as ServerRouteUser[]
  const lines = impact.lines_entering
  return {
    nodes, routes, lines, egress: impact.egress_users,
    /** Delete refuses on nodes, entering lines and routes (admin_servers.go Delete); the exit users do not block. */
    blocking: nodes.length + lines.length + routes.length,
    /** Some entering line belongs to a landing whose delete is still queued. */
    pendingLandings: lines.some(l => l.landing_pending_delete),
  }
}

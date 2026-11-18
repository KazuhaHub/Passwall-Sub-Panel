import { isAxiosError } from 'axios'
import { client } from './client'
import type { ReadOptions } from './requestOptions'
import type { ProbeQuality } from './types'

// Routing by destination (handler/admin_route.go, adminGroup only). A node's
// ordered routes send some destinations out through another server, or keep
// them on the node's own server, while everything no route matches leaves
// through the node's default exit. Every call is silent (`_skipErrorToast`): a
// refusal carries a stable `code` and parameters, which the editor words itself
// and shows beside the form.

export type RouteTarget = { kind: 'local' } | { kind: 'server'; panel_id: number }

/** What the editor sends for one route; `id` is omitted for a new route. */
export interface RouteInput {
  id?: string
  name?: string
  target: RouteTarget
  list_ids: number[]
  /** Typed entries: `domain:x`, `full:x` or `keyword:x` only. */
  domains: string[]
  cidrs: string[]
  /** "25,465,587" or "6881-6889"; empty for any port. */
  ports: string
  network: '' | 'tcp' | 'udp'
}

export type RouteNoteCode = 'list_not_ready' | 'list_empty' | 'list_regexp_skipped' | 'list_last_good' | 'route_skipped'

export interface RouteNote { code: RouteNoteCode; list_id?: number; list?: string; count?: number }

/** The tunnel from the node's server to a route's (or the default) exit. */
export interface RouteLink {
  panel_id: number
  server_name?: string
  /** The exit server no longer exists; the node is withheld until the route changes. */
  gone?: boolean
  exists: boolean
  ready: boolean
  in_applied: boolean
  out_applied: boolean
  quality?: ProbeQuality
}

export interface RouteView extends RouteInput {
  id: string
  /** Compiled destination entries this route holds. */
  entries: number
  /** The route is in the config sent to the server. */
  sent: boolean
  notes: RouteNote[]
  /** Absent for a local target. */
  link?: RouteLink
}

export type RouteSupportReason = 'not_node' | 'no_agent' | 'no_capability' | 'no_link_capability'

export interface RouteWarning {
  code: string
  /** English fallback for a code the editor does not know. */
  message: string
  params?: Record<string, unknown>
  listener?: string
  route_id?: string
}

export interface RouteBudget {
  entries: number
  rules: number
  bytes: number
  max_entries: number
  max_rules: number
  max_bytes: number
  engine: 'xray' | 'sing-box'
}

export interface RoutesView {
  supported: boolean
  reason?: RouteSupportReason
  routes: RouteView[]
  default_exit: RouteTarget
  default_link?: RouteLink
  /** The server's config holds this node's routes. */
  sent: boolean
  /** The server confirmed them applied. */
  running: boolean
  sent_at?: string
  applied_at?: string
  warnings: RouteWarning[]
  budget?: RouteBudget
}

export interface RouteError {
  status: number
  code: string
  params: Record<string, unknown>
  message: string
}

const quiet = { _skipErrorToast: true } as const
const base = (nodeId: number) => `/admin/nodes/${nodeId}/routes`

function normalise(view: RoutesView): RoutesView {
  return {
    ...view,
    routes: (view.routes ?? []).map(r => ({
      ...r,
      list_ids: r.list_ids ?? [], domains: r.domains ?? [], cidrs: r.cidrs ?? [], notes: r.notes ?? [],
      ports: r.ports ?? '', network: r.network ?? '',
    })),
    warnings: view.warnings ?? [],
  }
}

export async function getRoutes(nodeId: number, opts: ReadOptions = {}): Promise<RoutesView> {
  const { data } = await client.get<RoutesView>(base(nodeId), { signal: opts.signal, ...quiet })
  return normalise(data)
}

/** Replaces the node's whole route list; the warnings in the answer are the save's. */
export async function saveRoutes(nodeId: number, routes: RouteInput[]): Promise<RoutesView> {
  const { data } = await client.put<RoutesView>(base(nodeId), { routes }, quiet)
  return normalise(data)
}

/** A dry run: validates and computes warnings and the budget, writes nothing. */
export async function checkRoutes(nodeId: number, routes: RouteInput[], opts: ReadOptions = {}): Promise<RoutesView> {
  const { data } = await client.post<RoutesView>(`${base(nodeId)}/check`, { routes }, { signal: opts.signal, ...quiet })
  return normalise(data)
}

export function routeErrorOf(err: unknown): RouteError | null {
  if (!isAxiosError(err) || !err.response) return null
  const body = (err.response.data ?? {}) as Record<string, unknown>
  return {
    status: err.response.status,
    code: typeof body.code === 'string' ? body.code : '',
    params: typeof body.params === 'object' && body.params !== null ? body.params as Record<string, unknown> : {},
    message: typeof body.error === 'string' ? body.error : err.message,
  }
}

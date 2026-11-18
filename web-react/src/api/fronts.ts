import { isAxiosError } from 'axios'
import { client } from './client'
import type { ReadOptions } from './requestOptions'

// SNI-shared ports (handler/admin_front.go, adminGroup only). Every call is
// silent (`_skipErrorToast`): a refusal carries a stable `code` and parameters,
// which the editor words itself and shows beside the form.

export type FrontTargetKind = 'listener' | 'forward' | 'link' | 'external'

export interface FrontTarget {
  kind: FrontTargetKind
  /** The listener's node, or the landing node of a relay line. */
  node_id?: number
  /** The relay line of a forward target. */
  line_id?: string
  link_id?: number
  /** External site: a lowercase name or an IP address. */
  host?: string
  port?: number
}

export interface FrontListener { node_id: number; loopback_port: number }
export interface FrontRoute { server_names: string[]; target: FrontTarget }

export interface Front {
  id: number
  port: number
  listeners: FrontListener[]
  routes: FrontRoute[]
  default: FrontTarget
}

/** Why a server cannot run fronts; absent when it can. */
export type FrontSupportReason = 'not_node' | 'engine_not_xray' | 'not_running_xray' | 'no_capability' | 'no_agent'

/** A saved front that is not being sent to the server, and why. */
export interface FrontWithheld {
  front_id: number
  port: number
  /** no_routes, listener_unfit, listener_port, listener_not_behind or contract. */
  reason: string
  detail?: string
}

export interface FrontsView {
  support: { supported: boolean; reason?: FrontSupportReason }
  withheld: FrontWithheld[]
  fronts: Front[]
  /** Fronts one server may hold. */
  max: number
  defaults: { port: number; loopback_low: number; loopback_high: number; probe_low: number; probe_high: number }
}

export interface FrontInput {
  port: number
  listeners: FrontListener[]
  routes: FrontRoute[]
  default: { host: string; port: number }
  /** The explicit go-ahead to move a listener that holds the port today. */
  confirm_move_listeners: boolean
}

export interface FrontListenerOption {
  node_id: number
  name: string
  protocol: string
  port: number
  enabled: boolean
  frontable: boolean
  /** Why it cannot sit behind a front: no_sni, udp, no_tls, bad_settings, separator. */
  reason?: string
  security?: 'tls' | 'reality'
  /** The names the listener already serves; a pre-fill for its route. */
  server_names: string[]
  front_id?: number
  /** Set when the listener is a tunnel line's hidden listener. */
  parent_node_id?: number
  parent_line_id?: string
  parent_name?: string
  line_name?: string
}

export interface FrontLineOption {
  node_id: number
  node_name: string
  line_id: string
  line_name: string
  port: number
  enabled: boolean
  /** The landing's own server names; a pre-fill for the route. */
  server_names: string[]
  front_id?: number
}

export interface FrontLinkOption {
  link_id: number
  entry_panel_id: number
  server_name: string
  port: number
  front_id?: number
}

export interface FrontOptions { listeners: FrontListenerOption[]; lines: FrontLineOption[]; links?: FrontLinkOption[] }

export type PortKind = 'listener' | 'forward' | 'link' | 'link_reserved' | 'link_planned' | 'front' | 'front_loopback' | 'probe_loopback' | 'probe_range' | 'reserved'

export interface PortBehind {
  server_names: string[]
  target: FrontTarget
  label: string
  loopback_port?: number
  is_default?: boolean
  /** Set when the target is a tunnel line's hidden listener. */
  parent_node_id?: number
  parent_name?: string
  line_name?: string
}

export interface PortUsage {
  port: number
  /** Last port of a range entry (Node's internal probe sockets). */
  range_high?: number
  bind: 'public' | 'loopback'
  listen?: string
  kind: PortKind
  node_id?: number
  node_name?: string
  /** Set when the listener is a tunnel line's hidden listener. */
  parent_node_id?: number
  parent_name?: string
  line_name?: string
  /** A hidden tunnel listener whose line is gone. */
  orphan?: boolean
  line_id?: string
  peer_panel_id?: number
  front_id?: number
  protocol?: string
  label: string
  enabled: boolean
  /** A relay line on a shared port that no route names: it is not sent yet. */
  unrouted?: boolean
  behind?: PortBehind[]
  /** Why a saved front is not being sent (a support reason or a withheld reason). */
  inactive?: string
}

export interface FrontError {
  status: number
  code: string
  params: Record<string, unknown>
  message: string
}

const quiet = { _skipErrorToast: true } as const
const base = (panelId: number) => `/admin/servers/${panelId}`

export async function getFronts(panelId: number, opts: ReadOptions = {}): Promise<FrontsView> {
  const { data } = await client.get<FrontsView>(`${base(panelId)}/fronts`, { signal: opts.signal, ...quiet })
  return { ...data, fronts: data.fronts ?? [], withheld: data.withheld ?? [] }
}

export async function getFrontOptions(panelId: number, opts: ReadOptions = {}): Promise<FrontOptions> {
  const { data } = await client.get<FrontOptions>(`${base(panelId)}/fronts/options`, { signal: opts.signal, ...quiet })
  return { listeners: data.listeners ?? [], lines: data.lines ?? [] }
}

export async function getServerPorts(panelId: number, opts: ReadOptions = {}): Promise<PortUsage[]> {
  const { data } = await client.get<{ ports: PortUsage[] | null }>(`${base(panelId)}/ports`, { signal: opts.signal, ...quiet })
  return data.ports ?? []
}

/** A free loopback port to pre-fill; nothing is stored. */
export async function suggestLoopbackPort(panelId: number): Promise<number> {
  const { data } = await client.get<{ port: number }>(`${base(panelId)}/fronts/suggest`, { params: { what: 'loopback' }, ...quiet })
  return data.port
}

/** A camouflage site to pre-fill; nothing is stored. */
export async function suggestCamouflage(panelId: number): Promise<{ host: string; port: number }> {
  const { data } = await client.get<{ host: string; port: number }>(`${base(panelId)}/fronts/suggest`, { params: { what: 'camouflage' }, ...quiet })
  return data
}

/** A free public port on the server, to pre-fill; nothing is stored. */
export async function suggestPort(panelId: number): Promise<number> {
  const { data } = await client.get<{ port: number }>(`${base(panelId)}/fronts/suggest`, { params: { what: 'port' }, ...quiet })
  return data.port
}

export interface FrontSaved { front: Front; warnings: string[] }

export async function createFront(panelId: number, input: FrontInput): Promise<FrontSaved> {
  const { data } = await client.post<FrontSaved>(`${base(panelId)}/fronts`, input, quiet)
  return data
}

export async function updateFront(panelId: number, frontId: number, input: FrontInput): Promise<FrontSaved> {
  const { data } = await client.put<FrontSaved>(`${base(panelId)}/fronts/${frontId}`, input, quiet)
  return data
}

/** Deleting a front that has listeners or lines behind it is a confirmed action: send confirm once the admin has agreed. */
export async function deleteFront(panelId: number, frontId: number, confirm = false): Promise<void> {
  await client.delete(`${base(panelId)}/fronts/${frontId}`, { ...quiet, params: confirm ? { confirm: 'true' } : undefined })
}

export function frontErrorOf(err: unknown): FrontError | null {
  if (!isAxiosError(err) || !err.response) return null
  const body = (err.response.data ?? {}) as Record<string, unknown>
  return {
    status: err.response.status,
    code: typeof body.code === 'string' ? body.code : '',
    params: typeof body.params === 'object' && body.params !== null ? body.params as Record<string, unknown> : {},
    message: typeof body.error === 'string' ? body.error : err.message,
  }
}

/** Server names as typed: separated by commas, spaces or new lines. */
export function parseServerNames(text: string): string[] {
  return text.split(/[\s,，;；]+/).map(s => s.trim()).filter(Boolean)
}

/**
 * The translation key and parameters for a refusal. A confirmation is worded
 * per action (join, disable, engine, delete, save); lists are joined for the
 * sentence.
 */
export function frontErrorWording(e: FrontError): { key: string; values: Record<string, unknown> } {
  // A list the server leaves out is empty: say so rather than show a bare placeholder.
  const values: Record<string, unknown> = { nodes: '—', leaving_listeners: '—', leaving_lines: '—', tunnels: '—', routes: '—', names: '—', ports: '—' }
  for (const [k, v] of Object.entries(e.params)) values[k] = Array.isArray(v) ? v.join(', ') : v
  const action = typeof e.params.action === 'string' ? e.params.action : ''
  const code = e.code === 'confirm_required' && action ? `confirm_required_${action}` : e.code
  return { key: `admin:servers.fronts.error.${code}`, values: { ...values, defaultValue: e.message } }
}

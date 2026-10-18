import { client } from './client'

export interface VlessRouteLine {
  id?: string
  name: string
  only_line_name?: boolean
  entry_relay_id?: string
  outbound_tag: string
  enabled: boolean
  route?: number
  network?: '' | 'tcp' | 'udp'
  protocols?: string[]
  ip?: string[]
  domain?: string[]
  port?: string
}

const cleanList = (values?: string[]) => [...new Set((values ?? []).map(value => value.trim()).filter(Boolean))]

/** Empty optional match fields preserve the legacy one-rule-per-line payload. */
export function routeLineInput(line: VlessRouteLine): VlessRouteLine {
  const input: VlessRouteLine = { id: line.id, name: line.name.trim(), only_line_name: !!line.only_line_name, entry_relay_id: line.entry_relay_id || '', outbound_tag: line.outbound_tag, enabled: line.enabled }
  if (line.network) input.network = line.network
  const protocols = cleanList(line.protocols).sort()
  const ip = cleanList(line.ip), domain = cleanList(line.domain), port = (line.port || '').trim()
  if (protocols.length) input.protocols = protocols
  if (ip.length) input.ip = ip
  if (domain.length) input.domain = domain
  if (port) input.port = port
  return input
}
export interface RouteState {
  revision: number
  lines: VlessRouteLine[]
  status: string
  error?: string
  binding?: string
}
export interface RouteLinesView {
  node_id: number
  supported: boolean
  state: RouteState
  warnings: string[]
}
export interface RouteOutbound { tag: string; protocol: string }

export interface RouteEditorView extends RouteLinesView {
  outbounds: RouteOutbound[]
  outbounds_error?: 'catalog_unavailable'
}

export async function getRouteLines(nodeId: number, signal?: AbortSignal): Promise<RouteLinesView> {
  return (await client.get<RouteLinesView>(`/admin/nodes/${nodeId}/vless-route-lines`, { signal })).data
}
export async function getRouteEditor(nodeId: number, signal?: AbortSignal): Promise<RouteEditorView> {
  return (await client.get<RouteEditorView>(`/admin/nodes/${nodeId}/vless-route-editor`, { signal })).data
}
export async function saveRouteLines(nodeId: number, revision: number, lines: VlessRouteLine[]): Promise<RouteLinesView> {
  return (await client.put<RouteLinesView>(`/admin/nodes/${nodeId}/vless-route-lines`, {
    revision,
    lines: lines.map(routeLineInput),
  })).data
}

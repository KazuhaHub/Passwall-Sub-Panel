import { client } from './client'

export interface VlessRouteLine {
  id?: string
  name: string
  outbound_tag: string
  enabled: boolean
  route?: number
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
  reason?: string
  state: RouteState
  warnings: string[]
}
export interface RouteOutbound { tag: string; protocol: string; source: string }

export async function getRouteLines(nodeId: number, signal?: AbortSignal): Promise<RouteLinesView> {
  return (await client.get<RouteLinesView>(`/admin/nodes/${nodeId}/vless-route-lines`, { signal })).data
}
export async function getRouteOutbounds(nodeId: number, signal?: AbortSignal): Promise<RouteOutbound[]> {
  return (await client.get<{ items: RouteOutbound[] }>(`/admin/nodes/${nodeId}/vless-route-outbounds`, { signal })).data.items
}
export async function saveRouteLines(nodeId: number, revision: number, lines: VlessRouteLine[]): Promise<RouteLinesView> {
  return (await client.put<RouteLinesView>(`/admin/nodes/${nodeId}/vless-route-lines`, {
    revision,
    lines: lines.map(({ id, name, outbound_tag, enabled }) => ({ id, name, outbound_tag, enabled })),
  })).data
}

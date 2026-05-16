import { client } from './client'
import type { ListResponse, Node, UnmanagedInbound } from './types'

export interface AdminInbound {
  id: number
  remark: string
  enable: boolean
  listen: string
  port: number
  protocol: string
  settings: string
  stream_settings: string
  sniffing: string
  allocate: string
  expiry_time: number
}

export interface ImportNodeRequest {
  panel_id: number
  inbound_id: number
  display_name: string
  server_address: string
  flow?: string
  region: string
  tags?: string[]
  sort_order?: number
}

export interface UpdateNodeMetadataRequest {
  display_name?: string
  server_address?: string
  flow?: string
  region?: string
  tags?: string[]
  sort_order?: number
}

export interface InboundSpec {
  remark: string
  enable: boolean
  listen: string
  port: number
  protocol: string
  settings: string
  stream_settings: string
  sniffing: string
  allocate: string
  expiry_time?: number
}

export interface CreateInboundRequest {
  panel_id: number
  display_name: string
  server_address: string
  flow?: string
  region: string
  tags?: string[]
  sort_order?: number
  inbound: InboundSpec
}

export interface RealityKeypair {
  private_key: string
  public_key: string
  short_id: string
}

export async function listNodes() {
  const { data } = await client.get<{ items: Node[] }>('/admin/nodes')
  return data.items
}

export async function getNode(id: number) {
  const { data } = await client.get<{
    node: Node
    inbound?: AdminInbound
    clients: unknown[]
    inbound_error?: string
    clients_error?: string
  }>(`/admin/nodes/${id}`)
  return data
}

export async function importNode(req: ImportNodeRequest) {
  const { data } = await client.post<Node>('/admin/nodes/import', req)
  return data
}

export async function createInbound(req: CreateInboundRequest) {
  const { data } = await client.post<Node | { queued: true }>('/admin/nodes', req)
  return data
}

export async function generateRealityKeypair() {
  const { data } = await client.post<RealityKeypair>('/admin/nodes/generate-reality-keypair')
  return data
}

export async function updateNodeMetadata(id: number, req: UpdateNodeMetadataRequest) {
  const { data } = await client.put<Node>(`/admin/nodes/${id}/metadata`, req)
  return data
}

export async function updateInboundConfig(id: number, req: InboundSpec) {
  await client.put(`/admin/nodes/${id}/inbound`, req)
}

export async function setNodeEnabled(id: number, enabled: boolean) {
  await client.post(`/admin/nodes/${id}/set-enabled`, { enabled })
}

export async function deleteNode(id: number) {
  await client.delete(`/admin/nodes/${id}`)
}

// detachNode stops managing the node without deleting the upstream inbound.
// Panel-created clients are removed from 3X-UI; the inbound itself and any
// unmanaged clients are preserved. Use when an admin wants to release a
// shared inbound back to its non-panel users.
export async function detachNode(id: number) {
  await client.post(`/admin/nodes/${id}/detach`)
}

export interface NodeReorderItem {
  id: number
  sort_order: number
}

export async function reorderNodes(items: NodeReorderItem[]) {
  await client.put('/admin/nodes/reorder', { items })
}

export async function listUnmanagedInbounds() {
  const { data } = await client.get<ListResponse<UnmanagedInbound>>('/admin/nodes/unmanaged')
  return data
}

export async function claimClient(req: {
  user_id: number
  panel_id: number
  inbound_id: number
  client_email: string
  client_uuid?: string
}) {
  await client.post('/admin/nodes/-/claim', req)
}

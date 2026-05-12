import { client } from './client'
import type { ListResponse, Node, UnmanagedInbound } from './types'

export interface ImportNodeRequest {
  panel_name: string
  inbound_id: number
  display_name: string
  server_address: string
  region: string
  tags?: string[]
  sort_order?: number
}

export interface UpdateNodeMetadataRequest {
  display_name?: string
  server_address?: string
  region?: string
  tags?: string[]
  sort_order?: number
}

export async function listNodes() {
  const { data } = await client.get<{ items: Node[] }>('/admin/nodes')
  return data.items
}

export async function getNode(id: number) {
  const { data } = await client.get<{ node: Node; clients: unknown[] }>(`/admin/nodes/${id}`)
  return data
}

export async function importNode(req: ImportNodeRequest) {
  const { data } = await client.post<Node>('/admin/nodes/import', req)
  return data
}

export async function updateNodeMetadata(id: number, req: UpdateNodeMetadataRequest) {
  const { data } = await client.put<Node>(`/admin/nodes/${id}/metadata`, req)
  return data
}

export async function setNodeEnabled(id: number, enabled: boolean) {
  await client.post(`/admin/nodes/${id}/set-enabled`, { enabled })
}

export async function deleteNode(id: number) {
  await client.delete(`/admin/nodes/${id}`)
}

export async function listUnmanagedInbounds() {
  const { data } = await client.get<ListResponse<UnmanagedInbound>>('/admin/nodes/unmanaged')
  return data
}

export async function claimClient(req: {
  user_id: number
  panel_name: string
  inbound_id: number
  client_email: string
  client_uuid: string
}) {
  await client.post('/admin/nodes/-/claim', req)
}

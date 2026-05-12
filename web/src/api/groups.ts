import { client } from './client'
import type { Group, ListResponse, Layout, TagFilter } from './types'

export async function listGroups() {
  const { data } = await client.get<ListResponse<Group>>('/admin/groups')
  return data
}

export async function getGroup(id: number) {
  const { data } = await client.get<Group>(`/admin/groups/${id}`)
  return data
}

export async function createGroup(req: {
  slug: string
  name: string
  tag_filter?: TagFilter
  layout?: Layout
  remark?: string
}) {
  const { data } = await client.post<Group>('/admin/groups', req)
  return data
}

export async function updateGroup(
  id: number,
  req: { name?: string; tag_filter?: TagFilter; remark?: string },
) {
  const { data } = await client.put<{ group: Group; resync_errors?: string[] }>(
    `/admin/groups/${id}`,
    req,
  )
  return data
}

export async function updateGroupLayout(id: number, layout: Layout) {
  const { data } = await client.put<Group>(`/admin/groups/${id}/layout`, { layout })
  return data
}

export async function deleteGroup(id: number) {
  await client.delete(`/admin/groups/${id}`)
}

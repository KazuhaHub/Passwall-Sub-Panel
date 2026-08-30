import { client } from './client'
import type { Group, Layout, ListResponse, TagFilter } from './types'

export interface GroupListParams {
  page?: number
  page_size?: number
  keyword?: string
  sort_by?: string
  sort_dir?: 'asc' | 'desc'
}

export async function listGroups(params: GroupListParams = {}, signal?: AbortSignal) {
  // Callers that don't pass page params want "everything visible" —
  // small lists are the typical case. Default to a generous page_size
  // that the backend will clamp to 200.
  const merged = { page: 1, page_size: 200, ...params }
  const { data } = await client.get<ListResponse<Group>>('/admin/groups', { params: merged, signal })
  return data
}

// Settings pickers need the complete group catalogue, not just the first
// backend-clamped page. Keep the pagination detail here so every caller gets
// the same stable id-ordered list and no form silently omits group 201+.
export async function listAllGroups(signal?: AbortSignal) {
  const pageSize = 200
  const items: Group[] = []
  let page = 1
  let total = 0

  do {
    const data = await listGroups({ page, page_size: pageSize, sort_by: 'id', sort_dir: 'asc' }, signal)
    items.push(...data.items)
    total = data.total
    if (data.items.length === 0) break
    page += 1
  } while (items.length < total)

  return items
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
  require_2fa?: boolean
}) {
  const { data } = await client.post<Group>('/admin/groups', req)
  return data
}

export async function updateGroup(
  id: number,
  req: { name?: string; tag_filter?: TagFilter; remark?: string; require_2fa?: boolean },
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

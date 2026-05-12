import { client } from './client'
import type { CreateUserRequest, CreateUserResponse, ListResponse, User } from './types'

export interface UserListParams {
  page?: number
  page_size?: number
  search?: string
  group_id?: number
  enabled?: boolean
}

export async function listUsers(params: UserListParams = {}) {
  const { data } = await client.get<ListResponse<User>>('/admin/users', { params })
  return data
}

export async function getUser(id: number) {
  const { data } = await client.get<User>(`/admin/users/${id}`)
  return data
}

export async function createUser(req: CreateUserRequest) {
  const { data } = await client.post<CreateUserResponse>('/admin/users', req)
  return data
}

export async function deleteUser(id: number) {
  await client.delete(`/admin/users/${id}`)
}

export async function resetSubToken(id: number) {
  const { data } = await client.post<{ sub_token: string; sub_url: string }>(
    `/admin/users/${id}/reset-sub-token`,
  )
  return data
}

export async function resetUUID(id: number) {
  const { data } = await client.post<{ uuid: string }>(`/admin/users/${id}/reset-uuid`)
  return data
}

export async function setEnabled(id: number, enabled: boolean) {
  await client.post(`/admin/users/${id}/set-enabled`, { enabled })
}

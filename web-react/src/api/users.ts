import { client } from './client'
import type {
  CreateUserRequest,
  CreateUserResponse,
  ListResponse,
  ResetPeriod,
  Role,
  User,
} from './types'

export interface UpdateUserRequest {
  group_id?: number
  role?: Role
  expire_at?: string | null
  clear_expire?: boolean
  traffic_limit_gb?: number
  traffic_reset_period?: ResetPeriod
  remark?: string
  display_name?: string
  email?: string
}

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

export async function updateUser(id: number, req: UpdateUserRequest) {
  const { data } = await client.put<User>(`/admin/users/${id}`, req)
  return data
}

export async function deleteUser(id: number) {
  await client.delete(`/admin/users/${id}`)
}

export async function setEnabled(id: number, enabled: boolean, reason?: string) {
  await client.post(`/admin/users/${id}/set-enabled`, { enabled, reason })
}

export async function resetCredentials(id: number) {
  const { data } = await client.post<{ sub_token: string; sub_url: string; uuid: string }>(
    `/admin/users/${id}/reset-credentials`,
  )
  return data
}

export async function resetPassword(id: number, password?: string) {
  const { data } = await client.post<{ password: string }>(
    `/admin/users/${id}/reset-password`,
    { password: password ?? '' },
  )
  return data
}

export async function resetEmergencyUsage(id: number) {
  await client.post(`/admin/users/${id}/reset-emergency-usage`)
}

export async function getUserRules(id: number) {
  const { data } = await client.get<{ personal_rules: string }>(`/admin/users/${id}/rules`)
  return data.personal_rules || ''
}

export async function updateUserRules(id: number, personalRules: string) {
  await client.put(`/admin/users/${id}/rules`, { personal_rules: personalRules })
}

import { client } from './client'
import type {
  CreateUserRequest,
  CreateUserResponse,
  ListResponse,
  PasskeyCredential,
  ResetPeriod,
  Role,
  User,
} from './types'

export interface UpdateUserRequest {
  group_id?: number
  role?: Role
  expire_at?: string | null
  /** YYYY-MM-DD calendar date; interpreted as end-of-day in the panel
   *  timezone server-side. Preferred over expire_at for a picked date. */
  expire_date?: string
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
  /** Legacy alias; new callers should pass `keyword`. The handler
   * coalesces the two server-side. */
  search?: string
  keyword?: string
  sort_by?: string
  sort_dir?: 'asc' | 'desc'
  group_id?: number
  enabled?: boolean
}

export async function listUsers(params: UserListParams = {}, signal?: AbortSignal) {
  const { data } = await client.get<ListResponse<User>>('/admin/users', { params, signal })
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

export async function setServiceStatus(id: number, enabled: boolean, reason?: string, detail?: string) {
  await client.post(`/admin/users/${id}/set-service-status`, { enabled, reason, detail })
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

// reset2FA is the admin break-glass: clears a user's 2FA so they can log in with
// just their password and re-enroll (used when they lost their authenticator).
export async function reset2FA(id: number) {
  await client.post(`/admin/users/${id}/reset-2fa`)
}

// regenerateUser2FARecovery rotates a user's recovery codes (admin break-glass for
// a user who still has 2FA on but lost their backup codes) and returns the fresh
// set ONCE for the admin to relay securely. The user's 2FA stays enabled.
export async function regenerateUser2FARecovery(id: number) {
  const { data } = await client.post<{ recovery_codes: string[] }>(
    `/admin/users/${id}/2fa/recovery/regenerate`,
  )
  return data.recovery_codes ?? []
}

export async function unlinkSSO(id: number) {
  await client.post(`/admin/users/${id}/unlink-sso`)
}

// Admin passkey management (v3.7.0). Admins can view and revoke a user's
// passkeys (break-glass for a lost/compromised device) but cannot enroll on
// their behalf — enrollment needs the user's own authenticator.
export async function listUserPasskeys(id: number) {
  const { data } = await client.get<{ passkeys: PasskeyCredential[] }>(`/admin/users/${id}/passkeys`)
  return data.passkeys ?? []
}

export async function revokeUserPasskey(id: number, passkeyId: number) {
  await client.delete(`/admin/users/${id}/passkeys/${passkeyId}`)
}

export async function revokeAllUserPasskeys(id: number) {
  const { data } = await client.delete<{ revoked: number }>(`/admin/users/${id}/passkeys`)
  return data.revoked
}

export async function getUserRules(id: number) {
  const { data } = await client.get<{ personal_rules: string }>(`/admin/users/${id}/rules`)
  return data.personal_rules || ''
}

export async function updateUserRules(id: number, personalRules: string) {
  await client.put(`/admin/users/${id}/rules`, { personal_rules: personalRules })
}

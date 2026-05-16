import { client } from './client'

export interface SubImportClient {
  name: string
  platforms: string[]
  render_format: 'mihomo' | 'sing-box'
  import_url_template: string
  install_url: string
  enabled: boolean
  sort: number
  /** Platforms (windows/macos/linux/ios/android) for which this client should
   *  be rendered as the hero CTA. The user portal detects the visitor's
   *  device and shows the first enabled client whose recommended_for matches.
   *  Empty = never the hero (still listed under "更多客户端"). */
  recommended_for?: string[]
}

export interface QuickLink {
  label: string
  url: string
  new_window: boolean
  enabled: boolean
  sort: number
}

export interface GlobalAnnouncement {
  enabled: boolean
  title: string
  content: string
  level: 'info' | 'warning' | 'danger'
  popup: boolean
  updated_at: string
}

export interface MeProfile {
  id: number
  display_name?: string
  upn: string
  email?: string
  sub_url: string
  /** Admin-configured subscription auto-update interval in hours (default 24).
   *  Frontend converts to minutes for CMfA-style `update-interval=` URI params. */
  sub_update_interval_hours?: number
  sub_import_clients?: SubImportClient[]
  sub_import_tutorial_url?: string
  quick_links?: QuickLink[]
  global_announcement?: GlobalAnnouncement | null
  expire_at?: string | null
  traffic_limit_bytes: number
  traffic_reset_period: string
  enabled: boolean
  can_change_password: boolean
  can_edit_personal_rules: boolean
  emergency_access: {
    enabled: boolean
    available: boolean
    status?: 'available' | 'active' | 'no_quota' | 'not_eligible' | 'disabled' | 'invalid_settings' | 'user_not_found' | string
    reason?: string
    duration_hours: number
    max_count: number
    used_count: number
    remaining: number
    emergency_until?: string | null
    /** Per-window traffic cap in bytes; 0 means unlimited (only time/count limits apply). */
    quota_bytes: number
    /** Bytes consumed during the currently-active window. Always 0 when no window is active. */
    used_bytes: number
  }
}

export async function useEmergencyAccess() {
  const { data } = await client.post<{
    expire_at?: string
    extended_from?: string
    extended_until?: string
    /** @deprecated alias of extended_until — kept for backwards compatibility */
    until?: string
    used_count: number
    max_count: number
    remaining: number
    emergency_until?: string
    quota_bytes?: number
    used_bytes?: number
    sync_pending?: boolean
  }>('/user/me/emergency-access')
  return data
}

export async function getMyProfile() {
  const { data } = await client.get<MeProfile>('/user/me')
  return data
}

export async function changeMyPassword(oldPassword: string, newPassword: string) {
  await client.post('/user/me/change-password', { old_password: oldPassword, new_password: newPassword })
}

export async function getMyRules() {
  const { data } = await client.get<{ personal_rules: string }>('/user/me/rules')
  return data.personal_rules || ''
}

export async function updateMyRules(personalRules: string) {
  await client.put('/user/me/rules', { personal_rules: personalRules })
}

export async function resetMyCredentials() {
  const { data } = await client.post<{ sub_token: string; sub_url: string; uuid: string }>(
    '/user/me/reset-credentials',
  )
  return data
}

import type {
  PublicKeyCredentialCreationOptionsJSON,
  RegistrationResponseJSON,
} from '@simplewebauthn/browser'

import { client } from './client'
import type { PasskeyCredential } from './types'

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
  /** Icon source: "http(s)://…" → image, "mui:Name" → built-in icon,
   *  anything else → literal text (emoji). Empty = no icon. */
  icon: string
  description: string
  /** Optional section name for grouping on the portal. */
  group: string
  highlight: boolean
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
  /** Resolved subscription profile name per the admin-configured
   *  SubProfileNameTemplate. Backend renders the placeholders server-side
   *  so the deep-link &name= and the response Profile-Title header always
   *  agree. Frontend should treat this as the canonical profile name. */
  profile_name?: string
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
  /** Admin-configured TrafficHistoryDays. Drives which traffic-chart range
   *  options the portal exposes (a 90-day retention hides "last 6 months"
   *  / "last 1 year"). 0 = no cap. */
  traffic_history_days?: number
  enabled: boolean
  can_change_password: boolean
  can_edit_personal_rules: boolean
  /** totp_available: the admin has enabled 2FA panel-wide AND this account has a
   *  local password (SSO-only accounts can't enroll). totp_enabled: current state. */
  totp_available?: boolean
  totp_enabled?: boolean
  /** passkey_available: admin enabled passkeys + account has a local password.
   *  passkey_enabled: at least one is registered. passkey_credentials: the list. */
  passkey_available?: boolean
  passkey_enabled?: boolean
  passkey_credentials?: PasskeyCredential[]
  /** must_enroll_2fa: the account is required (per-user / group / staff-wide) to
   *  set up a second factor but hasn't. The panel is gated until it does. */
  must_enroll_2fa?: boolean
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
  // Skip the global error toast: saveRules() localizes the failure itself
  // (the backend's "disabled by admin" string is hardcoded English).
  await client.put('/user/me/rules', { personal_rules: personalRules }, { _skipErrorToast: true })
}

export async function resetMyCredentials() {
  const { data } = await client.post<{ sub_token: string; sub_url: string; uuid: string }>(
    '/user/me/reset-credentials',
  )
  return data
}

/** One node's availability as shown to the end user. Sanitized server-side:
 *  name + region + a coarse status only — no host metrics, no error detail. */
export interface MyNodeStatus {
  name: string
  region: string
  /** "ok" = up, "down" = unreachable/inbound gone, "unknown" = not yet probed. */
  status: 'ok' | 'down' | 'unknown'
  checked_at?: string
}

export async function getMyServerStatus() {
  const { data } = await client.get<{ nodes: MyNodeStatus[] }>('/user/me/server-status')
  return data.nodes
}

// ---- Two-factor authentication (TOTP) self-service ----

// begin2FA starts enrollment: returns the otpauth URL (for the QR) + raw secret
// (for manual entry). The secret is stored disabled until confirmed via enable2FA.
export async function begin2FA() {
  const { data } = await client.post<{ otpauth_url: string; secret: string }>('/user/me/2fa/begin')
  return data
}

// enable2FA confirms enrollment with a code and returns one-time recovery codes
// to show ONCE.
export async function enable2FA(code: string) {
  const { data } = await client.post<{ recovery_codes: string[] }>(
    '/user/me/2fa/enable',
    { code },
    { _skipErrorToast: true },
  )
  return data.recovery_codes
}

// disable2FA turns 2FA off, requiring a valid current TOTP or recovery code.
export async function disable2FA(code: string) {
  await client.post('/user/me/2fa/disable', { code }, { _skipErrorToast: true })
}

// regenerate2FARecovery rotates the user's recovery codes (requires a current
// TOTP or recovery code as step-up proof) and returns the fresh set to show ONCE.
export async function regenerate2FARecovery(code: string) {
  const { data } = await client.post<{ recovery_codes: string[] }>(
    '/user/me/2fa/recovery/regenerate',
    { code },
    { _skipErrorToast: true },
  )
  return data.recovery_codes
}

// ---- Passkeys (WebAuthn) self-service ----

export async function beginPasskeyEnroll(): Promise<{
  session_id: string
  publicKey: PublicKeyCredentialCreationOptionsJSON
}> {
  const { data } = await client.post('/user/me/passkeys/begin', {}, { _skipErrorToast: true })
  return data
}

// finishPasskeyEnroll posts the attestation (body) with the session id + chosen
// name in the query, and returns the updated credential list.
export async function finishPasskeyEnroll(
  sessionId: string,
  name: string,
  attestation: RegistrationResponseJSON,
): Promise<PasskeyCredential[]> {
  const q = `session=${encodeURIComponent(sessionId)}&name=${encodeURIComponent(name)}`
  const { data } = await client.post<{ passkeys: PasskeyCredential[] }>(
    `/user/me/passkeys/finish?${q}`,
    attestation,
    { _skipErrorToast: true },
  )
  return data.passkeys
}

export async function listPasskeys(): Promise<PasskeyCredential[]> {
  const { data } = await client.get<{ passkeys: PasskeyCredential[] }>('/user/me/passkeys')
  return data.passkeys
}

export async function renamePasskey(id: number, name: string) {
  await client.patch(`/user/me/passkeys/${id}`, { name }, { _skipErrorToast: true })
}

export async function deletePasskey(id: number) {
  await client.delete(`/user/me/passkeys/${id}`, { _skipErrorToast: true })
}

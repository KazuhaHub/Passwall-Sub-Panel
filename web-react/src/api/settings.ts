import { client } from './client'
import type { LoginMode } from './types'

export interface SubClientRule {
  name: string
  keywords: string[]
  render_format: 'mihomo' | 'sing-box' | 'uri-list'
  enabled: boolean
}

export interface SubImportClient {
  name: string
  platforms: Array<'windows' | 'macos' | 'linux' | 'ios' | 'android' | 'other'>
  render_format: 'mihomo' | 'sing-box' | 'uri-list'
  import_url_template: string
  install_url: string
  enabled: boolean
  sort: number
  /** Per-platform hero recommendation: the user portal detects the visitor's
   *  device and shows the first enabled client whose recommended_for includes
   *  that platform. Empty = never hero (just appears under "更多客户端"). */
  recommended_for?: Array<'windows' | 'macos' | 'linux' | 'ios' | 'android' | 'other'>
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
  /** When true, the portal shows the announcement as a modal on first
   *  visit after `updated_at` changes. Dismissal state lives in the
   *  browser only (localStorage), never persisted server-side. */
  popup: boolean
  updated_at: string
}

export interface UISettings {
  login_mode: LoginMode
  site_title: string
  app_title: string
  icon_url: string
  logo_url: string
  logo_url_dark: string
  email_domain: string
  audit_retention_days: number
  sub_base_url: string
  /** IANA timezone name used for system-level time math (traffic resets,
   *  expire_at, default chart bucketing). Empty falls back to server local. */
  timezone: string
  cron_traffic_pull_minutes: number
  cron_reconcile_minutes: number
  /** Concurrency cap for parallel ListInbounds fan-out during traffic poll
   *  and reconcile. 0 / unset falls back to 8; values > 64 clamp down. */
  max_panel_concurrency: number
  jwt_access_ttl_minutes: number
  jwt_refresh_ttl_minutes: number
  jwt_issuer: string
  sub_per_ip_per_min: number
  login_per_ip_per_min: number
  sync_task_retention_days: number
  disallow_user_local_login: boolean
  disallow_user_password_change: boolean
  allow_user_personal_rules: boolean
  emergency_access_enabled: boolean
  emergency_access_hours: number
  emergency_access_max_count: number
  emergency_access_quota_gb: number
  sub_path: string
  sub_client_rules: SubClientRule[]
  sub_import_clients: SubImportClient[]
  sub_import_tutorial_url: string
  sub_log_retention_days: number
  sub_block_auto_disable: boolean
  sub_block_auto_disable_count: number
  sub_update_interval_hours: number
  /** When true, prepend the Unicode flag of the node's Region (ISO 3166-1
   *  alpha-2) to the rendered node name in subscriptions. Off by default. */
  sub_region_flag_prefix: boolean
  quick_links: QuickLink[]
  global_announcement: GlobalAnnouncement
  footer_text: string
  /** M3 source hex color used as the system-default theme. Empty falls back to the frontend default. */
  theme_color: string
}

export async function getUISettings() {
  const { data } = await client.get<UISettings>('/admin/settings/ui')
  return data
}

export async function putUISettings(s: UISettings) {
  const { data } = await client.put<UISettings>('/admin/settings/ui', s)
  return data
}

// ---- Mail reminders ----
export type MailReminderKind = 'expire_before' | 'expired' | 'traffic_low' | 'traffic_exhausted' | 'account_disabled' | 'account_enabled' | 'announcement'

export interface MailSettings {
  enabled: boolean
  smtp_host: string
  smtp_port: number
  smtp_username: string
  smtp_password?: string
  has_smtp_password: boolean
  from_email: string
  from_name: string
  encryption: 'none' | 'starttls' | 'tls'
  expire_before_days: number
  traffic_remain_percent: number
}

export interface MailTemplate {
  kind: MailReminderKind
  subject: string
  body: string
  enabled: boolean
}

export async function getMailSettings() {
  const { data } = await client.get<{ settings: MailSettings; templates: MailTemplate[] }>('/admin/settings/mail')
  return data
}

export async function putMailSettings(s: MailSettings) {
  const { data } = await client.put<MailSettings>('/admin/settings/mail', s)
  return data
}

export async function putMailTemplate(tpl: MailTemplate) {
  const { data } = await client.put<MailTemplate>(`/admin/settings/mail/templates/${tpl.kind}`, tpl)
  return data
}

export async function previewMailTemplate(tpl: MailTemplate) {
  const { data } = await client.post<{ subject: string; body: string }>(
    `/admin/settings/mail/templates/${tpl.kind}/preview`, tpl)
  return data
}

export async function resetMailTemplate(kind: MailReminderKind) {
  const { data } = await client.post<MailTemplate>(
    `/admin/settings/mail/templates/${kind}/reset`)
  return data
}

export async function sendTestMail(to: string) {
  const { data } = await client.post<{ sent: boolean }>('/admin/settings/mail/test', { to })
  return data
}

// ---- SAML ----
export type SAMLMode = 'auto' | 'manual'

export interface SAMLConfig {
  enabled: boolean
  mode: SAMLMode
  sp: {
    entity_id: string
    acs_url: string
    cert_pem: string
    has_key_pem: boolean
  }
  idp: {
    metadata_url: string
    metadata_refresh_hours: number
  }
  attribute_mapping: {
    upn: string
    email: string
    display_name: string
    groups: string
  }
  admin_group_ids: string[]
  default_group_slug: string
  allow_auto_create: boolean
  /** When true, panel admin role is PRESERVED on SSO login even if the IdP
   *  no longer reports the user in any admin group. Default false makes IdP
   *  admin-group membership authoritative for the admin role — promote AND
   *  demote. Operator role is panel-side and unaffected by this flag. */
  keep_admin_when_not_in_group: boolean
  new_user_defaults: {
    expire_days: number
    traffic_limit_bytes: number
    traffic_reset_period: string
  }
}

export interface SAMLUpdateRequest extends Omit<SAMLConfig, 'sp'> {
  sp: {
    entity_id: string
    acs_url: string
    cert_pem: string
    key_pem: string
  }
}

export async function getSAML() {
  const { data } = await client.get<SAMLConfig>('/admin/settings/saml')
  return data
}

export async function putSAML(req: SAMLUpdateRequest) {
  const { data } = await client.put<{ saved: boolean; reload_error?: string; config: SAMLConfig }>(
    '/admin/settings/saml', req)
  return data
}

export interface SAMLMetadataSummary {
  entity_id: string
  num_signing_certs: number
  signing_cert_expires_at?: string
}

// Server-side verification of an IdP metadata URL. Returns a small summary
// the admin UI shows under the URL input so the operator can confirm the
// URL reaches the intended directory before saving.
export async function fetchSAMLMetadata(url: string) {
  const { data } = await client.post<SAMLMetadataSummary>(
    '/admin/settings/saml/fetch', { url })
  return data
}

// ---- OIDC ----
export interface OIDCConfig {
  enabled: boolean
  issuer_url: string
  client_id: string
  has_client_secret: boolean
  redirect_url: string
  scopes: string[]
  attribute_mapping: {
    username: string
    email: string
    display_name: string
    groups: string
  }
  admin_group_ids: string[]
  default_group_slug: string
  allow_auto_create: boolean
  /** See SAMLConfig.keep_admin_when_not_in_group — when true, preserves a
   *  panel admin's role even after the IdP drops them from every admin
   *  group. Default false = IdP groups authoritative (promote + demote). */
  keep_admin_when_not_in_group: boolean
  new_user_defaults: {
    expire_days: number
    traffic_limit_bytes: number
    traffic_reset_period: string
  }
}

export interface OIDCUpdateRequest extends Omit<OIDCConfig, 'has_client_secret'> {
  client_secret: string
}

export async function getOIDC() {
  const { data } = await client.get<OIDCConfig>('/admin/settings/oidc')
  return data
}

export async function putOIDC(req: OIDCUpdateRequest) {
  const { data } = await client.put<{ saved: boolean; reload_error?: string; config: OIDCConfig }>(
    '/admin/settings/oidc', req)
  return data
}

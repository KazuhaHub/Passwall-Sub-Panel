import { client } from './client'
import type { LoginMode } from './types'

export type SubPlatform = 'windows' | 'macos' | 'linux' | 'ios' | 'android' | 'other'
export type SubRenderFormat = 'mihomo' | 'sing-box' | 'uri-list'

/** One import app nested under a SubClientFamily (v3.3.0). No render_format —
 *  it's the family's, served by UA at fetch time. */
export interface SubClientApp {
  name: string
  platforms: SubPlatform[]
  import_url_template: string
  install_url: string
  enabled: boolean
  sort: number
  /** Per-platform hero recommendation: the portal detects the visitor's device
   *  and shows the first enabled app whose recommended_for includes that
   *  platform. Empty = never hero (just appears under "更多客户端"). */
  recommended_for?: SubPlatform[]
}

/** A UA-detection family in the unified client registry (v3.3.0): keywords +
 *  render format + an enabled gate, owning the import apps shown in the portal.
 *  An app is offered iff its family is enabled AND the app is enabled. */
export interface SubClientFamily {
  name: string
  keywords: string[]
  render_format: SubRenderFormat
  enabled: boolean
  apps: SubClientApp[]
}

export interface QuickLink {
  label: string
  url: string
  /** Icon source, auto-detected by the portal: "http(s)://…" → image,
   *  "mui:Name" → built-in icon, anything else → literal text (emoji). */
  icon: string
  /** Optional one/two-line subtitle under the label. */
  description: string
  /** Optional section name; links sharing a group render under a header.
   *  When no link has a group the portal shows a flat grid. */
  group: string
  /** Visually emphasize the card (featured link). */
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
  /** Authentication-event log retention (days). Default 90, floored at 90. */
  auth_event_retention_days: number
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
  sub_clients: SubClientFamily[]
  /** Client gate mode: "blacklist" (block only disabled families, unknown
   *  passes) or "whitelist" (only matched+enabled passes, unknown blocked). */
  sub_client_filter_mode: 'blacklist' | 'whitelist'
  sub_import_tutorial_url: string
  sub_log_retention_days: number
  sub_block_auto_disable: boolean
  sub_block_auto_disable_count: number
  /** Email the user a warning when they fetch with a blocked client (before
   *  hitting the auto-disable threshold). Off by default. */
  sub_block_notify_user: boolean
  /** Per-user/day cap on the blocked-client warning email. Default 1. */
  sub_block_notify_max_per_day: number
  sub_update_interval_hours: number
  /** Template applied server-side to produce the profile name baked into
   *  Content-Disposition / Profile-Title response headers and the
   *  &name= query param of one-click import deep links. Supports
   *  {{ site_title }}, {{ app_title }}, {{ display_name }}, {{ upn }},
   *  and the composite {{ user }} (display_name with UPN fallback).
   *  Empty falls back to "{{ site_title }} - {{ user }}". */
  sub_profile_name_template: string
  /** When true, prepend the Unicode flag of the node's Region (ISO 3166-1
   *  alpha-2) to the rendered node name in subscriptions. Off by default. */
  sub_region_flag_prefix: boolean
  quick_links: QuickLink[]
  global_announcement: GlobalAnnouncement
  footer_text: string
  /** M3 source hex color used as the system-default theme. Empty falls back to the frontend default. */
  theme_color: string
  /** Notify thresholds (moved out of mail_settings in v9; admin edits them
   *  in the global settings page now, not the mail page). */
  expire_before_days: number
  traffic_remain_percent: number
  /** How many days of traffic history the chart can render. Drives the
   *  retention of the hourly rollup tables; raw 5-min snapshots live for
   *  a fixed short window (~7 days) and aren't admin-tunable. 0 keeps
   *  everything. Renamed from traffic_snapshot_retention_days in
   *  v3.0.0-beta.6 when the rollup pipeline landed. */
  traffic_history_days: number
  /** Auto-prune cutoff for the mail_sent table (Logs → Email tab).
   *  0 disables auto-prune. Default 30. */
  mail_sent_retention_days: number

  // ---- IP geolocation (offline .mmdb region display in access logs) ----
  /** Master toggle. Off by default; resolution is fully offline against a
   *  local .mmdb in <ConfigDir>/geoip/ (no per-IP external calls). */
  geo_ip_enabled: boolean
  /** Active database filename when several .mmdb are present. Empty = first by
   *  name. Only one is ever active (no merging → no conflict). */
  geo_ip_db_file: string
  geo_ip_auto_update: boolean
  /** Updater source. maxmind (GeoLite2-City, recommended) / dbip / ipinfo / custom. */
  geo_ip_update_source: 'maxmind' | 'dbip' | 'ipinfo' | 'custom' | ''
  geo_ip_update_url: string
  /** Database identifier for the chosen source: MaxMind edition id
   *  (GeoLite2-City / paid GeoIP2-City) or IPinfo database stem (ipinfo_lite /
   *  a paid product). Cleared on source change so each source's default applies. */
  geo_ip_update_edition: string
  /** Auto-update cadence in hours (default 12, floored at 1). */
  geo_ip_update_interval_hours: number
  /** PSP-managed certificate automation (v3.6.4). Included here so the settings
   *  page round-trips them on save instead of resetting them to defaults. */
  cert_renew_before_days: number
  cert_renew_check_interval_hours: number
  /** Write-only: sent on PUT (empty = keep existing), never returned. The
   *  presence flag below reports whether one is stored. */
  geo_ip_update_token?: string
  has_geo_ip_update_token: boolean

  // ---- Login security: CAPTCHA + account lockout (v3.7.0) ----
  /** Master toggle for the login-form captcha. Off by default. */
  captcha_enabled: boolean
  /** "image" (self-hosted, CN-safe default) / "turnstile" / "recaptcha" / "hcaptcha". */
  captcha_provider: 'image' | 'turnstile' | 'recaptcha' | 'hcaptcha' | ''
  /** "always" or "after_failures" (show only once an IP/account racks up failures). */
  captcha_trigger: 'always' | 'after_failures' | ''
  /** Failure count (over the lockout window) that trips after_failures mode. */
  captcha_fail_threshold: number
  /** Public site key for token providers (embedded in the login page). */
  captcha_site_key: string
  /** Write-only secret key: sent on PUT (empty = keep existing), never returned.
   *  Image provider needs no keys. */
  captcha_secret_key?: string
  has_captcha_secret_key: boolean
  /** Per-context captcha (v3.7.0): captcha_enabled gates the LOGIN form; these
   *  extend the same provider to the register / forgot forms (always-on). */
  captcha_register_enabled: boolean
  captcha_forgot_enabled: boolean
  /** Master toggle for temporary account lockout after repeated failures. */
  lockout_enabled: boolean
  /** Failures within the window that trigger a lock. */
  lockout_threshold: number
  /** Window (minutes) over which failures are counted. */
  lockout_window_minutes: number
  /** How long (minutes) a tripped lock lasts. */
  lockout_duration_minutes: number
  /** "ip" or "ip_upn" (recommended — lock the IP+username pair). */
  lockout_scope: 'ip' | 'ip_upn' | ''

  // ---- Self-service password recovery (v3.7.0) ----
  /** Master toggle for the forgot-password / reset flow. Needs SMTP configured. */
  password_recovery_enabled: boolean
  /** "link" (a one-time reset URL) or "otp" (a short code the user types). */
  password_recovery_delivery: 'link' | 'otp' | ''

  // ---- Self-service registration (v3.7.0) ----
  registration_enabled: boolean
  /** Positive form of the stored allow_unverified (default true = required). */
  registration_require_email_verification: boolean
  /** Comma-separated allowed email domains; empty = any. */
  registration_email_domains: string
  registration_default_group_id: number
  registration_delivery: 'link' | 'otp' | ''
  /** Quota/expiry a registrant inherits. 0 = unlimited / no expiry. */
  registration_default_traffic_gb: number
  registration_default_expire_days: number

  // ---- Two-factor authentication (TOTP) (v3.7.0) ----
  /** Master switch for 2FA enrollment. Off blocks new enrollment panel-wide but
   *  does NOT strip 2FA from already-enrolled accounts. */
  totp_enabled: boolean

  // ---- Passkeys / WebAuthn (v3.7.0) ----
  /** Allow local accounts to register passkeys on their profile page. */
  passkey_enabled: boolean
  /** Additionally allow usernameless passkey login from the login page. */
  passkey_passwordless: boolean

  // ---- Alternative 2FA verification methods (v3.7.0) ----
  /** Offer a one-time code by email as the 2FA factor (weaker; default off).
   *  TOTP, recovery codes, and an enrolled passkey are always available — a
   *  passkey is itself a second factor, so there is no separate allow toggle. */
  twofa_allow_email: boolean
  /** Per-account cooldown (seconds) between email-code resends. 0 = default 60. */
  twofa_email_resend_cooldown_sec: number
  /** Per-account cooldown (seconds) between signup-verification / password-reset
   *  email-code resends (anti mail-bombing). 0 = default 60. */
  code_resend_cooldown_sec: number
  /** Force every admin/operator (local-password) account to enroll a second
   *  factor before using the panel. */
  require_2fa_for_staff: boolean
}

export async function getUISettings() {
  const { data } = await client.get<UISettings>('/admin/settings/ui')
  return data
}

export async function putUISettings(s: UISettings) {
  const { data } = await client.put<UISettings>('/admin/settings/ui', s)
  return data
}

// ---- Geo IP database (offline .mmdb) status + manual update ----
export interface GeoDBStatus {
  file: string
  type: string
  granularity: string
  build_epoch: number
  active: boolean
  error?: string
}

// Background-updater status. The download runs server-side off the request, so
// the UI triggers it then polls getGeoIPStatus() and watches `update`.
export interface GeoUpdateState {
  updating: boolean
  last_error?: string
  last_file?: string
  last_at?: number // unix seconds of last completion
}

export interface GeoIPStatus {
  enabled: boolean
  dir: string
  active: string
  available: GeoDBStatus[]
  update: GeoUpdateState
}

export async function getGeoIPStatus() {
  const { data } = await client.get<GeoIPStatus>('/admin/settings/geoip/status')
  return data
}

/**
 * Kick off a background download/refresh of the configured source's database.
 * Returns immediately (202); poll getGeoIPStatus() and read `update` for the
 * result. A 409 means an update is already running.
 */
export async function updateGeoIPNow() {
  const { data } = await client.post<{ status: string }>('/admin/settings/geoip/update')
  return data
}

// ---- Mail reminders ----
export type MailReminderKind = 'expire_before' | 'expired' | 'traffic_low' | 'traffic_exhausted' | 'account_disabled' | 'account_enabled' | 'announcement' | 'blocked_client' | 'password_reset' | 'email_verify' | 'login_2fa'

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
  // v9 note: expire_before_days / traffic_remain_percent moved into UISettings
  // (settings KV type='notify'). Admin edits them through the global settings
  // page now.
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
  default_group_slug: string
  allow_auto_create: boolean
  /** Attribute-driven role mapping. Evaluated in order; first match
   *  decides the panel role. Per-rule Keep flag controls demote
   *  behaviour when the rule doesn't fire. See backend
   *  ResolveRoleForSSO for the full matcher. */
  role_rules: SSORoleRule[]
  new_user_defaults: {
    expire_days: number
    traffic_limit_bytes: number
    traffic_reset_period: string
  }
}

/** SSORoleRule: an IdP attribute value -> panel role mapping. Shared by
 *  SAML and OIDC configs.
 *  - attribute: empty string or "groups" matches against the groups
 *    attribute (whatever URN it's configured under). Any other value
 *    is the IdP attribute Name to look up exactly.
 *  - value: matched exactly (case-sensitive, no wildcards).
 *  - role: panel role to assign. Built-in: "admin" / "operator" /
 *    "user". Custom strings are accepted — the panel stores them on
 *    the user row, but middleware only grants elevated access to
 *    admin / operator until the role enum is extended.
 *  - keep: when true, preserve the user's existing panel role on
 *    logins where THIS rule does NOT fire (admin remains admin even
 *    if their group attribute happens to miss this time). Defaults
 *    to false = rule is authoritative both ways. */
export interface SSORoleRule {
  attribute: string
  value: string
  role: string
  keep: boolean
  /** Admin-facing free-form note. Never affects matching — UI label
   *  only, so admins can document what each rule represents. */
  note: string
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
  default_group_slug: string
  allow_auto_create: boolean
  /** Attribute-driven role mapping. See SAMLConfig.role_rules. */
  role_rules: SSORoleRule[]
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

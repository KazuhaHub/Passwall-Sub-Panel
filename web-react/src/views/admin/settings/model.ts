import type { UISettings } from '@/api/settings'
import { normalizeRegistry } from '../subclients/clientRegistry'

/**
 * The sections of the settings page, in the order of the navigation. The values are the `?tab=` values the page has
 * always used, so a saved link keeps working.
 */
export const SECTIONS = ['general', 'security', 'brand', 'subscription', 'portal', 'mail', 'sso', 'legal'] as const
export type SectionKey = (typeof SECTIONS)[number]

export const SECTION_LABEL: Record<SectionKey, string> = {
  general: 'settings.tab_general',
  security: 'settings.tab_security',
  brand: 'settings.tab_brand',
  subscription: 'settings.tab_subscription',
  portal: 'settings.tab_portal',
  mail: 'settings.tab_mail',
  sso: 'settings.tab_sso',
  legal: 'settings.tab_legal',
}

type Keys = readonly (keyof UISettings)[]

/**
 * Which keys of the one panel-wide settings record each section edits. The record is written back whole (the server
 * has one endpoint for it), so a section's save sends the latest saved record with ONLY these keys replaced by the
 * section's draft: a section that was never opened, or one with unsaved edits of its own, is never written from here.
 * Mail, SAML and OIDC are not in this record; each has its own endpoint (see MailSection and the sso/ folder).
 */
export const SECTION_KEYS = {
  general: [
    'timezone', 'cron_traffic_pull_minutes', 'cron_reconcile_minutes', 'node_poll_seconds', 'full_report_seconds',
    'max_panel_concurrency', 'audit_retention_days', 'sync_task_retention_days', 'auth_event_retention_days',
    'traffic_history_days', 'expire_before_days', 'traffic_remain_percent',
    'node_task_offline_reconcile_days', 'node_task_backup_restore_days', 'node_task_result_retention_days',
    'emergency_access_enabled', 'emergency_access_hours', 'emergency_access_max_count', 'emergency_access_quota_gb',
    'cloudflare_ips_auto_fetch',
    'geo_ip_enabled', 'geo_ip_db_file', 'geo_ip_auto_update', 'geo_ip_update_interval_hours', 'geo_ip_update_source',
    'geo_ip_update_edition', 'geo_ip_update_url', 'geo_ip_update_token',
  ],
  security: [
    'login_mode', 'disallow_user_local_login', 'disallow_user_password_change', 'allow_user_personal_rules',
    'captcha_enabled', 'captcha_register_enabled', 'captcha_forgot_enabled', 'captcha_provider', 'captcha_trigger',
    'captcha_fail_threshold', 'captcha_site_key', 'captcha_secret_key',
    'lockout_enabled', 'lockout_threshold', 'lockout_window_minutes', 'lockout_duration_minutes', 'lockout_scope',
    'password_recovery_enabled', 'password_recovery_delivery',
    'registration_enabled', 'registration_require_email_verification', 'registration_delivery',
    'registration_email_domains', 'registration_default_group_id', 'registration_default_traffic_gb',
    'registration_default_expire_days', 'code_resend_cooldown_sec',
    'totp_enabled', 'passkey_enabled', 'passkey_passwordless', 'twofa_allow_email', 'twofa_email_resend_cooldown_sec',
    'require_2fa_for_staff',
    'jwt_issuer', 'jwt_access_ttl_minutes', 'jwt_refresh_ttl_minutes', 'sub_per_ip_per_min', 'login_per_ip_per_min',
  ],
  brand: [
    'site_title', 'app_title', 'footer_text', 'sub_base_url', 'panel_path', 'icon_url', 'logo_url', 'logo_url_dark',
    'theme_color', 'email_domain',
  ],
  subscription: [
    'sub_path', 'sub_import_tutorial_url', 'sub_log_retention_days', 'sub_update_interval_hours',
    'sub_profile_name_template', 'sub_region_flag_prefix', 'sub_block_auto_disable', 'sub_block_auto_disable_count',
    'sub_block_notify_user', 'sub_block_notify_max_per_day',
  ],
  portal: ['quick_links', 'global_announcement'],
  legal: ['legal_enabled', 'legal_disclose_usage'],
} as const satisfies Record<string, Keys>

/**
 * Keys the server accepts but never returns (empty on a write means "keep the stored one"). They are left out of a
 * request while empty, exactly as the old form left them out until they were typed.
 */
export const WRITE_ONLY_KEYS = ['geo_ip_update_token', 'captcha_secret_key'] as const satisfies Keys

/**
 * The server answers `null` for an empty list (Go's nil slice), and an older server omits newer keys. Normalised
 * once at the read boundary so every editor can treat these as present. An explicit value, even an invalid one, is
 * kept so the admin can see and correct it. An empty subscription base URL reads as this page's own origin, the same
 * as before (the Brand section shows it and a save writes it).
 */
export function normalizeSettings(s: UISettings): UISettings {
  return {
    ...s,
    sub_clients: normalizeRegistry(s.sub_clients),
    sub_client_filter_mode: s.sub_client_filter_mode ?? 'blacklist',
    quick_links: s.quick_links ?? [],
    timezone: s.timezone ?? '',
    node_task_offline_reconcile_days: s.node_task_offline_reconcile_days ?? 30,
    node_task_backup_restore_days: s.node_task_backup_restore_days ?? 30,
    node_task_result_retention_days: s.node_task_result_retention_days ?? 90,
    legal_enabled: s.legal_enabled ?? false,
    legal_consent_version: s.legal_consent_version ?? 0,
    legal_disclose_usage: s.legal_disclose_usage ?? false,
    sub_base_url: s.sub_base_url || window.location.origin,
  }
}

/** The DOM id of the control that edits a setting: what the search jumps to. */
export const settingId = (key: string) => `setting-${key}`

export const sameValue = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

/** The keys of one section's draft, as a type. */
export type KeysOf<S extends keyof typeof SECTION_KEYS> = (typeof SECTION_KEYS)[S][number]

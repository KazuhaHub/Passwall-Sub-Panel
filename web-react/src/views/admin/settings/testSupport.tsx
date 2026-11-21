import { vi } from 'vitest'
import type { UISettings } from '@/api/settings'
import { api, installReads } from '../adminTestHarness'

// The link-probe card is another team's, written in the old component set, and needs the old theme: not what these tests are about.
vi.mock('../LinkProbeSettings', () => ({ default: () => null }))

export const list = (items: unknown[]) => ({ items, total: items.length, page: 1, page_size: 200 })

/** A complete settings record as a server would give it: every key a section edits has a value. */
export function uiSettings(over: Partial<UISettings> = {}): UISettings {
  return {
    login_mode: 'dual', site_title: 'Site', app_title: 'App', icon_url: '', logo_url: '', logo_url_dark: '', email_domain: '',
    audit_retention_days: 90, auth_event_retention_days: 90, sub_base_url: 'https://panel.example.test', panel_path: '',
    timezone: 'UTC', cron_traffic_pull_minutes: 5, cron_reconcile_minutes: 5, node_poll_seconds: 30, full_report_seconds: 60,
    node_task_offline_reconcile_days: 30, node_task_backup_restore_days: 30, node_task_result_retention_days: 90,
    max_panel_concurrency: 8, jwt_access_ttl_minutes: 60, jwt_refresh_ttl_minutes: 10080, jwt_issuer: 'passwall-sub-panel',
    sub_per_ip_per_min: 30, login_per_ip_per_min: 10, sync_task_retention_days: 90,
    disallow_user_local_login: false, disallow_user_password_change: false, allow_user_personal_rules: true,
    emergency_access_enabled: false, emergency_access_hours: 1, emergency_access_max_count: 1, emergency_access_quota_gb: 0,
    sub_path: 'sub', sub_clients: [], sub_client_filter_mode: 'blacklist', sub_import_tutorial_url: '', sub_log_retention_days: 30,
    sub_block_auto_disable: false, sub_block_auto_disable_count: 5, sub_block_notify_user: false, sub_block_notify_max_per_day: 1,
    sub_update_interval_hours: 24, sub_profile_name_template: '', sub_region_flag_prefix: false,
    quick_links: [], global_announcement: { enabled: false, title: '', content: '', level: 'info', popup: false, updated_at: '' },
    footer_text: 'Footer', theme_color: '', expire_before_days: 7, traffic_remain_percent: 10, traffic_history_days: 730,
    mail_sent_retention_days: 30,
    geo_anomaly_scope: '', geo_anomaly_max_places: 1, geo_anomaly_max_regions: 0, geo_anomaly_max_cities: 0,
    geo_anomaly_flag_after_polls: 2, geo_anomaly_clear_after_polls: 3, geo_anomaly_min_placed_ratio: 0.8, geo_anomaly_co_travel: '',
    geo_anomaly_allow_anywhere: false, geo_anomaly_ignore_addresses: '', geo_anomaly_ban_enabled: false,
    geo_ip_enabled: false, geo_ip_db_file: '', geo_ip_auto_update: false, geo_ip_update_source: 'dbip', geo_ip_update_url: '',
    geo_ip_update_edition: '', geo_ip_update_interval_hours: 12, cert_renew_before_days: 30, cert_renew_check_interval_hours: 12,
    has_geo_ip_update_token: false,
    captcha_enabled: false, captcha_provider: 'image', captcha_trigger: 'after_failures', captcha_fail_threshold: 3, captcha_site_key: '',
    has_captcha_secret_key: false, captcha_register_enabled: false, captcha_forgot_enabled: false,
    lockout_enabled: false, lockout_threshold: 5, lockout_window_minutes: 15, lockout_duration_minutes: 15, lockout_scope: 'ip_upn',
    password_recovery_enabled: false, password_recovery_delivery: 'link',
    registration_enabled: false, registration_require_email_verification: true, registration_email_domains: '', registration_default_group_id: 0,
    registration_delivery: 'link', registration_default_traffic_gb: 0, registration_default_expire_days: 0,
    totp_enabled: false, passkey_enabled: false, passkey_passwordless: false, twofa_allow_email: false, twofa_email_resend_cooldown_sec: 0,
    code_resend_cooldown_sec: 0, require_2fa_for_staff: false,
    legal_enabled: false, legal_consent_version: 0, legal_disclose_usage: false,
    ...over,
  } as UISettings
}

export const group = { id: 1, slug: 'test', name: 'Test group', tag_filter: { all: true, tags: [], mode: 'all' }, members: 0, remark: '', require_2fa: false }

/** The reads every section may make; a test overrides what it cares about. */
export function reads(over: Record<string, unknown> = {}, settings: Partial<UISettings> = {}) {
  installReads({
    '/admin/settings/ui': uiSettings(settings),
    '/admin/groups': list([group]),
    '/admin/settings/geoip/status': { enabled: false, dir: '', active: '', available: [], update: { updating: false } },
    '/admin/settings/mail': { settings: { enabled: false, smtp_host: '', smtp_port: 587, smtp_username: '', has_smtp_password: false, from_email: '', from_name: '', encryption: 'starttls' }, templates: [] },
    '/admin/settings/saml': samlConfig(),
    '/admin/settings/oidc': oidcConfig(),
    '/admin/legal/terms': legalDoc('terms'),
    '/admin/legal/privacy': legalDoc('privacy'),
    ...over,
  })
}

export const samlConfig = (over: Record<string, unknown> = {}) => ({
  enabled: false, mode: 'auto', sp: { entity_id: '', acs_url: '', cert_pem: '', has_key_pem: false }, idp: { metadata_url: '', metadata_refresh_hours: 24 },
  attribute_mapping: { upn: 'nameid', email: 'email', display_name: 'name', groups: 'groups' }, default_group_slug: '', allow_auto_create: true,
  role_rules: [], group_rules: [], new_user_defaults: { expire_days: 0, traffic_limit_bytes: 0, traffic_reset_period: 'never' }, ...over,
})
export const oidcConfig = (over: Record<string, unknown> = {}) => ({
  enabled: false, issuer_url: '', client_id: '', has_client_secret: false, redirect_url: '', scopes: ['openid'],
  attribute_mapping: { username: 'preferred_username', email: 'email', display_name: 'name', groups: 'groups' }, default_group_slug: '', allow_auto_create: true,
  role_rules: [], group_rules: [], new_user_defaults: { expire_days: 0, traffic_limit_bytes: 0, traffic_reset_period: 'never' }, ...over,
})
export const legalDoc = (kind: string) => ({ kind, enabled: false, consent_version: 0, documents: [], versions: [], data_collection: {} })

/** The server answers a PUT with what it was sent (the records are written whole). */
export function echoPut() {
  api.put.mockImplementation(async (_url: string, data: unknown) => ({ data }))
}

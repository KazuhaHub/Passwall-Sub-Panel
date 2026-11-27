import { settingId, type SectionKey } from './model'
import { subOf } from './tree'

export interface SearchEntry {
  section: SectionKey
  /** The key the setting has in the settings record (or its name in the mail, SAML and OIDC records): a way to find it for someone who knows it. */
  key: string
  /** The i18n key of the label the setting shows. */
  label: string
  /** The id of the control to jump to. */
  id: string
  /** The control that shows this one (a switch it depends on), for when this one is not on the page. */
  via?: string
  /** The sub-tab that holds it (from the tree, tree.ts: it is not written here, so the two cannot disagree). */
  sub: string
}

type Opts = { id?: string; via?: string }
const e = (section: SectionKey, key: string, label: string, o: Opts = {}): SearchEntry =>
  ({ section, key, label, id: o.id ?? settingId(key), via: o.via, sub: subOf(section, key) })
const g = (key: string, label = `settings.general.${key}`, o?: Opts) => e('general', key, label, o)
const s = (key: string, label = `settings.general.${key}`, o?: Opts) => e('security', key, label, o)
const b = (key: string, o?: Opts) => e('brand', key, `settings.brand.${key}`, o)
const u = (key: string, o?: Opts) => e('subscription', key, `settings.subscription.${key}`, o)
const m = (key: string, label: string, id = settingId(`mail_${key}`)) => e('mail', key, label, { id })
const sso = (sub: 'saml' | 'oidc', key: string, label: string, o: Opts = {}) => e('sso', `${sub}.${key}`, label, { ...o, id: o.id ?? `setting-${sub}_${key}` })

/**
 * Every setting the page edits, for the search box: by the label it shows or the key it is stored under. It lists
 * what an admin looks for, and a test checks that each entry's control is in the page. A setting that is only on
 * the page while another is on names that one in `via`.
 */
export const SEARCH_INDEX: readonly SearchEntry[] = [
  g('timezone'), g('cron_traffic_pull_minutes'), g('cron_reconcile_minutes'), g('node_poll_seconds'), g('full_report_seconds'),
  g('max_panel_concurrency'), g('audit_retention_days'), g('sync_task_retention_days'), g('auth_event_retention_days'),
  g('traffic_history_days'), g('expire_before_days'), g('traffic_remain_percent'),
  g('node_task_offline_reconcile_days'), g('node_task_backup_restore_days'), g('node_task_result_retention_days'),
  g('emergency_access_enabled'), g('emergency_access_hours'), g('emergency_access_max_count'), g('emergency_access_quota_gb'),
  g('cloudflare_ips_auto_fetch', 'settings.general.cloudflare_auto_fetch'),
  g('geo_ip_enabled', 'settings.geo.enabled'), g('geo_ip_db_file', 'settings.geo.active_db'), g('geo_ip_auto_update', 'settings.geo.auto_update'),
  g('geo_ip_update_interval_hours', 'settings.geo.update_interval_hours'), g('geo_ip_update_source', 'settings.geo.source'),
  g('geo_ip_update_edition', 'settings.geo.edition'), g('geo_ip_update_url', 'settings.geo.url', { via: settingId('geo_ip_update_source') }),
  g('geo_ip_update_token', 'settings.geo.token_custom'),

  s('login_mode'), s('disallow_user_local_login'), s('disallow_user_password_change'), s('allow_user_personal_rules'),
  s('captcha_enabled', 'settings.general.captcha_ctx_login'), s('captcha_register_enabled', 'settings.general.captcha_ctx_register'),
  s('captcha_forgot_enabled', 'settings.general.captcha_ctx_forgot'),
  s('captcha_provider', undefined, { via: settingId('captcha_enabled') }), s('captcha_trigger', undefined, { via: settingId('captcha_enabled') }),
  s('captcha_fail_threshold', undefined, { via: settingId('captcha_enabled') }), s('captcha_site_key', undefined, { via: settingId('captcha_enabled') }),
  s('captcha_secret_key', undefined, { via: settingId('captcha_enabled') }),
  s('lockout_enabled'), s('lockout_threshold', undefined, { via: settingId('lockout_enabled') }), s('lockout_window_minutes', undefined, { via: settingId('lockout_enabled') }),
  s('lockout_duration_minutes', undefined, { via: settingId('lockout_enabled') }), s('lockout_scope', undefined, { via: settingId('lockout_enabled') }),
  s('password_recovery_enabled'), s('password_recovery_delivery', undefined, { via: settingId('password_recovery_enabled') }),
  s('registration_enabled'), s('registration_require_email_verification', undefined, { via: settingId('registration_enabled') }),
  s('registration_delivery', undefined, { via: settingId('registration_enabled') }), s('registration_email_domains', undefined, { via: settingId('registration_enabled') }),
  s('registration_default_group_id', undefined, { via: settingId('registration_enabled') }), s('registration_default_traffic_gb', undefined, { via: settingId('registration_enabled') }),
  s('registration_default_expire_days', undefined, { via: settingId('registration_enabled') }), s('code_resend_cooldown_sec', 'settings.general.code_resend_cooldown', { via: settingId('password_recovery_enabled') }),
  s('totp_enabled'), s('passkey_enabled'), s('passkey_passwordless', undefined, { via: settingId('passkey_enabled') }),
  s('twofa_allow_email', undefined, { via: settingId('totp_enabled') }), s('twofa_email_resend_cooldown_sec', 'settings.general.twofa_email_cooldown', { via: settingId('totp_enabled') }),
  s('require_2fa_for_staff', undefined, { via: settingId('totp_enabled') }),
  s('jwt_issuer'), s('jwt_access_ttl_minutes'), s('jwt_refresh_ttl_minutes'), s('sub_per_ip_per_min'), s('login_per_ip_per_min'),

  b('site_title'), b('app_title'), b('footer_text'), b('sub_base_url'), b('panel_path'), b('icon_url'), b('logo_url'), b('logo_url_dark'), b('theme_color'), b('email_domain'),

  u('sub_path'), u('sub_import_tutorial_url'), u('sub_log_retention_days'), u('sub_update_interval_hours'), u('sub_profile_name_template'), u('sub_region_flag_prefix'),
  u('sub_block_auto_disable'), u('sub_block_auto_disable_count'), u('sub_block_notify_user'), u('sub_block_notify_max_per_day', { via: settingId('sub_block_notify_user') }),

  e('portal', 'quick_links', 'settings.portal.section_quick_links', { id: 'setting-quick_links' }),
  e('portal', 'global_announcement', 'settings.portal.announcement.enabled', { id: settingId('announcement_enabled') }),

  m('enabled', 'settings.mail.enabled', settingId('mail_enabled')), m('smtp_host', 'settings.mail.smtp_host'), m('smtp_port', 'settings.mail.smtp_port'),
  m('encryption', 'settings.mail.encryption'), m('smtp_username', 'settings.mail.smtp_username'), m('smtp_password', 'settings.mail.smtp_password'),
  m('from_email', 'settings.mail.from_email'), m('from_name', 'settings.mail.from_name'),
  e('mail', 'templates', 'settings.mail.section_templates', { id: 'setting-mail_templates' }),

  sso('saml', 'enabled', 'settings.sso.saml.enabled'), sso('saml', 'mode', 'settings.sso.saml.mode'), sso('saml', 'metadata_url', 'settings.sso.saml.idp_metadata_url'),
  sso('saml', 'metadata_refresh_hours', 'settings.sso.saml.idp_refresh_hours'), sso('saml', 'entity_id', 'settings.sso.saml.sp_entity_id', { via: 'setting-saml_mode' }),
  sso('saml', 'acs_url', 'settings.sso.saml.sp_acs_url', { via: 'setting-saml_mode' }), sso('saml', 'key_pem', 'settings.sso.saml.sp_key', { via: 'setting-saml_mode' }),
  sso('saml', 'attr_upn', 'settings.sso.saml.attr_upn'), sso('saml', 'attr_email', 'settings.sso.saml.attr_email'),
  sso('saml', 'attr_display_name', 'settings.sso.saml.attr_display_name'), sso('saml', 'attr_groups', 'settings.sso.saml.attr_groups'),
  sso('saml', 'allow_auto_create', 'settings.sso.allow_auto_create'), sso('saml', 'default_group_slug', 'settings.sso.saml.default_group'),
  sso('saml', 'expire_days', 'settings.sso.saml.expire_days'), sso('saml', 'traffic_limit_gb', 'settings.sso.saml.traffic_limit_gb'),
  sso('saml', 'traffic_reset_period', 'settings.sso.saml.traffic_reset_period', { id: 'setting-saml_traffic_reset_period' }),
  sso('oidc', 'enabled', 'settings.sso.oidc.enabled'), sso('oidc', 'issuer_url', 'settings.sso.oidc.issuer_url'), sso('oidc', 'client_id', 'settings.sso.oidc.client_id'),
  sso('oidc', 'client_secret', 'settings.sso.oidc.client_secret'), sso('oidc', 'redirect_url', 'settings.sso.oidc.redirect_url'), sso('oidc', 'scopes', 'settings.sso.oidc.scopes'),
  sso('oidc', 'attr_username', 'settings.sso.oidc.attr_username'), sso('oidc', 'attr_email', 'settings.sso.oidc.attr_email'),
  sso('oidc', 'attr_display_name', 'settings.sso.oidc.attr_display_name'), sso('oidc', 'attr_groups', 'settings.sso.oidc.attr_groups'),
  sso('oidc', 'allow_auto_create', 'settings.sso.allow_auto_create'), sso('oidc', 'default_group_slug', 'settings.sso.oidc.default_group'),
  sso('oidc', 'expire_days', 'settings.sso.oidc.expire_days'), sso('oidc', 'traffic_limit_gb', 'settings.sso.oidc.traffic_limit_gb'),
  sso('oidc', 'traffic_reset_period', 'settings.sso.oidc.traffic_reset_period'),

  e('legal', 'legal_enabled', 'settings.legal.enabled', { id: 'settings-section-legal' }), e('legal', 'legal_disclose_usage', 'settings.legal.disclose_usage', { id: 'settings-section-legal' }),
]

/** The entries that match what was typed: the label as shown, the key, or where it lives (section and sub-tab), all case-insensitively. Best matches first. */
export function searchSettings(query: string, label: (e: SearchEntry) => string, whereIs: (e: SearchEntry) => string, limit = 8): SearchEntry[] {
  const q = query.trim().toLowerCase()
  if (!q) return []
  const scored: { entry: SearchEntry; score: number }[] = []
  for (const entry of SEARCH_INDEX) {
    const text = label(entry).toLowerCase()
    const key = entry.key.toLowerCase()
    let score = 0
    if (text.startsWith(q) || key === q) score = 3
    else if (text.includes(q)) score = 2
    else if (key.includes(q)) score = 1
    else if (whereIs(entry).toLowerCase().includes(q)) score = 0.5
    if (score > 0) scored.push({ entry, score })
  }
  return scored.sort((a, b) => b.score - a.score).slice(0, limit).map(x => x.entry)
}

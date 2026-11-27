import type { UISettings } from '@/api/settings'
import type { ConfirmOpts } from '@/components/ConfirmHost'

type T = (key: string, opts?: Record<string, unknown>) => string

/** The keys the Login and security section's confirmations read. */
export type LoginDraft = Pick<UISettings,
  'login_mode' | 'disallow_user_local_login' | 'require_2fa_for_staff' | 'jwt_issuer' | 'jwt_access_ttl_minutes'
  | 'jwt_refresh_ttl_minutes' | 'lockout_enabled' | 'lockout_threshold' | 'lockout_window_minutes'
  | 'lockout_duration_minutes' | 'lockout_scope'>

/** Whether SAML or OIDC is on: the thing a forced-SSO login mode depends on. null: could not be read. */
export type SsoState = 'on' | 'off' | 'unknown'

const ask = (t: T, name: string, vars?: Record<string, unknown>, extra = ''): ConfirmOpts => ({
  title: t(`settings.danger.${name}.title`, vars),
  message: t(`settings.danger.${name}.message`, vars) + extra,
  confirmText: t('settings.danger.confirm'),
  initialFocus: 'cancel',
})

/**
 * The saves that can lock people out or end everyone's session ask first, each in its own words, naming what changes.
 * Only a change toward the risky side asks: turning a protection off, or loosening a limit, does not. Pure, so a test
 * can say exactly which edits ask.
 */
export function loginHazards(saved: LoginDraft, next: LoginDraft, sso: SsoState, t: T): ConfirmOpts[] {
  const out: ConfirmOpts[] = []
  if (next.login_mode !== saved.login_mode) {
    if (next.login_mode === 'sso_redirect') {
      const note = sso === 'off' ? ` ${t('settings.danger.login_mode_sso_redirect.sso_off')}` : sso === 'unknown' ? ` ${t('settings.danger.login_mode_sso_redirect.sso_unknown')}` : ''
      out.push(ask(t, 'login_mode_sso_redirect', undefined, note))
    } else if (next.login_mode === 'local_only') out.push(ask(t, 'login_mode_local_only'))
  }
  if (next.disallow_user_local_login && !saved.disallow_user_local_login) out.push(ask(t, 'disallow_user_local_login'))
  if (next.require_2fa_for_staff && !saved.require_2fa_for_staff) out.push(ask(t, 'require_2fa_for_staff'))
  if (next.jwt_issuer !== saved.jwt_issuer) out.push(ask(t, 'jwt_issuer'))
  else if (next.jwt_access_ttl_minutes < saved.jwt_access_ttl_minutes || next.jwt_refresh_ttl_minutes < saved.jwt_refresh_ttl_minutes) {
    out.push(ask(t, 'jwt_ttl', { access: next.jwt_access_ttl_minutes, refresh: next.jwt_refresh_ttl_minutes }))
  }
  const stricter = next.lockout_enabled && (
    !saved.lockout_enabled
    || next.lockout_threshold < saved.lockout_threshold
    || next.lockout_duration_minutes > saved.lockout_duration_minutes
    || next.lockout_window_minutes > saved.lockout_window_minutes
    || (next.lockout_scope === 'ip' && saved.lockout_scope !== 'ip'))
  if (stricter) {
    out.push(ask(t, 'lockout', { threshold: next.lockout_threshold, window: next.lockout_window_minutes, duration: next.lockout_duration_minutes }))
  }
  return out
}

/**
 * The panel path the way the server reads it (`panelpath.Normalize`): spaces trimmed, and "" or "/" is the domain root.
 * A path it would refuse (a trailing slash, no leading slash) is left as typed so the server's answer is the one shown.
 */
export const normalizePanelPath = (p: string) => { const t = (p ?? '').trim(); return t === '/' ? '' : t }
/** The subscription path as the server stores it: spaces trimmed (the leading slash is only shown, never stored). */
export const normalizeSubPath = (p: string) => (p ?? '').trim().replace(/^\/+/, '')

/**
 * The panel path moves this very page, and the subscription path changes every link already handed out. Both are
 * given NORMALISED, so `/panel/` or a stray space against the saved `/panel` does not ask. `lost` names the other
 * sections with unsaved changes: a panel path change reloads the page, which drops their drafts.
 */
export function addressHazard(kind: 'panel_path' | 'sub_path', from: string, to: string, t: T, lost: string[] = []): ConfirmOpts {
  const opts = ask(t, kind, { from: from || '/', to: to || '/', url: `${to}/admin/settings` })
  if (kind === 'panel_path' && lost.length > 0) opts.message += ` ${t('settings.danger.panel_path.lost', { sections: lost.join(t('settings.section_separator')) })}`
  return opts
}

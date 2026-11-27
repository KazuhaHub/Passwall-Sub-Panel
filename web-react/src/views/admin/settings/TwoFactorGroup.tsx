import { Notice } from '@/components/common/Notice'
import { useAdminT } from '../useAdminT'
import type { SubKeys } from './model'
import { Group, Nested, NumField, SwitchField } from './parts'
import type { UiSection } from './useUiSection'

/**
 * Two-step verification. It is the umbrella: the authenticator app (TOTP), passkeys and the email code are peer
 * methods under it, and "require it for staff" is the enrollment policy, kept apart from the methods. The email code
 * and the policy only make sense once a real factor (TOTP or a passkey) can be enrolled, so they appear then.
 */
export function TwoFactorGroup({ form }: { form: UiSection<SubKeys<'security', 'twofa'>> }) {
  const t = useAdminT()
  const { value: v, patch } = form
  const factor = v.totp_enabled || v.passkey_enabled
  return (
    <Group title={t('settings.general.twofa_section_title')} description={t('settings.general.twofa_section_hint')}>
      <SwitchField name="totp_enabled" label={t('settings.general.totp_enabled')} hint={t('settings.general.totp_hint')}
        checked={v.totp_enabled} onChange={c => patch({ totp_enabled: c })} />
      <SwitchField name="passkey_enabled" label={t('settings.general.passkey_enabled')} hint={t('settings.general.passkey_hint')}
        checked={v.passkey_enabled} onChange={c => patch({ passkey_enabled: c })} />
      {v.passkey_enabled && (
        <Nested>
          <SwitchField name="passkey_passwordless" label={t('settings.general.passkey_passwordless')}
            checked={v.passkey_passwordless} onChange={c => patch({ passkey_passwordless: c })} />
        </Nested>
      )}
      {factor && (
        <SwitchField name="twofa_allow_email" label={t('settings.general.twofa_allow_email')} hint={t('settings.general.twofa_allow_email_hint')}
          checked={v.twofa_allow_email} onChange={c => patch({ twofa_allow_email: c })} />
      )}
      {factor && v.twofa_allow_email && (
        <Nested>
          <div className="max-w-xs">
            <NumField name="twofa_email_resend_cooldown_sec" label={t('settings.general.twofa_email_cooldown')} unit={t('settings.unit.seconds')}
              value={v.twofa_email_resend_cooldown_sec} onChange={n => patch({ twofa_email_resend_cooldown_sec: n })} />
          </div>
        </Nested>
      )}
      {factor && (
        <SwitchField name="require_2fa_for_staff" label={t('settings.general.require_2fa_for_staff')} hint={t('settings.general.require_2fa_for_staff_hint')}
          checked={v.require_2fa_for_staff} onChange={c => patch({ require_2fa_for_staff: c })} />
      )}
      <Notice tone="info">{t('settings.general.local_auth_scope_hint')}</Notice>
    </Group>
  )
}

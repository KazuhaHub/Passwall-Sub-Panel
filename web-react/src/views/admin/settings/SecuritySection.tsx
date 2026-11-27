import { memo } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { UISettings } from '@/api/settings'
import { confirm } from '@/components/ConfirmHost'
import { Field } from '@/components/form/Field'
import { SecretField } from '@/components/form/SecretField'
import { samlConfigQuery, oidcConfigQuery } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { useAdminT } from '../useAdminT'
import { AccountPolicyGroups } from './AccountPolicyGroups'
import { loginHazards, type SsoState } from './dangerConfirms'
import { SUB_KEYS, settingId } from './model'
import { Group, Hint, NumField, Pair, SectionForm, SelectRow, TextRow, SwitchField, Nested } from './parts'
import { SubTabs, type SubPanel } from './SubTabs'
import { TwoFactorGroup } from './TwoFactorGroup'
import { useUiSection } from './useUiSection'

const noProblems = () => ({})

/**
 * Login and security, one sub-tab per decision: how people sign in and how long a session lasts, how an account is
 * made or recovered, the second factor, and what guards the sign-in form against abuse. Each saves on its own.
 */
const PANELS = { signin: SignInPanel, account: AccountPanel, twofa: TwoFactorPanel, protection: ProtectionPanel } satisfies Record<string, SubPanel>

export const SecuritySection = memo(function SecuritySection({ active }: { active: boolean }) {
  return <SubTabs section="security" active={active} panels={PANELS} />
})

type Panel = { onDirtyChange: (dirty: boolean) => void }

/** Whether SAML or OIDC is on matters only to a forced-SSO login mode, so it is asked when that is saved, not on every visit. */
function useSsoState() {
  const queryClient = useQueryClient()
  const scope = useQueryScope()
  return async function ssoState(): Promise<SsoState> {
    try {
      const [saml, oidc] = await Promise.all([queryClient.fetchQuery(samlConfigQuery(scope)), queryClient.fetchQuery(oidcConfigQuery(scope))])
      return saml.enabled || oidc.enabled ? 'on' : 'off'
    } catch { return 'unknown' }
  }
}

/**
 * Asks, one by one, about the risky edits among this sub-tab's keys. `loginHazards` compares the whole login record,
 * so it is given the saved record under this sub-tab's draft: only the keys the draft changed can ask.
 */
async function confirmHazards(saved: UISettings, draft: Partial<UISettings>, sso: SsoState, t: ReturnType<typeof useAdminT>): Promise<boolean> {
  for (const opts of loginHazards(saved, { ...saved, ...draft }, sso, t)) if (!(await confirm(opts))) return false
  return true
}

function SignInPanel({ onDirtyChange }: Panel) {
  const t = useAdminT()
  const ssoState = useSsoState()
  const form = useUiSection(SUB_KEYS.security.signin, onDirtyChange, noProblems)
  const { value: v, patch, saved } = form

  async function save() {
    const sso = v.login_mode === 'sso_redirect' && saved.login_mode !== 'sso_redirect' ? await ssoState() : 'on'
    if (!(await confirmHazards(saved, v, sso, t))) return
    await form.save()
  }

  const min = t('settings.unit.minutes')
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void save()} onDiscard={form.discard}>
      <Group title={t('settings.general.section_login')}>
        <SelectRow<UISettings['login_mode']> name="login_mode" label={t('settings.general.login_mode')} value={v.login_mode} onChange={m => patch({ login_mode: m })}
          hint={t('settings.general.login_mode_hint')}
          options={[
            { value: 'dual', label: t('settings.general.login_mode_dual') },
            { value: 'sso_first', label: t('settings.general.login_mode_sso_first') },
            { value: 'sso_redirect', label: t('settings.general.login_mode_sso_redirect') },
            { value: 'local_only', label: t('settings.general.login_mode_local_only') },
          ]} />
        <SwitchField name="disallow_user_local_login" label={t('settings.general.disallow_user_local_login')}
          checked={v.disallow_user_local_login} onChange={c => patch({ disallow_user_local_login: c })} />
        <SwitchField name="disallow_user_password_change" label={t('settings.general.disallow_user_password_change')}
          checked={v.disallow_user_password_change} onChange={c => patch({ disallow_user_password_change: c })} />
        <SwitchField name="allow_user_personal_rules" label={t('settings.general.allow_user_personal_rules')} hint={t('settings.general.allow_user_personal_rules_hint')}
          checked={v.allow_user_personal_rules} onChange={c => patch({ allow_user_personal_rules: c })} />
      </Group>

      <Group title={t('settings.general.section_sessions')}>
        <TextRow name="jwt_issuer" label={t('settings.general.jwt_issuer')} hint={t('settings.general.jwt_issuer_hint')} value={v.jwt_issuer} onChange={i => patch({ jwt_issuer: i })} />
        <Pair>
          <NumField name="jwt_access_ttl_minutes" label={t('settings.general.jwt_access_ttl_minutes')} unit={min} value={v.jwt_access_ttl_minutes} onChange={n => patch({ jwt_access_ttl_minutes: n })} />
          <NumField name="jwt_refresh_ttl_minutes" label={t('settings.general.jwt_refresh_ttl_minutes')} unit={min} value={v.jwt_refresh_ttl_minutes} onChange={n => patch({ jwt_refresh_ttl_minutes: n })} />
        </Pair>
        <Hint>{t('settings.general.jwt_ttl_hint')}</Hint>
      </Group>
    </SectionForm>
  )
}

function TwoFactorPanel({ onDirtyChange }: Panel) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.security.twofa, onDirtyChange, noProblems)
  async function save() {
    if (!(await confirmHazards(form.saved, form.value, 'on', t))) return
    await form.save()
  }
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void save()} onDiscard={form.discard}>
      <TwoFactorGroup form={form} />
    </SectionForm>
  )
}

function AccountPanel({ onDirtyChange }: Panel) {
  const form = useUiSection(SUB_KEYS.security.account, onDirtyChange, noProblems)
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void form.save()} onDiscard={form.discard}>
      <AccountPolicyGroups form={form} />
    </SectionForm>
  )
}

function ProtectionPanel({ onDirtyChange }: Panel) {
  const t = useAdminT()
  const form = useUiSection(SUB_KEYS.security.protection, onDirtyChange, noProblems)
  const { value: v, patch, saved } = form
  async function save() {
    if (!(await confirmHazards(saved, v, 'on', t))) return
    await form.save()
  }
  const min = t('settings.unit.minutes')
  const perMin = t('settings.unit.per_minute')
  const captchaOn = v.captcha_enabled || v.captcha_register_enabled || v.captcha_forgot_enabled
  const provider = v.captcha_provider || 'image'
  return (
    <SectionForm dirty={form.dirty} pending={form.pending} onSave={() => void save()} onDiscard={form.discard}>
      <Group title={t('settings.general.section_captcha')}>
        <p className="text-sm font-medium">{t('settings.general.captcha_contexts')}</p>
        <SwitchField name="captcha_enabled" label={t('settings.general.captcha_ctx_login')} checked={v.captcha_enabled} onChange={c => patch({ captcha_enabled: c })} />
        <SwitchField name="captcha_register_enabled" label={t('settings.general.captcha_ctx_register')} checked={v.captcha_register_enabled} onChange={c => patch({ captcha_register_enabled: c })} />
        <SwitchField name="captcha_forgot_enabled" label={t('settings.general.captcha_ctx_forgot')} checked={v.captcha_forgot_enabled} onChange={c => patch({ captcha_forgot_enabled: c })} />
        {captchaOn && (
          <Nested>
            <SelectRow<NonNullable<UISettings['captcha_provider']>> name="captcha_provider" label={t('settings.general.captcha_provider')} value={provider}
              onChange={p => patch({ captcha_provider: p })}
              hint={provider !== 'image' ? t('settings.general.captcha_provider_keys_hint') : undefined}
              options={[
                { value: 'image', label: t('settings.general.captcha_provider_image') },
                { value: 'turnstile', label: 'Cloudflare Turnstile' },
                { value: 'recaptcha', label: 'Google reCAPTCHA v2' },
                { value: 'hcaptcha', label: 'hCaptcha' },
              ]} />
            {/* Trigger and threshold are for sign-in only: register and recovery have no failure history before an account exists, so they are always on. */}
            {v.captcha_enabled && (
              <>
                <SelectRow<NonNullable<UISettings['captcha_trigger']>> name="captcha_trigger" label={t('settings.general.captcha_trigger')} value={v.captcha_trigger || 'after_failures'}
                  onChange={c => patch({ captcha_trigger: c })}
                  options={[
                    { value: 'after_failures', label: t('settings.general.captcha_trigger_after_failures') },
                    { value: 'always', label: t('settings.general.captcha_trigger_always') },
                  ]} />
                {v.captcha_trigger !== 'always' && (
                  <NumField name="captcha_fail_threshold" label={t('settings.general.captcha_fail_threshold')} value={v.captcha_fail_threshold}
                    onChange={n => patch({ captcha_fail_threshold: n })} />
                )}
              </>
            )}
            {provider !== 'image' && (
              <>
                <TextRow name="captcha_site_key" label={t('settings.general.captcha_site_key')} value={v.captcha_site_key} onChange={k => patch({ captcha_site_key: k })} />
                <Field label={t('settings.general.captcha_secret_key')} hint={saved.has_captcha_secret_key ? t('settings.secret_hint') : undefined}>
                  <SecretField id={settingId('captcha_secret_key')} value={v.captcha_secret_key ?? ''} isSet={saved.has_captcha_secret_key}
                    onChange={k => patch({ captcha_secret_key: k })} />
                </Field>
              </>
            )}
          </Nested>
        )}
      </Group>

      <Group title={t('settings.general.section_lockout')}>
        <SwitchField name="lockout_enabled" label={t('settings.general.lockout_enabled')} checked={v.lockout_enabled} onChange={c => patch({ lockout_enabled: c })} />
        {v.lockout_enabled && (
          <Nested>
            <Pair>
              <NumField name="lockout_threshold" label={t('settings.general.lockout_threshold')} value={v.lockout_threshold} onChange={n => patch({ lockout_threshold: n })} />
              <NumField name="lockout_window_minutes" label={t('settings.general.lockout_window_minutes')} unit={min} value={v.lockout_window_minutes} onChange={n => patch({ lockout_window_minutes: n })} />
            </Pair>
            <Pair>
              <NumField name="lockout_duration_minutes" label={t('settings.general.lockout_duration_minutes')} unit={min} value={v.lockout_duration_minutes} onChange={n => patch({ lockout_duration_minutes: n })} />
              <SelectRow<NonNullable<UISettings['lockout_scope']>> name="lockout_scope" label={t('settings.general.lockout_scope')} value={v.lockout_scope || 'ip_upn'} onChange={s => patch({ lockout_scope: s })}
                options={[
                  { value: 'ip_upn', label: t('settings.general.lockout_scope_ip_upn') },
                  { value: 'ip', label: t('settings.general.lockout_scope_ip') },
                ]} />
            </Pair>
          </Nested>
        )}
      </Group>

      <Group title={t('settings.general.section_rate_limits')}>
        <Pair>
          <NumField name="sub_per_ip_per_min" label={t('settings.general.sub_per_ip_per_min')} unit={perMin} value={v.sub_per_ip_per_min} onChange={n => patch({ sub_per_ip_per_min: n })} />
          <NumField name="login_per_ip_per_min" label={t('settings.general.login_per_ip_per_min')} unit={perMin} value={v.login_per_ip_per_min} onChange={n => patch({ login_per_ip_per_min: n })} />
        </Pair>
      </Group>
    </SectionForm>
  )
}

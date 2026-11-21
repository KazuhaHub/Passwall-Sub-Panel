import type { UISettings } from '@/api/settings'
import { Field } from '@/components/form/Field'
import { NativeSelect } from '@/components/ui/native-select'
import { useAllGroups } from '@/query/groups'
import { useQueryScope } from '@/query/useQueryScope'
import { useAdminT } from '../useAdminT'
import type { KeysOf } from './model'
import { settingId } from './model'
import { Group, Hint, Nested, NumField, Pair, SelectRow, SwitchField, TextRow } from './parts'
import type { UiSection } from './useUiSection'

type Delivery = NonNullable<UISettings['password_recovery_delivery']>

/** Password recovery and self-service registration: what an account can do for itself before it can sign in. */
export function AccountPolicyGroups({ form }: { form: UiSection<KeysOf<'security'>> }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const groups = useAllGroups(scope)
  const { value: v, patch } = form
  const list = groups.data ?? []
  const catalogue: 'loading' | 'ready' | 'error' = groups.data ? 'ready' : groups.isError ? 'error' : 'loading'
  const missing = v.registration_default_group_id > 0 && !list.some(g => g.id === v.registration_default_group_id)

  // One line under the picker, whichever is true: still loading, failed, the saved group is gone, or the plain explanation.
  const groupHint = catalogue === 'loading' ? t('settings.general.registration_default_group_loading')
    : catalogue === 'error' ? t('settings.general.registration_default_group_load_error')
      : missing ? t('settings.general.registration_default_group_missing', { id: v.registration_default_group_id })
        : t('settings.general.registration_default_group_hint')
  const deliveryOptions = [
    { value: 'link' as const, label: t('settings.general.password_recovery_delivery_link') },
    { value: 'otp' as const, label: t('settings.general.password_recovery_delivery_otp') },
  ]
  const sendsCodes = v.password_recovery_enabled || (v.registration_enabled && v.registration_require_email_verification)

  return (
    <>
      <Group title={t('settings.general.section_recovery')}>
        <SwitchField name="password_recovery_enabled" label={t('settings.general.password_recovery_enabled')} hint={t('settings.general.password_recovery_hint')}
          checked={v.password_recovery_enabled} onChange={c => patch({ password_recovery_enabled: c })} />
        {v.password_recovery_enabled && (
          <Nested>
            <SelectRow<Delivery> name="password_recovery_delivery" label={t('settings.general.password_recovery_delivery')} value={v.password_recovery_delivery || 'link'}
              onChange={d => patch({ password_recovery_delivery: d })} options={deliveryOptions} />
          </Nested>
        )}
      </Group>

      <Group title={t('settings.general.section_registration')} description={v.registration_enabled ? t('settings.general.registration_hint') : undefined}>
        <SwitchField name="registration_enabled" label={t('settings.general.registration_enabled')}
          checked={v.registration_enabled} onChange={c => patch({ registration_enabled: c })} />
        {v.registration_enabled && (
          <Nested>
            <SwitchField name="registration_require_email_verification" label={t('settings.general.registration_require_email_verification')}
              checked={v.registration_require_email_verification} onChange={c => patch({ registration_require_email_verification: c })} />
            {v.registration_require_email_verification && (
              <SelectRow<Delivery> name="registration_delivery" label={t('settings.general.registration_delivery')} value={v.registration_delivery || 'link'}
                onChange={d => patch({ registration_delivery: d })} options={deliveryOptions} />
            )}
            <TextRow name="registration_email_domains" label={t('settings.general.registration_email_domains')} placeholder="example.com, corp.org"
              value={v.registration_email_domains} onChange={d => patch({ registration_email_domains: d })} />
            <Pair>
              <Field label={t('settings.general.registration_default_group_id')} hint={groupHint} error={undefined}>
                <NativeSelect id={settingId('registration_default_group_id')} value={String(v.registration_default_group_id)} disabled={catalogue !== 'ready'}
                  onChange={e => patch({ registration_default_group_id: Number(e.target.value) })}>
                  <option value="0">
                    {list[0]
                      ? t('settings.general.registration_default_group_auto', { group: list[0].name })
                      : t('settings.general.registration_default_group_auto_empty')}
                  </option>
                  {v.registration_default_group_id > 0 && missing && (
                    <option value={v.registration_default_group_id}>
                      {catalogue === 'ready'
                        ? t('settings.general.registration_default_group_missing_option', { id: v.registration_default_group_id })
                        : t('settings.general.registration_default_group_current_option', { id: v.registration_default_group_id })}
                    </option>
                  )}
                  {list.map(g => <option key={g.id} value={g.id}>{g.name} ({g.slug})</option>)}
                </NativeSelect>
              </Field>
              <NumField name="registration_default_traffic_gb" label={t('settings.general.registration_default_traffic_gb')} unit="GB" integer={false}
                value={v.registration_default_traffic_gb} onChange={n => patch({ registration_default_traffic_gb: n })} />
            </Pair>
            <NumField name="registration_default_expire_days" label={t('settings.general.registration_default_expire_days')} unit={t('settings.unit.days')}
              value={v.registration_default_expire_days} onChange={n => patch({ registration_default_expire_days: n })} />
          </Nested>
        )}
        {/* Shared anti-abuse: how often a signup-verification or password-reset code may be sent again to one account. */}
        {sendsCodes && (
          <div className="max-w-xs">
            <NumField name="code_resend_cooldown_sec" label={t('settings.general.code_resend_cooldown')} unit={t('settings.unit.seconds')}
              hint={t('settings.general.code_resend_cooldown_hint')} value={v.code_resend_cooldown_sec} onChange={n => patch({ code_resend_cooldown_sec: n })} />
          </div>
        )}
        <Hint>{t('settings.general.registration_group_note')}</Hint>
      </Group>
    </>
  )
}

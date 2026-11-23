import type { ReactNode } from 'react'
import type { Group, ResetPeriod, Role } from '@/api/types'
import { IconButton } from '@/components/common/IconButton'
import { Field } from '@/components/form/Field'
import { FormSection } from '@/components/form/FormSection'
import { InheritanceField } from '@/components/form/InheritanceField'
import { NumberField } from '@/components/form/NumberField'
import { SecretField } from '@/components/form/SecretField'
import { Combobox, ComboboxContent, ComboboxEmpty, ComboboxInput, ComboboxItem, ComboboxList } from '@/components/ui/combobox'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Textarea } from '@/components/ui/textarea'
import { IconHelp } from '@/lib/icons'
import { useSiteStore } from '@/stores/site'
import type { CapHints } from './capHints'
import type { DraftField, UserDraft } from './draft'
import { useAdminT } from './useAdminT'

const RESET_PERIODS: ResetPeriod[] = ['never', 'monthly', 'quarterly', 'yearly']
const ROLES: Role[] = ['user', 'operator', 'admin']

export interface UserFormProps {
  mode: 'create' | 'edit'
  draft: UserDraft
  patch: (p: Partial<UserDraft>) => void
  /** The message under a field, already translated; empty until the person has left it or tried to save. */
  error: (f: DraftField) => string | undefined
  blur: (f: DraftField) => void
  groups: readonly Group[]
  hints: CapHints
  /** What moving to the drafted group would change (edit). */
  groupNote?: ReactNode
  /** An operator may not change roles; the backend refuses it too, so the field says so instead of failing after Save. */
  roleLocked?: boolean
  /** Your own account: it cannot be disabled from here, which would lock you out. */
  selfAccount?: boolean
  /** Why the account is off, as the server worded it (edit). */
  disabledReason?: string
  /** The emergency-access block (edit). */
  emergency?: ReactNode
}

/** The role cheat sheet behind the "?" beside the role: two roles are easy to remember, three already have admins asking what an operator can do. */
function RoleHelp() {
  const t = useAdminT()
  return (
    <Popover>
      <PopoverTrigger render={<IconButton label={t('users.role.help_tooltip')} size="icon-sm" />}><IconHelp /></PopoverTrigger>
      <PopoverContent align="end" className="w-80 gap-2">
        <p className="font-medium">{t('users.role.help_title')}</p>
        {ROLES.map(r => (
          <div key={r}>
            <div className="text-[0.8125rem] font-semibold">{t(`users.role.${r}`)}</div>
            <p className="text-xs text-muted-foreground">{t(`users.role.help_${r}`)}</p>
          </div>
        ))}
      </PopoverContent>
    </Popover>
  )
}

/**
 * The fields of the create form and the edit form, grouped the way they are decided: who, which group, how much
 * and when it resets, until when, access, notes. Controlled by the caller; it knows nothing about saving, so the
 * two forms cannot drift. A limit is an InheritanceField: it shows the group's value and says whether this user
 * follows it, instead of a number that might or might not be the user's own.
 */
export function UserForm(p: UserFormProps) {
  const t = useAdminT()
  const { draft: d, patch, mode } = p
  const edit = mode === 'edit'
  const panelTz = useSiteStore(s => s.timezone)
  const browserTz = (() => { try { return Intl.DateTimeFormat().resolvedOptions().timeZone } catch { return '' } })()
  // A picked "5/30" is the panel's 5/30, not the browser's: say so when the two zones differ.
  const tzHint = panelTz && panelTz !== browserTz ? t('users.field.expire_tz_hint', { tz: panelTz }) : undefined
  const err = (f: DraftField) => { const k = p.error(f); return k ? t(k) : undefined }
  const group = p.groups.find(g => g.id === d.group_id)

  const unlimited = t('users.detail.unlimited')
  const gb = (v: number) => (v === 0 ? unlimited : `${v} GB`)
  const count = (v: number) => (v === 0 ? unlimited : String(v))
  const levels = (own: number | undefined, theirs: number | null | undefined) => [
    { id: 'group', label: group?.name ?? t('users.field.group'), value: theirs ?? undefined },
    { id: 'user', label: t('users.field.level_user'), value: own },
  ]
  // At creation a typed 0 would read as "follow the group" (the backend's rule), so a set value must be positive there.
  const minSet = edit ? 0 : 1

  return (
    <div className="flex flex-col gap-8">
      <FormSection title={t('users.form.identity')}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('users.field.upn')} required hint={edit ? t('users.field.upn_edit_hint') : undefined} error={err('upn')}>
            <Input value={d.upn} maxLength={255} onChange={e => patch({ upn: e.target.value })} onBlur={() => p.blur('upn')} />
          </Field>
          <Field label={t('users.field.display_name')} error={err('display_name')}>
            <Input value={d.display_name} onChange={e => patch({ display_name: e.target.value })} onBlur={() => p.blur('display_name')} />
          </Field>
          <Field label={t('users.field.email')} error={err('email')}>
            <Input type="email" value={d.email} onChange={e => patch({ email: e.target.value })} onBlur={() => p.blur('email')} />
          </Field>
          {!edit && (
            <Field label={t('users.field.password')} error={err('password')}>
              {fp => <SecretField id={fp.id} aria-describedby={fp['aria-describedby']} aria-invalid={fp['aria-invalid']} value={d.password} onChange={v => patch({ password: v })} onBlur={() => p.blur('password')} />}
            </Field>
          )}
        </div>
      </FormSection>

      <FormSection title={t('users.form.group')}>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="flex flex-col gap-1">
            <Field label={t('users.field.group')} required error={err('group_id')}>
              {fp => (
                <Combobox items={p.groups as Group[]} value={group ?? null}
                  onValueChange={(v: Group | null) => patch({ group_id: v ? v.id : '' })}
                  itemToStringLabel={(g: Group) => g.name} isItemEqualToValue={(a: Group, b: Group) => a.id === b.id}>
                  <ComboboxInput id={fp.id} aria-describedby={fp['aria-describedby']} aria-invalid={fp['aria-invalid']} onBlur={() => p.blur('group_id')} />
                  <ComboboxContent>
                    <ComboboxEmpty>{t('users.filter_group_none')}</ComboboxEmpty>
                    <ComboboxList>{(g: Group) => <ComboboxItem key={g.id} value={g}>{g.name}</ComboboxItem>}</ComboboxList>
                  </ComboboxContent>
                </Combobox>
              )}
            </Field>
            {p.groupNote}
          </div>
        </div>
      </FormSection>

      <FormSection title={t('users.form.quota')} description={edit ? undefined : t('users.form.create_limits_note')}>
        <div className="grid gap-4 sm:grid-cols-2">
          <InheritanceField<number>
            className="sm:col-span-2"
            label={t('users.field.traffic_limit')}
            levels={levels(d.traffic, group?.traffic_limit_gb)}
            editLevel="user"
            onChange={traffic => patch({ traffic })}
            renderValue={gb}
            defaultValue={0}
            renderEditor={ep => <NumberField {...ep} onChange={n => ep.onChange(n ?? 0)} min={edit ? 0 : 0.01} unit="GB" zeroLabel={unlimited} />}
          />
          <Field label={t('users.field.traffic_reset_period')}>
            <NativeSelect value={d.reset_period} onChange={e => patch({ reset_period: e.target.value as ResetPeriod })}>
              {RESET_PERIODS.map(r => <option key={r} value={r}>{t(`users.reset_period.${r}`)}</option>)}
            </NativeSelect>
          </Field>
          {edit && (
            <Field label={t('users.field.period_used_gb')}>
              {fp => <NumberField id={fp.id} aria-describedby={fp['aria-describedby']} value={d.period_used} min={0} unit="GB" onChange={n => patch({ period_used: n ?? 0 })} />}
            </Field>
          )}
          <InheritanceField<number>
            label={t('users.field.ip_limit')}
            levels={levels(d.ip, group?.ip_limit)}
            editLevel="user"
            onChange={ip => patch({ ip })}
            renderValue={count}
            defaultValue={0}
            hint={p.hints.ip(d.ip ?? 0, edit) ?? t('users.field.limit_zero_hint')}
            renderEditor={ep => <NumberField {...ep} onChange={n => ep.onChange(n ?? 0)} min={minSet} integer zeroLabel={unlimited} />}
          />
          <InheritanceField<number>
            label={t('users.field.device_limit')}
            levels={levels(d.device, group?.device_limit)}
            editLevel="user"
            onChange={device => patch({ device })}
            renderValue={count}
            defaultValue={0}
            hint={p.hints.device(d.device ?? 0, edit) ?? t('users.field.limit_zero_hint')}
            renderEditor={ep => <NumberField {...ep} onChange={n => ep.onChange(n ?? 0)} min={minSet} integer zeroLabel={unlimited} />}
          />
        </div>
      </FormSection>

      <FormSection title={t('users.form.expiry')}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t('users.field.expire_mode')}>
            <NativeSelect value={d.expire_mode} onChange={e => patch({ expire_mode: e.target.value as 'date' | 'permanent' })}>
              <option value="date">{t('users.field.expire_mode_date')}</option>
              <option value="permanent">{t('users.field.expire_mode_permanent')}</option>
            </NativeSelect>
          </Field>
          {d.expire_mode === 'date' && (
            <Field label={t('users.field.expire_date')} required hint={tzHint} error={err('expire_date')}>
              <Input type="date" value={d.expire_date} onChange={e => patch({ expire_date: e.target.value })} onBlur={() => p.blur('expire_date')} />
            </Field>
          )}
        </div>
      </FormSection>

      {edit && (
        <FormSection title={t('users.form.access')}>
          <Field label={t('users.field.role')} hint={p.roleLocked ? t('users.role.operator_locked') : undefined}>
            {fp => (
              <div className="flex items-center gap-1">
                <NativeSelect id={fp.id} aria-describedby={fp['aria-describedby']} value={d.role} disabled={p.roleLocked}
                  onChange={e => patch({ role: e.target.value as Role })}>
                  {ROLES.map(r => <option key={r} value={r}>{t(`users.role.${r}`)}</option>)}
                </NativeSelect>
                <RoleHelp />
              </div>
            )}
          </Field>
          <Field label={t('users.field.account_enabled')}
            hint={p.selfAccount ? t('users.field.account_self_locked') : (!d.enabled && p.disabledReason ? p.disabledReason : undefined)}>
            {fp => (
              <NativeSelect id={fp.id} aria-describedby={fp['aria-describedby']} value={d.enabled ? 'enabled' : 'disabled'} disabled={p.selfAccount}
                onChange={e => patch({ enabled: e.target.value === 'enabled' })}>
                <option value="enabled">{t('users.status.account_active')}</option>
                <option value="disabled">{t('users.status.account_disabled')}</option>
              </NativeSelect>
            )}
          </Field>
          {p.emergency}
        </FormSection>
      )}

      <FormSection title={t('users.form.notes')}>
        <Field label={t('users.field.remark')}>
          <Textarea rows={3} value={d.remark} onChange={e => patch({ remark: e.target.value })} />
        </Field>
      </FormSection>
    </div>
  )
}

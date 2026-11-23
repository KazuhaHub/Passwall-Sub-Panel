import type { Group as GroupRow } from '@/api/types'
import { Field } from '@/components/form/Field'
import { NumberField } from '@/components/form/NumberField'
import { OptionCombobox } from '@/components/OptionCombobox'
import { useAdminT } from '../../useAdminT'
import { Pair, SelectRow, SwitchField } from '../parts'

const GIB = 1024 * 1024 * 1024

interface Defaults { expire_days: number; traffic_limit_bytes: number; traffic_reset_period: string }

/** What a freshly auto-created SSO account looks like: whether it is created at all, which group, quota, expiry and reset period. SAML and OIDC share it. */
export function NewUserDefaults({ prefix, allowAutoCreate, onAllowAutoCreate, groupSlug, onGroupSlug, groups, defaults, onDefaults }: {
  /** The id prefix of the controls ("saml", "oidc"), so the search can find them. */
  prefix: 'saml' | 'oidc'
  allowAutoCreate: boolean
  onAllowAutoCreate: (v: boolean) => void
  groupSlug: string
  onGroupSlug: (slug: string) => void
  groups: GroupRow[]
  defaults: Defaults
  onDefaults: (p: Partial<Defaults>) => void
}) {
  const t = useAdminT()
  const missing = !!groupSlug && !groups.some(g => g.slug === groupSlug)
  // The saved slug stays selectable when it is not in the list (stale, or not loaded yet): losing it silently would be worse than a phantom row.
  const options = [
    ...(missing ? [{ value: groupSlug, label: groupSlug, note: t('settings.sso.default_group_current', { slug: groupSlug }) }] : []),
    ...groups.map(g => ({ value: g.slug, label: `${g.name} (${g.slug})` })),
  ]
  return (
    <>
      <SwitchField name={`${prefix}_allow_auto_create`} label={t('settings.sso.allow_auto_create')} hint={t('settings.sso.allow_auto_create_hint')}
        checked={allowAutoCreate} onChange={onAllowAutoCreate} />
      <Field label={t(`settings.sso.${prefix}.default_group`)}>
        <OptionCombobox id={`setting-${prefix}_default_group_slug`} options={options} value={groupSlug || null}
          onChange={slug => onGroupSlug(slug ?? '')} emptyText={t('settings.sso.default_group_empty')} placeholder={t('settings.sso.default_group_none')} />
      </Field>
      <Pair>
        <Field label={t(`settings.sso.${prefix}.expire_days`)}>
          <NumberField id={`setting-${prefix}_expire_days`} value={defaults.expire_days} integer min={0} unit={t('settings.unit.days')}
            onChange={n => { if (n !== null) onDefaults({ expire_days: n }) }} />
        </Field>
        <Field label={t(`settings.sso.${prefix}.traffic_limit_gb`)}>
          <NumberField id={`setting-${prefix}_traffic_limit_gb`} value={Math.round((defaults.traffic_limit_bytes / GIB) * 100) / 100} min={0} unit="GB"
            onChange={n => { if (n !== null) onDefaults({ traffic_limit_bytes: Math.round(n * GIB) }) }} />
        </Field>
      </Pair>
      <SelectRow name={`${prefix}_traffic_reset_period`} label={t('users.field.traffic_reset_period')} value={defaults.traffic_reset_period}
        onChange={p => onDefaults({ traffic_reset_period: p })}
        options={(['never', 'monthly', 'quarterly', 'yearly'] as const).map(p => ({ value: p, label: t(`users.reset_period.${p}`) }))} />
    </>
  )
}

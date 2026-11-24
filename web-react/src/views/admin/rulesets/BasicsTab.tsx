import { useTranslation } from 'react-i18next'

import type { RuleSet } from '@/api/rules'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { countLines } from './model'

/**
 * The rule set's own fields: what used to sit above the rules in the side sheet. The slug is the rule set's address and
 * file name, so it is set once, when the rule set is created.
 */
export function BasicsTab({ form, setForm, editing, readOnly, slugError, nameError, usedBy }: {
  form: RuleSet
  setForm: (next: RuleSet) => void
  editing: boolean
  readOnly: boolean
  slugError?: string
  nameError?: string
  /** Names of the templates that bind this rule set. */
  usedBy: readonly string[]
}) {
  const { t } = useTranslation(['admin'])
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <Field label={t('admin:rules.field.slug')} required error={slugError}>
          <Input value={form.slug} disabled={editing || readOnly} placeholder={t('admin:rules.placeholder.slug')} onChange={e => setForm({ ...form, slug: e.target.value })} />
        </Field>
        <Field label={t('admin:rules.field.name')} required error={nameError}>
          <Input value={form.name} disabled={readOnly} placeholder={t('admin:rules.placeholder.name')} onChange={e => setForm({ ...form, name: e.target.value })} />
        </Field>
        <Field label={t('admin:rules.field.sort')}>
          <Input type="number" value={form.sort} disabled={readOnly} onChange={e => setForm({ ...form, sort: Number(e.target.value) })} />
        </Field>
        <Field label={t('admin:rules.field.enabled')} orientation="horizontal">
          <Switch checked={form.enabled} disabled={readOnly} onCheckedChange={c => setForm({ ...form, enabled: c })} />
        </Field>
      </div>
      <Field label={t('admin:rules.field.direct_subscription_domain')} hint={t('admin:rules.hint.direct_subscription_domain')} orientation="horizontal">
        <Switch checked={form.direct_subscription_domain} disabled={readOnly} onCheckedChange={c => setForm({ ...form, direct_subscription_domain: c })} />
      </Field>
      <dl className="grid max-w-xl grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm">
        <dt className="text-muted-foreground">{t('admin:rules.table.used_by')}</dt>
        <dd>{usedBy.length > 0 ? usedBy.join('、') : t('admin:rules.status.no_usage')}</dd>
        <dt className="text-muted-foreground">{t('admin:rules.table.lines')}</dt>
        <dd className="tabular-nums">{countLines(form.content).toLocaleString()}</dd>
      </dl>
    </div>
  )
}

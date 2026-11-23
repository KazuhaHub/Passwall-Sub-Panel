import { Link } from 'react-router'

import type { Group } from '@/api/types'
import { Field } from '@/components/form/Field'
import { FormSection } from '@/components/form/FormSection'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import type { GroupForm } from './model'
import { useGroupsT } from './useGroupsT'

/**
 * Identity, remark and the one rule that reaches into members' sign-in (require two-step verification). The slug
 * is chosen when the group is created and then read-only. The members line is a pointer, not a list: members are
 * managed on the Users page.
 */
export function GeneralTab({ form, setForm, group, slugError, nameError }: {
  form: GroupForm
  setForm: (next: GroupForm) => void
  /** The saved group; null while creating. */
  group: Group | null
  slugError?: string
  nameError?: string
}) {
  const t = useGroupsT()
  return (
    <div className="flex flex-col gap-6">
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <Field label={t('admin:groups.field.slug')} required={!group} error={slugError} hint={group ? t('admin:groups.field.slug_hint') : undefined}>
          <Input className="font-mono" value={form.slug} disabled={!!group} onChange={e => setForm({ ...form, slug: e.target.value })} />
        </Field>
        <Field label={t('admin:groups.field.name')} required error={nameError}>
          <Input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} />
        </Field>
      </div>
      <Field label={t('admin:groups.field.remark')}>
        <Input value={form.remark} onChange={e => setForm({ ...form, remark: e.target.value })} />
      </Field>
      {group && (
        <div className="flex flex-col gap-1 text-sm">
          <p>{t('admin:groups.members.count', { count: group.members })}</p>
          <p className="text-xs text-muted-foreground">
            {t('admin:groups.members.hint')}{' '}
            <Link to="/admin/users" className="underline underline-offset-2 outline-none focus-visible:ring-3 focus-visible:ring-ring/50">{t('admin:groups.action.members')}</Link>
          </p>
        </div>
      )}
      <FormSection title={t('admin:groups.general.security_title')}>
        <Field label={t('admin:groups.field.require_2fa')} hint={t('admin:groups.field.require_2fa_hint')} orientation="horizontal">
          <Switch checked={form.require_2fa} onCheckedChange={c => setForm({ ...form, require_2fa: c })} />
        </Field>
      </FormSection>
    </div>
  )
}

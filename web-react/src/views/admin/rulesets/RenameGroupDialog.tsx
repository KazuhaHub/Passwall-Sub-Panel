import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { RuleSet } from '@/api/rules'
import { FormDialog } from '@/components/data/FormDialog'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { planRename, type RenamePlan } from '@/utils/ruleset/rename'
import { newGroupNameProblem } from './targets'

/**
 * Renames a proxy group (design 3.11). The dialog is the confirmation: it says how many rule lines and member references
 * the rename rewrites before anything is touched, and the name goes through the same check a new group's name does (not
 * taken by a group, a built-in outbound or a Rematch outbound; nothing that could not be written into a rule line).
 *
 * The rewrite itself is `planRename`: a pure function of the draft. The dialog hands the plan to the page, which applies it.
 */
export function RenameGroupDialog({ draft, from, groupNames, rematchNames, onApply, onClose }: {
  draft: RuleSet
  from: string
  /** Every proxy group the server lists for this draft (the names a new one must not take). */
  groupNames: readonly string[]
  rematchNames: readonly string[]
  onApply: (plan: Extract<RenamePlan, { ok: true }>, to: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const [name, setName] = useState(from)
  const [attempted, setAttempted] = useState(false)
  const to = name.trim()
  const problem = to === from ? null : newGroupNameProblem(to, { groups: [...groupNames], rematch: [...rematchNames] })
  const plan = useMemo(() => (to === from || problem ? null : planRename(draft, from, to)), [draft, from, to, problem])
  const refused = plan && !plan.ok ? plan.reason : null
  const unchanged = to === from
  const ready = plan !== null && plan.ok

  return (
    <FormDialog
      title={t('admin:rules.rename.title')}
      description={from}
      dirty={!unchanged}
      pending={false}
      submitLabel={t('admin:rules.rename.submit')}
      submitDisabled={!ready}
      onClose={onClose}
      onSubmit={() => {
        setAttempted(true)
        if (plan && plan.ok) onApply(plan, to)
      }}
    >
      <Field label={t('admin:rules.rename.new_name')} hint={t('admin:rules.rename.hint')} error={(attempted || !unchanged) && problem ? t(`admin:rules.target.${problem}`) : undefined}>
        <Input value={name} autoFocus onChange={e => setName(e.target.value)} />
      </Field>
      {refused && refused !== 'same' && <p role="alert" className="text-sm text-destructive">{t(`admin:rules.rename.unavailable.${refused}`)}</p>}
      {plan && plan.ok && <p role="status" className="text-sm">{t('admin:rules.rename.confirm', { rules: plan.rules.toLocaleString(), refs: plan.refs.toLocaleString() })}</p>}
    </FormDialog>
  )
}

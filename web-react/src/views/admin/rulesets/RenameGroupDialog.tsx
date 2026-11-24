import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { ProxyGroupMember, RuleSet } from '@/api/rules'
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
export function RenameGroupDialog({ draft, from, effective, templates, rematchNames, onApply, onClose }: {
  draft: RuleSet
  from: string
  /** The backend's effective members of every proxy group of this draft (defaults included): what the rename keeps as it is. */
  effective: ReadonlyMap<string, readonly ProxyGroupMember[]>
  /** The templates this rule set is bound to: where the old name lives on in other rule sets. */
  templates: readonly string[]
  rematchNames: readonly string[]
  onApply: (plan: Extract<RenamePlan, { ok: true }>, to: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const [name, setName] = useState(from)
  const [attempted, setAttempted] = useState(false)
  const to = name.trim()
  const problem = to === from ? null : newGroupNameProblem(to, { groups: [...effective.keys()], rematch: [...rematchNames] })
  const plan = useMemo(() => (to === from || problem ? null : planRename(draft, from, to, effective)), [draft, from, to, problem, effective])
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
      {plan && plan.ok && (
        <div role="status" className="flex flex-col gap-2 text-sm">
          <p>{t('admin:rules.rename.confirm', { rules: plan.rules.toLocaleString(), refs: plan.refs.toLocaleString(), lists: plan.materialised.toLocaleString() })}</p>
          {/* What it does not reach (render.go: the template's own proxy_group_order, other rule sets and personal rules are all read by name). */}
          <p className="text-muted-foreground">{templates.length > 0 ? t('admin:rules.rename.not_reached', { templates: templates.join('、') }) : t('admin:rules.rename.not_reached_unbound')}</p>
        </div>
      )}
    </FormDialog>
  )
}

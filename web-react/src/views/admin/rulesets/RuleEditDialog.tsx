import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { FormDialog } from '@/components/data/FormDialog'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { validateRuleValue } from '@/utils/ruleset/validate'
import { COMMON_TYPES, IP_TYPES, MORE_TYPES } from '@/utils/ruleset/types'
import { singboxSupports, typeName } from './ruleTypes'

export interface RuleDraft {
  type: string
  value: string
  options: string[]
}

/**
 * One rule, as a form (design 3.5): its type, what it matches, and for address rules whether the domain may be
 * resolved to check it. The target is not here: a rule goes where its group goes, and "Move to…" changes that. Used for
 * a new rule and for an existing one; the caller turns the answer into the edit.
 */
export function RuleEditDialog({ mode, groupName, initial, onSubmit, onClose }: {
  mode: 'add' | 'edit'
  /** The group the rule belongs to, named in the title. */
  groupName: string
  initial: RuleDraft
  onSubmit: (rule: RuleDraft) => void
  onClose: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const [type, setType] = useState(initial.type)
  const [value, setValue] = useState(initial.value)
  const [options, setOptions] = useState(initial.options)
  const [attempted, setAttempted] = useState(false)
  const upper = type.toUpperCase()
  const ip = IP_TYPES.has(upper)
  const problem = validateRuleValue(type, value)
  const dirty = type !== initial.type || value !== initial.value || options.join() !== initial.options.join()
  const listed = [...COMMON_TYPES, ...MORE_TYPES] as string[]

  const setNoResolve = (on: boolean) => setOptions(o => {
    const rest = o.filter(x => x.toLowerCase() !== 'no-resolve')
    return on ? [...rest, 'no-resolve'] : rest
  })

  return (
    <FormDialog
      title={mode === 'add' ? t('admin:rules.matchers.add_title', { target: groupName }) : t('admin:rules.matchers.edit_title')}
      description={groupName}
      dirty={mode === 'add' ? true : dirty}
      pending={false}
      submitLabel={mode === 'add' ? t('admin:rules.matchers.add') : t('common:actions.save')}
      onClose={onClose}
      onSubmit={() => {
        setAttempted(true)
        if (problem) return
        onSubmit({ type: type.trim(), value: value.trim(), options })
      }}
    >
      <Field label={t('admin:rules.matchers.col_type')} hint={singboxSupports(type) ? t('admin:rules.matchers.support_both') : t('admin:rules.matchers.support_mihomo')}>
        <NativeSelect value={type} onChange={e => {
          const next = e.target.value
          setType(next)
          // A new address rule starts with "do not resolve", as every address rule of the seeded set has it.
          if (mode === 'add' && IP_TYPES.has(next.toUpperCase()) && !IP_TYPES.has(upper)) setNoResolve(true)
          if (!IP_TYPES.has(next.toUpperCase())) setNoResolve(false)
        }}>
          {!listed.includes(upper) && <option value={type}>{type}</option>}
          {COMMON_TYPES.map(c => <option key={c} value={c}>{`${typeName(t, c, true)} · ${c}`}</option>)}
          <optgroup label={t('admin:rules.matchers.more_types')}>
            {MORE_TYPES.map(c => <option key={c} value={c}>{`${typeName(t, c, true)} · ${c}`}</option>)}
          </optgroup>
        </NativeSelect>
      </Field>
      <Field label={t('admin:rules.matchers.col_value')} error={attempted && problem ? t(`admin:rules.validate.${problem}`) : undefined}>
        <Input value={value} className="font-mono" autoFocus onChange={e => setValue(e.target.value)} />
      </Field>
      {ip && (
        <Field label={t('admin:rules.matchers.no_resolve_label')} hint={t('admin:rules.matchers.no_resolve_hint')} orientation="horizontal">
          <Switch checked={options.some(o => o.toLowerCase() === 'no-resolve')} onCheckedChange={setNoResolve} />
        </Field>
      )}
    </FormDialog>
  )
}

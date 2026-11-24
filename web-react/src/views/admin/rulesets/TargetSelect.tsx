import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { BUILTIN_TARGETS } from '@/utils/ruleset/types'
import { newGroupNameProblem, type TargetChoices } from './targets'

const NEW = '\u0000new'

/**
 * Where a rule sends traffic (design 3.4): the existing proxy groups, the built-in outbounds with what each does, and
 * the Rematch outbounds, plus a deliberate "New proxy group…" that asks for a name and checks it. `onChange` is told
 * only about a target that is usable, so a half-typed new name never reaches the caller.
 */
export function TargetSelect({ value, onChange, choices, label, allowNew = true, disabled }: {
  value: string
  onChange: (target: string) => void
  choices: TargetChoices
  label: string
  allowNew?: boolean
  disabled?: boolean
}) {
  const { t } = useTranslation(['admin'])
  // What the select shows is its own state: the caller applies a change later (a button says what it will do), so
  // `value` stays the current target meanwhile.
  const [shown, setShown] = useState(value)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState('')
  const known = choices.groups.includes(shown) || choices.rematch.includes(shown) || (BUILTIN_TARGETS as readonly string[]).includes(shown)
  const problem = creating ? newGroupNameProblem(name, choices) : null

  return (
    <div className="flex flex-col gap-2">
      <Field label={label}>
        <NativeSelect
          value={creating ? NEW : shown}
          disabled={disabled}
          onChange={e => {
            if (e.target.value === NEW) { setCreating(true); return }
            setCreating(false)
            setShown(e.target.value)
            onChange(e.target.value)
          }}
        >
          {/* A target the lists do not know (a line name, a group not yet created) is still the current value. */}
          {!known && shown !== '' && <option value={shown}>{shown}</option>}
          {shown === '' && !creating && <option value="" disabled />}
          {choices.groups.length > 0 && (
            <optgroup label={t('admin:rules.target.groups')}>
              {choices.groups.map(g => <option key={g} value={g}>{g}</option>)}
            </optgroup>
          )}
          <optgroup label={t('admin:rules.target.builtins')}>
            {BUILTIN_TARGETS.map(b => <option key={b} value={b}>{`${b} · ${t(`admin:rules.target.builtin.${b}`)}`}</option>)}
          </optgroup>
          {choices.rematch.length > 0 && (
            <optgroup label={t('admin:rules.target.rematch')}>
              {choices.rematch.map(g => <option key={g} value={g}>{g}</option>)}
            </optgroup>
          )}
          {allowNew && <option value={NEW}>{t('admin:rules.target.new_group')}</option>}
        </NativeSelect>
      </Field>
      {creating && (
        <Field label={t('admin:rules.target.new_group_name')} error={name !== '' && problem ? t(`admin:rules.target.${problem}`) : undefined}>
          <Input
            value={name}
            autoFocus
            onChange={e => {
              const next = e.target.value
              setName(next)
              if (newGroupNameProblem(next, choices) === null) onChange(next.trim())
            }}
          />
        </Field>
      )}
    </div>
  )
}

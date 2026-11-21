import type { ComponentProps, ReactNode } from 'react'
import { Autocomplete, AutocompleteContent, AutocompleteInput, AutocompleteItem, AutocompleteList } from '@/components/ui/autocomplete'
import { Field } from '@/components/form/Field'
import { FormValidityProvider } from '@/components/form/FormValidity'
import { NumberField } from '@/components/form/NumberField'
import { SaveBar } from '@/components/form/SaveBar'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { IconButton } from '@/components/common/IconButton'
import { useSpacedT } from '@/i18n/spacing'
import { IconArrowDown, IconArrowUp, IconDelete } from '@/lib/icons'
import { cn } from '@/lib/utils'
import { settingId } from './model'

/**
 * One section's frame: its form, its validity scope and its own save bar. A section saves on its own, so each
 * has one of each; the page keeps them mounted (hidden) while another section is shown, so a draft survives a switch.
 */
export function SectionForm({ dirty, pending, onSave, onDiscard, children, className, plain }: {
  /** No <form>: for a section that hosts dialogs with forms of their own (their submits would bubble through the portal). */
  plain?: boolean
  dirty: boolean
  pending: boolean
  onSave: () => void
  onDiscard: () => void
  children: ReactNode
  className?: string
}) {
  const body = <>{children}<SaveBar dirty={dirty} pending={pending} onSave={onSave} onDiscard={onDiscard} /></>
  return (
    <FormValidityProvider>
      {plain
        ? <div className={cn('flex flex-col gap-5', className)}>{body}</div>
        : <form className={cn('flex flex-col gap-5', className)} noValidate onSubmit={e => { e.preventDefault(); onSave() }}>{body}</form>}
    </FormValidityProvider>
  )
}

/** A titled group of settings, the unit a section is read in. */
export function Group({ title, description, children, className, id }: {
  title: ReactNode
  description?: ReactNode
  children: ReactNode
  className?: string
  id?: string
}) {
  return (
    <Card id={id}>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent className={cn('grid gap-4', className)}>{children}</CardContent>
    </Card>
  )
}

/** Two fields side by side from a tablet up, stacked on a phone. */
export function Pair({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">{children}</div>
}

/** Fields that only apply while a switch above them is on, set apart from the rest. */
export function Nested({ children }: { children: ReactNode }) {
  return <div className="grid gap-4 border-l-2 border-border pl-4">{children}</div>
}

export function Hint({ children }: { children: ReactNode }) {
  return <p className="text-xs text-muted-foreground">{children}</p>
}

export function SwitchField({ name, label, hint, checked, onChange, disabled, error }: {
  /** The setting's key: the control's id, which the search jumps to. */
  name: string
  label: ReactNode
  hint?: ReactNode
  checked: boolean
  onChange: (next: boolean) => void
  disabled?: boolean
  error?: ReactNode
}) {
  return (
    <Field orientation="horizontal" label={label} hint={hint} error={error}>
      <Switch id={settingId(name)} checked={checked} disabled={disabled} onCheckedChange={c => onChange(c)} />
    </Field>
  )
}

export function NumField({ name, label, hint, unit, value, onChange, min = 0, max, integer = true, zeroLabel, disabled, error }: {
  name: string
  label: ReactNode
  hint?: ReactNode
  unit?: string
  value: number
  onChange: (next: number) => void
  min?: number
  max?: number
  /** Whole numbers only. The two quota fields that take fractions pass false. */
  integer?: boolean
  zeroLabel?: string
  disabled?: boolean
  error?: ReactNode
}) {
  return (
    <Field label={label} hint={hint} error={error}>
      <NumberField id={settingId(name)} value={value} min={min} max={max} integer={integer} unit={unit} zeroLabel={zeroLabel} disabled={disabled}
        onChange={v => { if (v !== null) onChange(v) }} />
    </Field>
  )
}

export function TextRow({ name, label, hint, value, onChange, onBlur, placeholder, error, required, ...rest }: {
  name: string
  label: ReactNode
  hint?: ReactNode
  value: string
  onChange: (next: string) => void
  onBlur?: () => void
  placeholder?: string
  error?: ReactNode
  required?: boolean
} & Omit<ComponentProps<typeof Input>, 'value' | 'onChange' | 'onBlur' | 'name' | 'placeholder' | 'required'>) {
  return (
    <Field label={label} hint={hint} error={error} required={required}>
      <Input id={settingId(name)} value={value} placeholder={placeholder} onChange={e => onChange(e.target.value)} onBlur={onBlur} {...rest} />
    </Field>
  )
}

export function SelectRow<V extends string>({ name, label, hint, value, onChange, options, disabled }: {
  name: string
  label: ReactNode
  hint?: ReactNode
  value: V
  onChange: (next: V) => void
  options: readonly { value: V; label: string }[]
  disabled?: boolean
}) {
  return (
    <Field label={label} hint={hint}>
      <NativeSelect id={settingId(name)} value={value} disabled={disabled} onChange={e => onChange(e.target.value as V)}>
        {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
      </NativeSelect>
    </Field>
  )
}

/** Free text with a list of suggestions that narrows as you type; the value is the text itself (a timezone, a role). */
export function SuggestInput({ id, value, onChange, options, placeholder, onBlur, ...aria }: {
  id?: string
  value: string
  onChange: (next: string) => void
  options: readonly string[]
  placeholder?: string
  onBlur?: () => void
  'aria-label'?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean | 'true' | 'false'
}) {
  return (
    <Autocomplete items={options as string[]} value={value} onValueChange={v => onChange(v)}>
      <AutocompleteInput id={id} placeholder={placeholder} onBlur={onBlur} {...aria} />
      <AutocompleteContent>
        <AutocompleteList>
          {(item: string) => <AutocompleteItem key={item} value={item}>{item}</AutocompleteItem>}
        </AutocompleteList>
      </AutocompleteContent>
    </Autocomplete>
  )
}

/** Move up, move down and remove for one row of an ordered list. Order is meaning (first match wins, display order), so it is reachable without a pointer. */
export function RowTools({ index, count, name, onMove, onRemove }: {
  index: number
  count: number
  /** What the row is, for the buttons' names ("Quick link 2"). */
  name: string
  onMove: (to: number) => void
  onRemove: () => void
}) {
  const { t } = useSpacedT()
  return (
    <div className="flex shrink-0 items-center gap-0.5">
      <IconButton label={t('settings.rows.move_up', { name })} size="icon-sm" aria-disabled={index === 0 || undefined} onClick={() => onMove(index - 1)}><IconArrowUp /></IconButton>
      <IconButton label={t('settings.rows.move_down', { name })} size="icon-sm" aria-disabled={index === count - 1 || undefined} onClick={() => onMove(index + 1)}><IconArrowDown /></IconButton>
      <IconButton label={t('settings.rows.remove', { name })} size="icon-sm" onClick={onRemove}><IconDelete /></IconButton>
    </div>
  )
}

/** Moves one element of a list to another place, as the up and down buttons do. */
export function moved<T>(list: readonly T[], from: number, to: number): T[] {
  if (from === to || to < 0 || to >= list.length) return list.slice()
  const next = list.slice()
  const [item] = next.splice(from, 1)
  next.splice(to, 0, item)
  return next
}

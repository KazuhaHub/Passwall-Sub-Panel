import type { Dispatch, ReactNode, SetStateAction } from 'react'
import type { PanelType } from '@/api/servers'
import { Field } from '@/components/form/Field'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'
import type { InboundFormState } from './inboundModel'

/** What every group of fields of the form is given. */
export interface SectionProps {
  form: InboundFormState
  setForm: Dispatch<SetStateAction<InboundFormState>>
  update: <K extends keyof InboundFormState>(key: K, value: InboundFormState[K]) => void
  /** Of the server the inbound lives on; the S-UI and Passwall Node adapters take a canonical subset of the 3X-UI options. */
  panelType: PanelType
}

/** Keys and passwords are read character by character; a monospace face keeps them legible. */
export const MONO = 'font-mono text-[13px]'

/** The smaller heading of a group of fields inside a section. */
export function SubHeading({ children }: { children: ReactNode }) {
  return <h4 className="text-xs font-medium text-muted-foreground">{children}</h4>
}

/** Fields side by side where there is room. */
export function FieldRow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('grid grid-cols-1 items-start gap-4 sm:grid-cols-2', className)}>{children}</div>
}

/** A switch with its label beside it. A dangerous option is named in the error colour so it is not toggled by habit. */
export function SwitchField({ label, checked, onChange, danger, hint }: {
  label: string
  checked: boolean
  onChange: (checked: boolean) => void
  danger?: boolean
  hint?: ReactNode
}) {
  return (
    <Field orientation="horizontal" className="justify-start gap-3" hint={hint}
      label={danger ? <span className="text-destructive">{label}</span> : label}>
      <Switch checked={checked} onCheckedChange={onChange} />
    </Field>
  )
}

/** A text box with its label above; `mono` for keys, passwords and paths. */
export function TextField({ label, value, onChange, placeholder, mono, required, hint, type, disabled, className }: {
  label: string
  value: string | number
  onChange: (value: string) => void
  placeholder?: string
  mono?: boolean
  required?: boolean
  hint?: ReactNode
  type?: 'text' | 'number'
  disabled?: boolean
  className?: string
}) {
  return (
    <Field label={label} hint={hint} required={required} className={className}>
      <Input type={type} value={value} placeholder={placeholder} disabled={disabled} className={mono ? MONO : undefined}
        onChange={e => onChange(e.target.value)} />
    </Field>
  )
}

/** A multi-line box in the monospace face (PEM blocks, raw JSON). */
export function AreaField({ label, value, onChange, placeholder, hint, rows = 4 }: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  hint?: ReactNode
  rows?: number
}) {
  return (
    <Field label={label} hint={hint}>
      <Textarea rows={rows} value={value} placeholder={placeholder} spellCheck={false} className={cn(MONO, 'max-h-80 text-xs')}
        onChange={e => onChange(e.target.value)} />
    </Field>
  )
}

/** A native list of fixed choices. */
export function SelectField({ label, value, onChange, options, disabled, hint, className }: {
  label: string
  value: string | number
  onChange: (value: string) => void
  options: { value: string | number; label: string }[]
  disabled?: boolean
  hint?: ReactNode
  className?: string
}) {
  return (
    <Field label={label} hint={hint} className={className}>
      <NativeSelect value={value} disabled={disabled} onChange={e => onChange(e.target.value)}>
        {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
      </NativeSelect>
    </Field>
  )
}

/** The options of a list of plain values, an empty one shown as a dash. */
export const dashed = (values: readonly string[]) => values.map(v => ({ value: v, label: v || '—' }))

import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Field } from '@/components/form/Field'
import { NumberField } from '@/components/form/NumberField'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

/** A field's value: a number, a switch or a text. */
export type FieldValue = number | boolean | string

export type FieldKind = 'number' | 'float' | 'switch' | 'inverted' | 'select' | 'text'

/**
 * What a field is, structurally. The risk center's PolicyFieldSpec is one;
 * the access-control settings write their own. `label`, `hint` and the
 * select's option labels are full i18n keys, namespace included.
 */
export interface FieldSpec {
  key: string
  kind: FieldKind
  label: string
  hint?: string
  /** Stored values above 0 outside these bounds are marked and block the save. */
  min?: number
  max?: number
  step?: number
  /** The helper text ends in the composed range and served default. */
  tail?: boolean
  /** A runtime knob: its caption is the value the server runs with. */
  effective?: boolean
  multiline?: boolean
  /** 'select' only. */
  options?: { value: string; label: string }[]
}

/** Whether a set number is outside the field's bounds. 0 is "unset" and never is. */
export function outOfRange(spec: FieldSpec, value: unknown): boolean {
  if ((spec.kind !== 'number' && spec.kind !== 'float') || typeof value !== 'number' || !(value > 0)) return false
  return (spec.min !== undefined && value < spec.min) || (spec.max !== undefined && value > spec.max)
}

/** The words every field shares (range, default). The risk center's own block, which the
 *  access-control page reads too: they say nothing of either page. */
const SHARED = 'admin:risk_center.policy.'

/**
 * ONE POLICY FIELD, of any kind the layout names. Controlled: `value` is the
 * draft's, `onChange` writes the draft; the field keeps no copy of the
 * stored value.
 *
 * A number is the shared NumberField, which keeps the text as typed ("0." is
 * not a number yet, so "0.5" can be typed) and tells the page the number the
 * text reads as. The stored 0 means UNSET, and the box shows that as an empty
 * field whose placeholder is the served default, never as a 0 that would read as "zero tolerance": the
 * served default is in the hint, and a set value carries a "follow again"
 * button that stores 0.
 */
export default function PolicyField<V extends FieldValue = FieldValue>({ spec, value, onChange, defaults, effective, error }: {
  spec: FieldSpec
  value: V | undefined
  onChange: (v: V) => void
  /** The served defaults (ports.RiskCenterPolicyDefaults). */
  defaults: Record<string, number>
  /** A runtime knob's value in effect, when the server sent it. */
  effective?: number
  /** An error the server named for this field (a refused ignore list). */
  error?: string
}) {
  const { t } = useTranslation(['admin'])
  const label = t(spec.label)
  const hint = spec.hint ? t(spec.hint) : ''

  if (spec.kind === 'switch' || spec.kind === 'inverted') {
    // An *_off key reads as its opposite: the switch says what is ON.
    const on = spec.kind === 'inverted' ? value !== true : value === true
    return (
      <Field label={label} hint={hint || undefined} orientation="horizontal">
        <Switch checked={on} onCheckedChange={c => onChange((spec.kind === 'inverted' ? !c : c) as V)} />
      </Field>
    )
  }

  if (spec.kind === 'select') {
    // '' is "never configured", which the server judges as the first
    // option (the city tier); shown as that option rather than as blank.
    const options = spec.options ?? []
    const shown = typeof value === 'string' && options.some(o => o.value === value) ? value : (options[0]?.value ?? '')
    return (
      <Field label={label} hint={hint || undefined}>
        <NativeSelect value={shown} onChange={e => onChange(e.target.value as V)}>
          {options.map(o => <option key={o.value} value={o.value}>{t(o.label)}</option>)}
        </NativeSelect>
      </Field>
    )
  }

  if (spec.kind === 'text') {
    const text = typeof value === 'string' ? value : ''
    return (
      <Field label={label} hint={hint || undefined} error={error} errorBlocksSave={false}>
        {spec.multiline
          ? <Textarea rows={2} value={text} onChange={e => onChange(e.target.value as V)} />
          : <Input value={text} onChange={e => onChange(e.target.value as V)} />}
      </Field>
    )
  }

  const stored = typeof value === 'number' ? value : 0
  const fallback = defaults[spec.key]
  // The hint says what the knob means. A † field adds its range and the served default; a runtime
  // knob's hint states its own, and its caption is the number the server runs with.
  let tail = ''
  if (spec.tail && fallback !== undefined) {
    const d = { min: spec.min, max: spec.max, d: fallback }
    tail = spec.min !== undefined && spec.max !== undefined ? t(`${SHARED}range_default`, d)
      : spec.min !== undefined ? t(`${SHARED}min_default`, d)
        : t(`${SHARED}default_only`, d)
  }
  const lines: ReactNode[] = [
    [hint, tail].filter(Boolean).join(' '),
    spec.effective && effective !== undefined && t('admin:settings.risk_center.effective', { value: effective }),
  ].filter(Boolean)

  return (
    <Field label={label} hint={lines.length > 0 ? <>{lines.map((line, i) => <span key={i} className="block">{line}</span>)}</> : undefined}>
      <NumberField
        value={stored > 0 ? stored : null}
        onChange={n => onChange((n === null ? 0 : n) as V)}
        integer={spec.kind === 'number'}
        min={spec.min} max={spec.max}
        allowEmpty placeholder={fallback === undefined ? undefined : String(fallback)}
      />
    </Field>
  )
}

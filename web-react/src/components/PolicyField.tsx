import { useCallback, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Field } from '@/components/form/Field'
import { NumberField } from '@/components/form/NumberField'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { LazyCodeEditor } from '@/components/LazyCodeEditor'
import { spacedT } from '@/i18n/spacing'

/** `t` for the field's words: keys keep their `admin:` prefix, interpolation boundaries get Latin-letter spacing. */
function useSpacedAdmin() {
  const { t } = useTranslation(['admin', 'common'])
  // eslint-disable-next-line react-hooks/exhaustive-deps
  return { t: useCallback(spacedT(t), [t]) }
}

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

/** The words every field shares (range, default). */
const SHARED = 'admin:risk_center.policy.'

/**
 * ONE POLICY FIELD, of any kind the layout names. Controlled: `value` is the draft's, `onChange` writes
 * the draft; the field keeps no copy of the stored value.
 *
 * A number is stored as 0 when it is unset, and an unset value is an EMPTY box that says 默认 and the
 * served default — never a 0 that reads as "zero tolerance". NumberField supplies the editing rules (the
 * text stays as typed until the box is left, "0.5" can be typed one character at a time, a value outside
 * the bounds is named on the line under the box and blocks the save) and the way back: emptying the box,
 * or its reset button, stores 0 again.
 */
export default function PolicyField({ spec, value, onChange, defaults, effective, error }: {
  spec: FieldSpec
  value: FieldValue | undefined
  onChange: (v: FieldValue) => void
  /** The served defaults (ports.RiskCenterPolicyDefaults). */
  defaults: Record<string, number>
  /** A runtime knob's value in effect, when the server sent it. */
  effective?: number
  /** An error the server named for this field (a refused ignore list). */
  error?: string
}) {
  const { t } = useSpacedAdmin()
  const label = t(spec.label)
  const hint = spec.hint ? t(spec.hint) : ''

  if (spec.kind === 'switch' || spec.kind === 'inverted') {
    // An *_off key reads as its opposite: the switch says what is ON.
    const on = spec.kind === 'inverted' ? value !== true : value === true
    return (
      <Field label={label} hint={hint || undefined} orientation="horizontal">
        <Switch checked={on} onCheckedChange={c => onChange(spec.kind === 'inverted' ? !c : c)} />
      </Field>
    )
  }

  if (spec.kind === 'select') {
    // '' is "never configured", which the server judges as the first option (the city tier); shown as
    // that option rather than as blank.
    const options = spec.options ?? []
    const shown = typeof value === 'string' && options.some(o => o.value === value) ? value : (options[0]?.value ?? '')
    return (
      <Field label={label} hint={hint || undefined}>
        <NativeSelect value={shown} onChange={e => onChange(e.target.value)}>
          {options.map(o => <option key={o.value} value={o.value}>{t(o.label)}</option>)}
        </NativeSelect>
      </Field>
    )
  }

  if (spec.kind === 'text') {
    const text = typeof value === 'string' ? value : ''
    return (
      // A refusal that came back from a save is a message to show, not a reason Save is off: the admin
      // may only need to try again.
      <Field label={label} hint={hint || undefined} error={error || undefined} errorBlocksSave={false}>
        {spec.multiline
          ? <LazyCodeEditor language="plain" tabMovesFocus minRows={2} maxRows={8} value={text} onChange={onChange} />
          : <Input value={text} onChange={e => onChange(e.target.value)} />}
      </Field>
    )
  }

  const stored = typeof value === 'number' ? value : 0
  const fallback = defaults[spec.key]
  // The hint says what the knob means. A † field adds its range and the served default; a runtime knob's
  // hint states its own, and its caption is the number the server runs with.
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
  const emptyLabel = fallback === undefined ? t(`${SHARED}default_adornment`) : `${t(`${SHARED}default_adornment`)} ${fallback}`
  return (
    <NumberBox spec={spec} stored={stored} emptyLabel={emptyLabel} onChange={onChange}
      label={label} hint={lines.length > 0 ? <>{lines.map((line, i) => <span key={i} className="block">{line}</span>)}</> : undefined} />
  )
}

/**
 * The number box. 0 has always meant "unset, use the default" and the server accepts it, so it must stay
 * enterable even where the field has a minimum: the box enforces the minimum on every other value, and a
 * 0 left in it is taken as "back to the default" when the box is left — it stores 0 and the box is drawn
 * again empty, as the default.
 */
function NumberBox({ spec, stored, emptyLabel, onChange, label, hint }: {
  spec: FieldSpec
  stored: number
  emptyLabel: string
  onChange: (v: FieldValue) => void
  label: string
  hint: ReactNode
}) {
  const [resets, setResets] = useState(0)
  return (
    <Field label={label} hint={hint}>
      <NumberField key={resets} value={stored > 0 ? stored : null} integer={spec.kind === 'number'} min={spec.min} max={spec.max}
        allowEmpty emptyLabel={emptyLabel}
        onChange={n => onChange(n === null ? 0 : n)}
        onBlur={e => {
          const text = e.currentTarget.value.trim()
          if (spec.min !== undefined && spec.min > 0 && text !== '' && Number.parseFloat(text) === 0) {
            onChange(0)
            setResets(k => k + 1)
          }
        }} />
    </Field>
  )
}

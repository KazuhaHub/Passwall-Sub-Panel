import { useState, type ReactNode } from 'react'
import {
  Box, Chip, FormControlLabel, IconButton, InputAdornment, MenuItem, Switch, TextField, Tooltip, Typography,
} from '@mui/material'
import RestartAltIcon from '@mui/icons-material/RestartAlt'
import { useTranslation } from 'react-i18next'

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

/** The words every field shares (range, default, reset). The risk center's
 *  own block, which the access-control page reads too: they say nothing of
 *  either page. */
const SHARED = 'admin:risk_center.policy.'

/**
 * ONE POLICY FIELD, of any kind the layout names. Controlled: `value` is the
 * draft's, `onChange` writes the draft; the field keeps no copy of the
 * stored value.
 *
 * A number is typed as text (U2). A number input that re-renders from the
 * store on every keystroke cannot take "0.5" — "0." is not a number yet —
 * so while the field has focus it shows the text as typed and stores the
 * number that text reads as (its numeric prefix; '' is 0, unset). On blur
 * the text is dropped and the field shows the store again: an unset value
 * (<= 0) is an empty field whose placeholder is the SERVED default and
 * which says 默认 — never a 0 that reads as "zero tolerance" — and a set
 * value carries a reset that stores 0.
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
      <Box>
        <FormControlLabel label={label} sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }}
          control={<Switch checked={on} onChange={(_, c) => onChange((spec.kind === 'inverted' ? !c : c) as V)} />} />
        {hint && <Typography sx={{ fontSize: 12, color: 'text.secondary', mt: -0.5 }}>{hint}</Typography>}
      </Box>
    )
  }

  if (spec.kind === 'select') {
    // '' is "never configured", which the server judges as the first
    // option (the city tier); shown as that option rather than as blank.
    const options = spec.options ?? []
    const shown = typeof value === 'string' && options.some(o => o.value === value) ? value : (options[0]?.value ?? '')
    return (
      <TextField select fullWidth label={label} value={shown} helperText={hint}
        onChange={e => onChange(e.target.value as V)}>
        {options.map(o => <MenuItem key={o.value} value={o.value}>{t(o.label)}</MenuItem>)}
      </TextField>
    )
  }

  if (spec.kind === 'text') {
    return (
      <TextField fullWidth label={label} value={typeof value === 'string' ? value : ''}
        multiline={spec.multiline} minRows={spec.multiline ? 2 : undefined}
        error={!!error} helperText={error ? <>{error}<Box component="span" sx={{ display: 'block' }}>{hint}</Box></> : hint}
        onChange={e => onChange(e.target.value as V)} />
    )
  }

  return <NumberField spec={spec} label={label} hint={hint} value={value} onChange={onChange}
    defaults={defaults} effective={effective} />
}

function NumberField<V extends FieldValue>({ spec, label, hint, value, onChange, defaults, effective }: {
  spec: FieldSpec
  label: string
  hint: string
  value: V | undefined
  onChange: (v: V) => void
  defaults: Record<string, number>
  effective?: number
}) {
  const { t } = useTranslation(['admin'])
  // The text as typed, while the field has focus; null otherwise.
  const [text, setText] = useState<string | null>(null)
  const stored = typeof value === 'number' ? value : 0
  const set = stored > 0
  const fallback = defaults[spec.key]
  const invalid = outOfRange(spec, stored)

  const onText = (raw: string) => {
    setText(raw)
    if (raw.trim() === '') { onChange(0 as V); return }
    // parseFloat reads the numeric prefix: "0." is 0 and "1e" is 1 while the
    // admin is still typing. Text with no number in it changes nothing.
    const n = Number.parseFloat(raw)
    if (!Number.isFinite(n)) return
    onChange((spec.kind === 'number' ? Math.trunc(n) : n) as V)
  }

  // The hint says what the knob means. A † field adds its range and the
  // served default; a runtime knob's hint states its own, and its caption
  // is the number the server runs with.
  let tail = ''
  if (spec.tail && fallback !== undefined) {
    const d = { min: spec.min, max: spec.max, d: fallback }
    tail = spec.min !== undefined && spec.max !== undefined ? t(`${SHARED}range_default`, d)
      : spec.min !== undefined ? t(`${SHARED}min_default`, d)
        : t(`${SHARED}default_only`, d)
  }
  const lines: ReactNode[] = [
    invalid && t(`${SHARED}out_of_range`),
    [hint, tail].filter(Boolean).join(' '),
    spec.effective && effective !== undefined && t('admin:settings.risk_center.effective', { value: effective }),
  ].filter(Boolean)

  const adornment = set
    ? (
      <InputAdornment position="end">
        <Tooltip title={t(`${SHARED}reset_default`)}>
          <IconButton size="small" edge="end" aria-label={t(`${SHARED}reset_default`)}
            onClick={() => { setText(null); onChange(0 as V) }}>
            <RestartAltIcon fontSize="small" />
          </IconButton>
        </Tooltip>
      </InputAdornment>
    )
    : fallback !== undefined && text === null
      ? <InputAdornment position="end"><Chip size="small" label={t(`${SHARED}default_adornment`)} /></InputAdornment>
      : undefined

  return (
    <TextField fullWidth label={label}
      value={text ?? (set ? String(stored) : '')}
      placeholder={fallback === undefined ? undefined : String(fallback)}
      error={invalid}
      onFocus={() => setText(set ? String(stored) : '')}
      onBlur={() => setText(null)}
      onChange={e => onText(e.target.value)}
      helperText={lines.length > 0
        ? lines.map((line, i) => <Box key={i} component="span" sx={{ display: 'block' }}>{line}</Box>)
        : undefined}
      slotProps={{
        htmlInput: { inputMode: spec.kind === 'float' ? 'decimal' : 'numeric' },
        // Shrunk, so the placeholder (the default) shows in an empty field.
        inputLabel: { shrink: true },
        input: { endAdornment: adornment },
      }} />
  )
}

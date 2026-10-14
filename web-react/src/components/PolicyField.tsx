import { useState, type ReactNode } from 'react'
import {
  Box, Chip, FormControlLabel, IconButton, InputAdornment, MenuItem, Switch, TextField, Tooltip, Typography,
} from '@mui/material'
import RestartAltIcon from '@mui/icons-material/RestartAlt'
import { useTranslation } from 'react-i18next'

export type PolicyValue = number | boolean | string
export type PolicyFieldKind = 'number' | 'float' | 'switch' | 'inverted' | 'select' | 'text'
export type PolicyFieldCopyKey = 'range_default' | 'min_default' | 'default_only' | 'out_of_range' | 'effective' | 'reset_default' | 'default_adornment'
export interface PolicyFieldSpec {
  key: string
  label: string
  hint?: string
  kind?: PolicyFieldKind
  min?: number
  max?: number
  step?: number
  tail?: boolean
  effective?: boolean
  multiline?: boolean
  options?: { value: string; label: string }[]
  /** Full i18n keys for legacy/domain-specific helper wording. */
  copy?: Partial<Record<PolicyFieldCopyKey, string>>
}

export function outOfRange(spec: PolicyFieldSpec, value: unknown): boolean {
  if ((spec.kind != null && spec.kind !== 'number' && spec.kind !== 'float') || typeof value !== 'number' || !(value > 0)) return false
  return (spec.min !== undefined && value < spec.min) || (spec.max !== undefined && value > spec.max)
}

const DEFAULT_COPY = Object.fromEntries(['range_default', 'min_default', 'default_only', 'out_of_range', 'effective', 'reset_default', 'default_adornment'].map(key => [key, `common:policy_field.${key}`])) as Record<PolicyFieldCopyKey, string>

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
export default function PolicyField({ spec, value, onChange, defaults, effective, error }: {
  spec: PolicyFieldSpec
  value: PolicyValue | undefined
  onChange: (v: PolicyValue) => void
  /** Defaults supplied by the server, indexed by field key. */
  defaults: Record<string, number>
  /** A runtime knob's value in effect, when the server sent it. */
  effective?: number
  /** An error the server named for this field. */
  error?: string
}) {
  const { t } = useTranslation(['common', 'admin'])
  const label = t(spec.label)
  const hint = spec.hint ? t(spec.hint) : ''

  if (spec.kind === 'switch' || spec.kind === 'inverted') {
    // An *_off key reads as its opposite: the switch says what is ON.
    const on = spec.kind === 'inverted' ? value !== true : value === true
    return (
      <Box>
        <FormControlLabel label={label} sx={{ ml: 0, '& .MuiFormControlLabel-label': { ml: 1.5 } }}
          control={<Switch checked={on} onChange={(_, c) => onChange(spec.kind === 'inverted' ? !c : c)} />} />
        {hint && <Typography sx={{ fontSize: 12, color: 'text.secondary', mt: -0.5 }}>{hint}</Typography>}
      </Box>
    )
  }

  if (spec.kind === 'select') {
    // An unset or unknown value displays the first configured option.
    const options = spec.options ?? []
    const shown = typeof value === 'string' && options.some(o => o.value === value) ? value : (options[0]?.value ?? '')
    return (
      <TextField select fullWidth label={label} value={shown} helperText={hint}
        onChange={e => onChange(e.target.value as PolicyValue)}>
        {options.map(o => <MenuItem key={o.value} value={o.value}>{t(o.label)}</MenuItem>)}
      </TextField>
    )
  }

  if (spec.kind === 'text') {
    return (
      <TextField fullWidth label={label} value={typeof value === 'string' ? value : ''}
        multiline={spec.multiline} minRows={spec.multiline ? 2 : undefined}
        error={!!error} helperText={error ? <>{error}<Box component="span" sx={{ display: 'block' }}>{hint}</Box></> : hint}
        onChange={e => onChange(e.target.value)} />
    )
  }

  return <NumberField spec={spec} label={label} hint={hint} value={value} onChange={onChange}
    defaults={defaults} effective={effective} />
}

function NumberField({ spec, label, hint, value, onChange, defaults, effective }: {
  spec: PolicyFieldSpec
  label: string
  hint: string
  value: PolicyValue | undefined
  onChange: (v: PolicyValue) => void
  defaults: Record<string, number>
  effective?: number
}) {
  const { t } = useTranslation(['common', 'admin'])
  // The text as typed, while the field has focus; null otherwise.
  const [text, setText] = useState<string | null>(null)
  const stored = typeof value === 'number' ? value : 0
  const set = stored > 0
  const fallback = defaults[spec.key]
  const invalid = outOfRange(spec, stored)

  const onText = (raw: string) => {
    setText(raw)
    if (raw.trim() === '') { onChange(0); return }
    // parseFloat reads the numeric prefix: "0." is 0 and "1e" is 1 while the
    // admin is still typing. Text with no number in it changes nothing.
    const n = Number.parseFloat(raw)
    if (!Number.isFinite(n)) return
    onChange(spec.kind === 'float' ? n : Math.trunc(n))
  }

  // The hint says what the knob means. A † field adds its range and the
  // served default; a runtime knob's hint states its own, and its caption
  // is the number the server runs with.
  let tail = ''
  if (spec.tail && fallback !== undefined) {
    const d = { min: spec.min, max: spec.max, d: fallback }
    tail = spec.min !== undefined && spec.max !== undefined ? t(spec.copy?.range_default ?? DEFAULT_COPY.range_default, d)
      : spec.min !== undefined ? t(spec.copy?.min_default ?? DEFAULT_COPY.min_default, d)
        : t(spec.copy?.default_only ?? DEFAULT_COPY.default_only, d)
  }
  const lines: ReactNode[] = [
    invalid && t(spec.copy?.out_of_range ?? DEFAULT_COPY.out_of_range),
    [hint, tail].filter(Boolean).join(' '),
    spec.effective && effective !== undefined && t(spec.copy?.effective ?? DEFAULT_COPY.effective, { value: effective }),
  ].filter(Boolean)

  const adornment = set
    ? (
      <InputAdornment position="end">
        <Tooltip title={t(spec.copy?.reset_default ?? DEFAULT_COPY.reset_default)}>
          <IconButton size="small" edge="end" aria-label={t(spec.copy?.reset_default ?? DEFAULT_COPY.reset_default)}
            onClick={() => { setText(null); onChange(0) }}>
            <RestartAltIcon fontSize="small" />
          </IconButton>
        </Tooltip>
      </InputAdornment>
    )
    : fallback !== undefined && text === null
      ? <InputAdornment position="end"><Chip size="small" label={t(spec.copy?.default_adornment ?? DEFAULT_COPY.default_adornment)} /></InputAdornment>
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

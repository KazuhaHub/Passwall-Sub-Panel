import { useEffect, useId, useState, type ComponentProps } from 'react'
import { RotateCcwIcon } from 'lucide-react'
import { useSpacedT } from '@/lib/cjkSpacing'
import { Badge } from '@/components/ui/badge'
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from '@/components/ui/input-group'
import { TipButton } from '@/components/data/TipButton'

export interface NumberFieldProps
  extends Omit<ComponentProps<'input'>, 'value' | 'onChange' | 'type' | 'min' | 'max' | 'step' | 'size'> {
  /** null is an EMPTY box. It is never 0, and a page must not turn one into the other. */
  value: number | null
  onChange: (next: number | null) => void
  /** Suffix shown inside the box: GB, 天, 秒, ms. */
  unit?: string
  min?: number
  max?: number
  /** Whole numbers only: the decimal point is not accepted. */
  integer?: boolean
  /**
   * Empty is a real answer ("follow the level above"), not a missing one. Shows
   * `emptyLabel` as a badge while empty and a "follow again" button while a
   * number is set, so empty and 0 cannot be confused (audit section 3: the same
   * input meant "follow the group" on create and "unlimited" on edit).
   */
  allowEmpty?: boolean
  emptyLabel?: string
  /** What an explicit 0 means here ("不限量"), shown beside the 0 so it reads as chosen. */
  zeroLabel?: string
}

const fmt = (n: number | null) => (n === null ? '' : String(n))

/** Full-width digits and punctuation, as typed on a Chinese or Japanese keyboard, as ASCII. */
function halfWidth(text: string): string {
  return text
    .replace(/[０-９]/g, c => String.fromCharCode(c.charCodeAt(0) - 0xfee0))
    .replace(/[．。]/g, '.')
    .replace(/[－−]/g, '-')
    .replace(/，/g, ',')
}

type Reject = 'exponent' | 'invalid'

/**
 * Text from the box to a plain number string, or the reason it is refused.
 * Surrounding and inner spaces go, and so do thousands separators when they are
 * where thousands separators go ("1,000", not "1,5": that one is a decimal comma
 * in some languages and is not guessed at). Scientific notation is refused out
 * loud: "1e3" would otherwise be 1000 here and a typo there.
 */
export function normalizeNumberText(raw: string, opts: { integer: boolean; negative: boolean }): { text: string } | { reject: Reject } {
  let text = halfWidth(raw).replace(/\s+/g, '')
  if (/[eE]/.test(text)) return { reject: 'exponent' }
  if (/^-?\d{1,3}(,\d{3})+(\.\d*)?$/.test(text)) text = text.replace(/,/g, '')
  const pattern = opts.integer
    ? (opts.negative ? /^-?\d*$/ : /^\d*$/)
    : (opts.negative ? /^-?\d*\.?\d*$/ : /^\d*\.?\d*$/)
  return pattern.test(text) ? { text } : { reject: 'invalid' }
}

/**
 * A numeric input with a unit suffix. The text being typed ("1.", "-") is kept
 * apart from the number it will become, so a half-typed value is neither lost
 * nor reported as a number; while it is only punctuation the parent hears "empty"
 * (null), never the number that was there before.
 *
 * Input is normalised (full-width digits, spaces, thousands separators) and what
 * cannot be read is refused WITH a message under the box. A value outside min and
 * max is explained under the box, and leaving the box clamps it and says so.
 */
export function NumberField({
  value, onChange, unit, min, max, integer, allowEmpty, emptyLabel, zeroLabel, placeholder,
  'aria-invalid': ariaInvalid, 'aria-describedby': describedBy, ...rest
}: NumberFieldProps) {
  const { t } = useSpacedT()
  const noteId = useId()
  const [draft, setDraft] = useState(fmt(value))
  const [note, setNote] = useState<string | null>(null)

  // A value changed from outside (follow again, reset, load) replaces the text;
  // a value that merely echoes what was typed leaves it alone.
  useEffect(() => {
    setDraft(prev => (prev.trim() !== '' && Number(prev) === value ? prev : fmt(value)))
  }, [value])

  const allowNegative = min === undefined || min < 0

  const handle = (raw: string) => {
    const r = normalizeNumberText(raw, { integer: integer === true, negative: allowNegative })
    if ('reject' in r) {
      setNote(t(r.reject === 'exponent' ? 'ui.form.number_exponent' : 'ui.form.number_invalid'))
      return
    }
    setNote(null)
    setDraft(r.text)
    if (r.text === '') { onChange(null); return }
    // Only punctuation so far ("-", ".", "-."): empty, not the previous number.
    if (!/\d/.test(r.text)) { onChange(null); return }
    onChange(Number(r.text))
  }

  const clampOnBlur = () => {
    if (value === null) return
    const clamped = min !== undefined && value < min ? min : max !== undefined && value > max ? max : value
    if (clamped === value) return
    onChange(clamped)
    setDraft(String(clamped))
    setNote(t('ui.form.number_clamped', { value: clamped }))
  }

  const outOfRange = value !== null && ((min !== undefined && value < min) || (max !== undefined && value > max))
  const rangeText = min !== undefined && max !== undefined
    ? t('ui.form.number_range', { min, max })
    : min !== undefined ? t('ui.form.number_min', { min }) : max !== undefined ? t('ui.form.number_max', { max }) : ''
  const message = note ?? (outOfRange ? rangeText : null)
  const empty = value === null

  return (
    <div className="flex min-w-0 flex-col gap-1">
      <InputGroup>
        <InputGroupInput
          {...rest}
          inputMode={integer ? 'numeric' : 'decimal'}
          autoComplete="off"
          value={draft}
          placeholder={allowEmpty && emptyLabel ? emptyLabel : placeholder}
          aria-invalid={ariaInvalid ?? (outOfRange || note !== null ? true : undefined)}
          aria-describedby={[describedBy, message ? noteId : undefined].filter(Boolean).join(' ') || undefined}
          onChange={e => handle(e.target.value)}
          onBlur={e => { rest.onBlur?.(e); clampOnBlur() }}
          className="tabular-nums"
        />
        <InputGroupAddon align="inline-end">
          {allowEmpty && empty && emptyLabel && <Badge variant="secondary">{emptyLabel}</Badge>}
          {value === 0 && zeroLabel && <Badge variant="outline">{zeroLabel}</Badge>}
          {unit && <InputGroupText>{unit}</InputGroupText>}
          {allowEmpty && !empty && (
            <TipButton label={t('ui.form.number_follow')} size="icon-xs" disabled={rest.disabled} onClick={() => { setNote(null); onChange(null) }}>
              <RotateCcwIcon />
            </TipButton>
          )}
        </InputGroupAddon>
      </InputGroup>
      {message && <p id={noteId} role="status" className="text-xs text-destructive">{message}</p>}
    </div>
  )
}

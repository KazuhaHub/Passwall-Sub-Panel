import { useEffect, useId, useState, type ComponentProps } from 'react'
import { RotateCcwIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from '@/components/ui/input-group'
import { TipButton } from '@/components/data/TipButton'
import { cn } from '@/lib/utils'

export interface NumberFieldProps
  extends Omit<ComponentProps<'input'>, 'value' | 'onChange' | 'type' | 'min' | 'max' | 'step' | 'size'> {
  /** null is an EMPTY box. It is never 0, and a page must not turn one into the other. */
  value: number | null
  /**
   * A number as soon as the text is one; null only for an explicit empty commit
   * (the box emptied AND then left or confirmed with Enter, on a field that
   * allows empty). Never null while the person is still editing.
   */
  onChange: (next: number | null) => void
  /** Suffix shown inside the box: GB, 天, 秒, ms. */
  unit?: string
  min?: number
  max?: number
  /** Whole numbers only. */
  integer?: boolean
  /**
   * Empty is a real answer ("follow the level above"), not a missing one. Shows
   * `emptyLabel` as a badge while empty and a "follow again" button while a
   * number is set, so empty and 0 cannot be confused (audit section 3: the same
   * input meant "follow the group" on create and "unlimited" on edit). Without it,
   * an empty commit goes back to the number that was there.
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
    .replace(/＋/g, '+')
    .replace(/，/g, ',')
}

export type Reading =
  | { kind: 'empty' }
  /** Not a number yet but could become one: "-", ".", "1,". No message, nothing reported. */
  | { kind: 'partial' }
  | { kind: 'number'; value: number }
  | { kind: 'exponent' }
  | { kind: 'invalid' }

/**
 * What the text in the box means. Spaces and a leading plus go, and so do thousands
 * separators where they belong ("1,000"; "1,5" is not guessed at: it is a decimal
 * comma in some languages). Scientific notation is refused out loud, because "1e3"
 * would be 1000 here and a typo there.
 */
export function readNumberText(raw: string, integer: boolean): Reading {
  let text = halfWidth(raw).replace(/\s+/g, '')
  if (text === '') return { kind: 'empty' }
  if (/[eE]/.test(text)) return { kind: 'exponent' }
  text = text.replace(/^\+/, '')
  if (/^[-.]*$/.test(text) || text === '') return /^-?\.?$/.test(text) ? { kind: 'partial' } : { kind: 'invalid' }
  // "1," and "1,00" on the way to "1,000" are not mistakes yet.
  if (/^-?\d{1,3}(,\d{3})*,\d{0,2}$/.test(text)) return { kind: 'partial' }
  if (/^-?\d{1,3}(,\d{3})+(\.\d*)?$/.test(text)) text = text.replace(/,/g, '')
  const pattern = integer ? /^-?\d+$/ : /^-?\d*\.?\d*$/
  if (!pattern.test(text) || !/\d/.test(text)) return { kind: 'invalid' }
  const value = Number(text)
  return Number.isFinite(value) ? { kind: 'number', value } : { kind: 'invalid' }
}

/**
 * A numeric input with a unit suffix, built around one rule: EDITING IS NOT
 * COMMITTING. While the box is being typed in it keeps its own draft, whatever it
 * holds ("", "-", ".", "1,"), tells the parent only about complete numbers, and
 * never reports "cleared" because a Backspace emptied it on the way to retyping.
 * Leaving the box or pressing Enter commits: an empty box is then the explicit
 * "cleared" (on a field that allows empty), a half-typed one goes back to the real
 * value with a message, and a value outside min and max is clamped and says so.
 * Nothing is blocked while typing: unreadable text stays in the box with guidance
 * under it.
 */
export function NumberField({
  value, onChange, unit, min, max, integer, allowEmpty, emptyLabel, zeroLabel, placeholder,
  'aria-invalid': ariaInvalid, 'aria-describedby': describedBy, ...rest
}: NumberFieldProps) {
  const { t } = useTranslation('common')
  const noteId = useId()
  const [draft, setDraft] = useState(fmt(value))
  const [note, setNote] = useState<string | null>(null)

  // A value changed from outside (follow again, reset, load) replaces the text;
  // a value that merely echoes what was typed leaves it alone.
  useEffect(() => {
    setDraft(prev => {
      const r = readNumberText(prev, integer === true)
      return r.kind === 'number' && r.value === value ? prev : fmt(value)
    })
    // `integer` only changes how the draft is read, not whether it matches.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])

  const guidance = (r: Reading): string | null => {
    if (r.kind === 'exponent') return t('ui.form.number_exponent')
    if (r.kind !== 'invalid') return null
    return t(integer ? 'ui.form.number_invalid_int' : 'ui.form.number_invalid')
  }

  const edit = (text: string) => {
    setDraft(text)
    const r = readNumberText(text, integer === true)
    setNote(guidance(r))
    if (r.kind === 'number') onChange(r.value)
  }

  const commit = () => {
    const r = readNumberText(draft, integer === true)
    if (r.kind === 'number') {
      const clamped = min !== undefined && r.value < min ? min : max !== undefined && r.value > max ? max : r.value
      if (clamped !== r.value) {
        onChange(clamped)
        setDraft(String(clamped))
        setNote(t('ui.form.number_clamped', { value: clamped }))
      } else {
        setDraft(String(r.value))
        setNote(null)
      }
      return
    }
    if (r.kind === 'empty' && (allowEmpty || value === null)) {
      setNote(null)
      if (value !== null) onChange(null)
      return
    }
    // Nothing usable: back to the real value, and say so.
    setDraft(fmt(value))
    setNote(value === null ? null : t(r.kind === 'empty' ? 'ui.form.number_required' : 'ui.form.number_reverted', { value }))
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
          aria-invalid={ariaInvalid ?? (outOfRange || (note !== null && !note.includes('changed')) ? true : undefined)}
          aria-describedby={[describedBy, message ? noteId : undefined].filter(Boolean).join(' ') || undefined}
          onChange={e => edit(e.target.value)}
          onBlur={e => { rest.onBlur?.(e); commit() }}
          onKeyDown={e => { rest.onKeyDown?.(e); if (e.key === 'Enter') commit() }}
          className="tabular-nums"
        />
        <InputGroupAddon align="inline-end">
          {allowEmpty && empty && emptyLabel && <Badge variant="secondary">{emptyLabel}</Badge>}
          {value === 0 && zeroLabel && <Badge variant="outline">{zeroLabel}</Badge>}
          {unit && <InputGroupText>{unit}</InputGroupText>}
          {allowEmpty && !empty && (
            <TipButton label={t('ui.form.number_follow')} size="icon-xs" disabled={rest.disabled} onClick={() => { setNote(null); setDraft(''); onChange(null) }}>
              <RotateCcwIcon />
            </TipButton>
          )}
        </InputGroupAddon>
      </InputGroup>
      {/* Always in the page, empty and out of sight until there is something to say:
          a live region that arrives together with its text is skipped by many readers. */}
      <p id={noteId} role="status" className={cn(message ? 'text-xs text-destructive' : 'sr-only')}>{message}</p>
    </div>
  )
}

import { useEffect, useState, type ComponentProps } from 'react'
import { RotateCcwIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
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

/**
 * A numeric input with a unit suffix. Typing is filtered rather than validated
 * after the fact, and the text being typed ("1.", "-") is kept apart from the
 * number it will become, so a half-typed value is neither lost nor reported.
 */
export function NumberField({
  value, onChange, unit, min, max, integer, allowEmpty, emptyLabel, zeroLabel, placeholder, 'aria-invalid': ariaInvalid, ...rest
}: NumberFieldProps) {
  const { t } = useTranslation('common')
  const [draft, setDraft] = useState(fmt(value))

  // A value changed from outside (follow again, reset, load) replaces the text;
  // a value that merely echoes what was typed leaves it alone.
  useEffect(() => {
    setDraft(prev => (prev.trim() !== '' && Number(prev) === value ? prev : fmt(value)))
  }, [value])

  const allowNegative = min === undefined || min < 0
  const pattern = integer
    ? (allowNegative ? /^-?\d*$/ : /^\d*$/)
    : (allowNegative ? /^-?\d*\.?\d*$/ : /^\d*\.?\d*$/)

  const handle = (text: string) => {
    if (!pattern.test(text)) return
    setDraft(text)
    if (text === '') { onChange(null); return }
    const n = Number(text)
    if (Number.isFinite(n) && !Number.isNaN(n) && /\d/.test(text)) onChange(n)
  }

  const outOfRange = value !== null && ((min !== undefined && value < min) || (max !== undefined && value > max))
  const empty = value === null

  return (
    <InputGroup>
      <InputGroupInput
        {...rest}
        inputMode={integer ? 'numeric' : 'decimal'}
        autoComplete="off"
        value={draft}
        placeholder={allowEmpty && emptyLabel ? emptyLabel : placeholder}
        aria-invalid={ariaInvalid ?? (outOfRange ? true : undefined)}
        onChange={e => handle(e.target.value)}
        className="tabular-nums"
      />
      <InputGroupAddon align="inline-end">
        {allowEmpty && empty && emptyLabel && <Badge variant="secondary">{emptyLabel}</Badge>}
        {value === 0 && zeroLabel && <Badge variant="outline">{zeroLabel}</Badge>}
        {unit && <InputGroupText>{unit}</InputGroupText>}
        {allowEmpty && !empty && (
          <TipButton label={t('ui.form.number_follow')} size="icon-xs" disabled={rest.disabled} onClick={() => onChange(null)}>
            <RotateCcwIcon />
          </TipButton>
        )}
      </InputGroupAddon>
    </InputGroup>
  )
}

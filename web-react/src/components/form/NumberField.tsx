import { useCallback, useContext, useEffect, useId, useRef, useState, type ComponentProps, type Ref } from 'react'
import { RotateCcwIcon } from 'lucide-react'
import { useSpacedT } from '@/i18n/spacing'
import { Badge } from '@/components/ui/badge'
import { InputGroup, InputGroupAddon, InputGroupInput, InputGroupText } from '@/components/ui/input-group'
import { IconButton } from '@/components/common/IconButton'
import { cn } from '@/lib/utils'
import { useFieldMessage } from './Field'
import { InheritEditorContext } from './editorContext'
import { useReportValidity } from './FormValidity'

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
 * A numeric input with a unit suffix, built around two rules.
 *
 * EDITING IS NOT COMMITTING. While the box is being typed in it keeps its own draft,
 * whatever it holds ("", "-", ".", "1,"), tells the parent only about complete
 * numbers, and never reports "cleared" because a Backspace emptied it on the way to
 * retyping. Leaving the box or pressing Enter commits; Escape puts the box back to the
 * current value.
 *
 * THE BOX NEVER LIES ABOUT WHAT WILL BE SAVED. Nothing is changed behind the person's
 * back: no clamping, no silent revert of what they typed. A draft that is not a usable
 * value (unreadable, unfinished, empty where a number is required, outside min and max)
 * is INVALID: it keeps the text, says why on the line under the box, sets aria-invalid
 * from that state, and reports itself to the form (useReportValidity) so a SaveBar
 * switches off with the reason instead of storing something else. The one thing that
 * is put back is a value the parent REFUSED: after a commit the box shows the value the
 * parent holds, with a note.
 *
 * Inside an InheritanceField an empty box is invalid-until-filled and the field offers
 * no way of its own back to "follow": that is the mode control's job.
 */
export function NumberField({
  value, onChange, unit, min, max, integer, allowEmpty, emptyLabel, zeroLabel, placeholder,
  'aria-invalid': ariaInvalid, 'aria-describedby': describedBy, ...rest
}: NumberFieldProps) {
  const { t } = useSpacedT()
  const inheritEditor = useContext(InheritEditorContext)
  const emptyOk = allowEmpty === true && !inheritEditor
  const noteId = useId()
  const [draft, setDraft] = useState(fmt(value))
  const [touched, setTouched] = useState(false)
  const [rejected, setRejected] = useState<string | null>(null)
  const afterCommit = useRef<{ expected: number | null } | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)
  // The last value that was settled on: what the box held when it arrived, or what the person
  // last committed (blur / Enter) while it was in range. Escape goes back to THIS, not to
  // "whatever the parent holds now", because the parent is told every complete number as it is
  // typed (an out-of-range "9999" is held by the parent too until it is fixed or abandoned).
  const committed = useRef<number | null>(value)
  // The caller may pass a ref of its own; both get the element.
  const outerRef = (rest as { ref?: Ref<HTMLInputElement> }).ref
  const setInput = useCallback((node: HTMLInputElement | null) => {
    inputRef.current = node
    if (typeof outerRef === 'function') outerRef(node)
    else if (outerRef) (outerRef as { current: HTMLInputElement | null }).current = node
  }, [outerRef])
  const draftNow = useRef(draft)
  draftNow.current = draft
  // Bumped by a commit so the check below runs once the parent's answer has rendered, even if its value did not change.
  const [commitTick, setCommitTick] = useState(0)

  // A value changed from outside (follow again, reset, load) replaces the text;
  // a value that merely echoes what was typed leaves it alone.
  useEffect(() => {
    const r = readNumberText(draftNow.current, integer === true)
    if (r.kind === 'number' && r.value === value) return
    committed.current = value
    setDraft(fmt(value))
    // `integer` only changes how the draft is read, not whether it matches.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value])

  // After a commit the parent has had its chance. If it kept something else, the box
  // must show what it holds, and say so: the person would otherwise see 7 and save 5.
  useEffect(() => {
    const check = afterCommit.current
    if (!check) return
    afterCommit.current = null
    if (check.expected !== value) {
      committed.current = value
      setDraft(fmt(value))
      setRejected(t('ui.form.number_rejected', { value: value === null ? '—' : value }))
    }
    // `t` is stable for a language; the tick and the value are what decide when to look.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [commitTick, value])

  const reading = readNumberText(draft, integer === true)
  const out = reading.kind === 'number' && ((min !== undefined && reading.value < min) || (max !== undefined && reading.value > max))
  const rangeText = min !== undefined && max !== undefined
    ? t('ui.form.number_range', { min, max })
    : min !== undefined ? t('ui.form.number_min', { min }) : max !== undefined ? t('ui.form.number_max', { max }) : ''

  // The state, not its wording: what is wrong, whether to say it already, and the text.
  let problem: { message: string; showNow: boolean } | null = null
  // An example the field would itself accept: inside the bounds, so "such as 1000" never
  // appears on a field that ends at 600.
  const lo = min !== undefined ? (integer ? Math.ceil(min) : min) : undefined
  const hi = max !== undefined ? (integer ? Math.floor(max) : max) : undefined
  const fits = (n: number) => (lo === undefined || n >= lo) && (hi === undefined || n <= hi)
  const settled = committed.current
  // The value the box holds when it is usable (what the person knows), else a round number, else a bound.
  const example = settled !== null && fits(settled) ? settled
    : integer ? (fits(1000) ? 1000 : lo ?? hi ?? 1000)
      : fits(1.5) ? 1.5 : lo !== undefined && fits(lo + 0.5) ? lo + 0.5 : lo ?? hi ?? 1.5
  if (reading.kind === 'exponent') problem = { message: t('ui.form.number_exponent', { example }), showNow: true }
  else if (reading.kind === 'invalid') problem = { message: t(integer ? 'ui.form.number_invalid_int' : 'ui.form.number_invalid', { example }), showNow: true }
  else if (reading.kind === 'partial') problem = { message: t('ui.form.number_incomplete', { example }), showNow: touched }
  else if (reading.kind === 'empty' && !emptyOk) {
    // Says what to enter, not only that something is missing.
    const needs = inheritEditor ? 'ui.form.number_required_inherit'
      : min !== undefined && max !== undefined ? 'ui.form.number_required_range' : 'ui.form.number_required'
    problem = { message: t(needs, { min, max, example }), showNow: true }
  }
  else if (out) problem = { message: rangeText, showNow: true }
  // An empty box on a field that allows empty is a complete answer, not a problem: the empty
  // commit (blur or Enter) is what tells the parent, and both come before any click on Save.

  // A disabled box cannot be fixed, so it must not hold the form hostage.
  useReportValidity(rest.disabled ? null : problem?.message ?? null, inputRef)
  const message = (problem?.showNow ? problem.message : null) ?? rejected
  const inField = useFieldMessage(message)

  const edit = (text: string) => {
    setDraft(text)
    setRejected(null)
    const r = readNumberText(text, integer === true)
    if (r.kind === 'number') onChange(r.value)
  }

  const commit = () => {
    setTouched(true)
    const r = readNumberText(draft, integer === true)
    if (r.kind === 'number') {
      setDraft(String(r.value))
      if (!out) committed.current = r.value
      if (r.value !== value) { onChange(r.value) }
      // Even when the parent already holds it: a refusal during typing shows up here.
      afterCommit.current = { expected: r.value }
      setCommitTick(n => n + 1)
      return
    }
    if (r.kind === 'empty' && emptyOk) {
      committed.current = null
      if (value !== null) onChange(null)
      afterCommit.current = { expected: null }
      setCommitTick(n => n + 1)
      return
    }
    // Unreadable, unfinished or empty-but-required: nothing is decided for the person.
    // The text stays, the problem stays visible, and the form cannot be saved.
  }

  // A field that is switched off cannot be corrected, and a form must not save something other
  // than what its boxes show: so a draft that is not usable goes back to the committed value now
  // (and the parent is told, which is what holds the value that will be saved).
  const revert = () => {
    const back = committed.current
    setDraft(fmt(back))
    setRejected(null)
    setTouched(false)
    // The parent was told the abandoned number as it was typed; tell it the settled one.
    // Never null on a field that does not allow empty: that event means "cleared on purpose".
    if (value !== back && (back !== null || emptyOk) && !(inheritEditor && inheritEditor.revert(back))) onChange(back)
  }

  useEffect(() => {
    if (rest.disabled && problem !== null) revert()
    // Only the moment the box becomes disabled matters; `revert` and `problem` are read as they are then.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rest.disabled])

  return (
    <div className="flex min-w-0 flex-col gap-1">
      <InputGroup>
        <InputGroupInput
          {...rest}
          ref={setInput}
          inputMode={integer ? 'numeric' : 'decimal'}
          autoComplete="off"
          value={draft}
          placeholder={emptyOk && emptyLabel ? emptyLabel : placeholder}
          aria-invalid={ariaInvalid ?? (problem !== null ? true : undefined)}
          aria-describedby={[describedBy, !inField && message ? noteId : undefined].filter(Boolean).join(' ') || undefined}
          onChange={e => edit(e.target.value)}
          onBlur={e => { rest.onBlur?.(e); commit() }}
          onKeyDown={e => {
            rest.onKeyDown?.(e)
            if (e.key === 'Enter') commit()
            else if (e.key === 'Escape' && (draft !== fmt(committed.current) || value !== committed.current)) { e.stopPropagation(); revert() }
          }}
          className="tabular-nums"
        />
        <InputGroupAddon align="inline-end">
          {emptyOk && value === null && emptyLabel && <Badge variant="secondary">{emptyLabel}</Badge>}
          {value === 0 && zeroLabel && <Badge variant="outline">{zeroLabel}</Badge>}
          {unit && <InputGroupText>{unit}</InputGroupText>}
          {emptyOk && value !== null && (
            <IconButton label={t('ui.form.number_follow')} size="icon-xs" side="top" disabled={rest.disabled} onClick={() => { setRejected(null); setDraft(''); onChange(null) }}>
              <RotateCcwIcon />
            </IconButton>
          )}
        </InputGroupAddon>
      </InputGroup>
      {/* Inside a Field the message goes to the line the Field reserves for it. On its own the
          field keeps one itself, always in the page and the height of one line, so a message
          arriving never moves anything below it. */}
      {!inField && (
        <p id={noteId} role="status" className={cn('min-h-4 text-xs text-destructive', !message && 'text-transparent')}>{message}</p>
      )}
    </div>
  )
}

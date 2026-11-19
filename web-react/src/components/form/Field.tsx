import { cloneElement, createContext, isValidElement, useCallback, useContext, useEffect, useId, useMemo, useState, type ReactElement, type ReactNode } from 'react'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'
import { useReportValidity } from './FormValidity'

interface FieldMessageApi { set: (message: string | null) => void }
const FieldMessageContext = createContext<FieldMessageApi | null>(null)

/**
 * For a control that explains its own state (NumberField): hands its message to the
 * Field it sits in, which shows it on the line under the control that is reserved for
 * messages. Returns whether there was a Field to take it; a control used on its own
 * renders the message itself.
 */
export function useFieldMessage(message: string | null): boolean {
  const field = useContext(FieldMessageContext)
  const set = field?.set
  useEffect(() => {
    if (!set) return
    set(message)
    return () => set(null)
  }, [set, message])
  return field !== null
}

/** What a control needs to be wired to its Field; a render-function child receives it. */
export interface FieldControlProps {
  id: string
  /** The label's id, for group controls (radio groups, tag boxes) that cannot use `for`. */
  labelId: string
  'aria-describedby'?: string
  'aria-invalid'?: true
  'aria-required'?: true
}

export interface FieldProps {
  label: ReactNode
  /** Help below the control. Replaced by `error` while there is one. */
  hint?: ReactNode
  /** How to fix it, not just what is wrong (copy rule 5). Shown below the control, instead of the hint. */
  error?: ReactNode
  required?: boolean
  /** `horizontal` puts the control beside the label, for switches and checkboxes. */
  orientation?: 'vertical' | 'horizontal'
  className?: string
  children: ReactElement | ((p: FieldControlProps) => ReactNode)
}

/**
 * Label above the control, hint or error below (plan 3.3: no floating labels).
 * One place wires `for`, `aria-describedby` and `aria-invalid`, so a page
 * cannot forget one. The error REPLACES the hint rather than stacking under it:
 * two grey lines under a red border is where the eye stops reading.
 *
 * Children are either one element (it receives the props by cloning, keeping
 * any id it already has) or a function for controls that are not a single
 * element, such as TagsField or a group of radios.
 */
export function Field({ label, hint, error, required, orientation = 'vertical', className, children }: FieldProps) {
  const auto = useId()
  const own = isValidElement(children) ? (children.props as { id?: string }).id : undefined
  const id = own ?? `${auto}-control`
  const labelId = `${auto}-label`
  const hintId = `${auto}-hint`
  const errorId = `${auto}-error`
  const [inner, setInner] = useState<string | null>(null)
  const shown = error ?? inner
  // The page's own error is a problem the form cannot be saved with, in the place where the
  // control sits, so the SaveBar can name this field and take the person to it. (`inner` is a
  // control's own message: it reports for itself.)
  const findControl = useCallback(() => document.getElementById(id), [id])
  useReportValidity(typeof error === 'string' ? error : null, findControl)
  const describedBy = shown ? errorId : hint ? hintId : undefined
  const messageApi = useMemo<FieldMessageApi>(() => ({ set: setInner }), [])

  const wiring: FieldControlProps = {
    id,
    labelId,
    'aria-describedby': describedBy,
    'aria-invalid': shown ? true : undefined,
    'aria-required': required ? true : undefined,
  }
  const control = typeof children === 'function'
    ? children(wiring)
    : cloneElement(children, {
        id,
        'aria-describedby': describedBy,
        'aria-invalid': shown ? true : undefined,
        'aria-required': required ? true : undefined,
      } as Record<string, unknown>)

  const labelEl = (
    <Label id={labelId} htmlFor={id} className="gap-1 leading-snug">
      {label}
      {required && <span aria-hidden="true" className="ml-0.5 text-destructive">*</span>}
    </Label>
  )
  // The live region is ALWAYS mounted and only its text changes: a region that
  // appears together with its text is skipped by many screen readers, one whose
  // text changes is announced.
  // The line under the control is RESERVED (min-h-4, one text-xs line): an error that
  // appears on blur must not push the controls below it, or the Save button, down by a
  // line between a mouse press and its release.
  const note = (
    <div className="min-h-4">
      <p id={errorId} aria-live="polite" className={cn('text-xs text-destructive', !shown && 'sr-only')}>{shown}</p>
      {!shown && hint && <p id={hintId} className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )

  const wrap = (node: ReactElement) => <FieldMessageContext.Provider value={messageApi}>{node}</FieldMessageContext.Provider>
  if (orientation === 'horizontal') {
    return wrap(
      <div data-slot="field" className={cn('flex items-start justify-between gap-4', className)}>
        <div className="flex min-w-0 flex-col gap-1">{labelEl}{note}</div>
        <div className="shrink-0 pt-0.5">{control}</div>
      </div>
    )
  }
  return wrap(
    <div data-slot="field" className={cn('flex min-w-0 flex-col gap-1.5', className)}>
      {labelEl}
      {control}
      {note}
    </div>
  )
}

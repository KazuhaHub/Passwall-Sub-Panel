import { cloneElement, isValidElement, useId, type ReactElement, type ReactNode } from 'react'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

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
  const describedBy = error ? errorId : hint ? hintId : undefined

  const wiring: FieldControlProps = {
    id,
    labelId,
    'aria-describedby': describedBy,
    'aria-invalid': error ? true : undefined,
    'aria-required': required ? true : undefined,
  }
  const control = typeof children === 'function'
    ? children(wiring)
    : cloneElement(children, {
        id,
        'aria-describedby': describedBy,
        'aria-invalid': error ? true : undefined,
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
  const note = (
    <>
      <p id={errorId} aria-live="polite" className={cn('text-xs text-destructive', !error && 'hidden')}>{error}</p>
      {!error && hint && <p id={hintId} className="text-xs text-muted-foreground">{hint}</p>}
    </>
  )

  if (orientation === 'horizontal') {
    return (
      <div data-slot="field" className={cn('flex items-start justify-between gap-4', className)}>
        <div className="flex min-w-0 flex-col gap-1">{labelEl}{note}</div>
        <div className="shrink-0 pt-0.5">{control}</div>
      </div>
    )
  }
  return (
    <div data-slot="field" className={cn('flex min-w-0 flex-col gap-1.5', className)}>
      {labelEl}
      {control}
      {note}
    </div>
  )
}

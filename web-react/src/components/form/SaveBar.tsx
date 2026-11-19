import { useId, type ReactNode } from 'react'
import { Loader2Icon } from 'lucide-react'
import { useSpacedT } from '@/i18n/spacing'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { useFormValidity } from './FormValidity'

/** A problem the page found itself (a rule on its own draft, a server refusal). */
export interface SaveBarError {
  /** The field's name; without it the problem is counted but cannot be named. */
  label?: string
  message: string
  /** Moves focus to the field. Without it the message is not a link to anything. */
  focus?: () => void
}

export interface SaveBarProps {
  dirty: boolean
  /** A save is in flight: both buttons lock and the label says so. */
  pending?: boolean
  /** The form cannot be saved yet (invalid); `message` says what to fix. */
  disabled?: boolean
  /** Replaces "有未保存的修改" when there is something more useful to say, such as how many fields to fix. */
  message?: ReactNode
  /**
   * The page's own problems. They are counted and named TOGETHER with the ones the
   * fields report to a FormValidityProvider, so neither hides the other. Fields come
   * first in the list and are the first to receive focus; page errors follow in the
   * order given. (`disabled` with a `message` still works and counts as one problem.)
   */
  errors?: readonly SaveBarError[]
  saveLabel?: string
  onSave: () => void
  onDiscard: () => void
  className?: string
}

/**
 * The sticky bar at the bottom of a long form. It appears only when there is
 * something to save, so a clean form has nothing asking for attention, and it
 * stays up while a save is in flight even if the draft already matches. One bar
 * serves one section or tab: each saves on its own (audit section 0).
 *
 * While saving is blocked the message names what to fix (the label of each field, or
 * "label: what is wrong" when there is one) and is a button: activating it, or pressing
 * Save, moves focus to the first problem, opening the advanced section it is in if
 * that is collapsed. It is a real button so the keyboard reaches it, and the text is
 * also spoken through the status region below.
 */
export function SaveBar({ dirty, pending, disabled, message, errors, saveLabel, onSave, onDiscard, className }: SaveBarProps) {
  const { t } = useSpacedT()
  const visible = dirty || pending
  const { issues, focusFirst } = useFormValidity()
  const pageErrors = errors ?? []
  const entries = [...issues, ...pageErrors]
  // `disabled` with a message is one more problem, not a replacement for the others.
  const legacy = disabled === true && message != null
  const count = entries.length + (legacy ? 1 : 0)
  const blocked = disabled === true || entries.length > 0
  const reasonId = useId()
  const names = entries.map(e => e.label).filter((l): l is string => !!l)
  const reason = entries.length === 0 ? null
    : count === 1 ? (entries[0].label ? t('ui.form.invalid_field', { label: entries[0].label, message: entries[0].message }) : entries[0].message)
      : names.length === count ? t('ui.form.invalid_named', { count, names: names.join(t('ui.form.name_sep')) })
        : t('ui.form.invalid_count', { count })
  const canGo = issues.length > 0 || pageErrors.some(e => e.focus)
  const goToFirst = () => { if (!focusFirst()) pageErrors.find(e => e.focus)?.focus?.() }
  const spoken = pending ? t('ui.form.saving') : dirty ? (reason ?? t('ui.form.unsaved')) : ''
  return (
    <>
      {/* Always in the page: a live region announces a change in its text, not its own arrival. */}
      <p role="status" className="sr-only">{spoken}</p>
      {visible && (
    <div
      role="region"
      aria-label={t('ui.form.unsaved')}
      data-slot="save-bar"
      className={cn(
        'sticky bottom-0 z-20 -mx-1 flex min-h-14 flex-wrap items-center gap-x-4 gap-y-2 rounded-t-xl border-t bg-popover/95 px-4 py-2.5 shadow-[0_-4px_12px_-6px_rgb(0_0_0/0.2)] backdrop-blur supports-backdrop-filter:bg-popover/80',
        className,
      )}
    >
      <p id={reasonId} className={cn('min-h-5 min-w-0 flex-1 text-sm font-medium', reason && 'text-destructive')}>
        {reason && canGo ? (
          <button
            type="button"
            onClick={goToFirst}
            title={t('ui.form.invalid_goto')}
            className="-mx-1 rounded-md px-1 text-left underline decoration-dotted underline-offset-4 outline-none hover:decoration-solid focus-visible:ring-3 focus-visible:ring-ring/50"
          >{reason}</button>
        ) : (reason ?? message ?? t('ui.form.unsaved'))}
      </p>
      <div className="flex items-center gap-2">
        <Button variant="ghost" onClick={onDiscard} disabled={pending}>{t('ui.form.discard')}</Button>
        {/* aria-disabled, not disabled: a click on it does nothing, but it stays focusable, and
            the reason beside it is what describes it. */}
        <Button
          onClick={() => { if (!blocked) onSave(); else if (canGo) goToFirst() }}
          disabled={pending}
          aria-disabled={blocked || undefined}
          aria-describedby={blocked ? reasonId : undefined}
          aria-busy={pending ? true : undefined}
          className={cn(blocked && 'cursor-not-allowed opacity-50')}
        >
          {pending && <Loader2Icon className="animate-spin" aria-hidden="true" />}
          {pending ? t('ui.form.saving') : (saveLabel ?? t('actions.save'))}
        </Button>
      </div>
    </div>
      )}
    </>
  )
}

import { useId, type ReactNode } from 'react'
import { Loader2Icon } from 'lucide-react'
import { useSpacedT } from '@/i18n/spacing'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { useFormValidity } from './FormValidity'

export interface SaveBarProps {
  dirty: boolean
  /** A save is in flight: both buttons lock and the label says so. */
  pending?: boolean
  /** The form cannot be saved yet (invalid); `message` says what to fix. */
  disabled?: boolean
  /** Replaces "有未保存的修改" when there is something more useful to say, such as how many fields to fix. */
  message?: ReactNode
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
 */
export function SaveBar({ dirty, pending, disabled, message, saveLabel, onSave, onDiscard, className }: SaveBarProps) {
  const { t } = useSpacedT()
  const visible = dirty || pending
  const { issues } = useFormValidity()
  const blocked = disabled === true || issues.length > 0
  const reasonId = useId()
  const reason = issues.length === 1 ? issues[0].message : issues.length > 1 ? t('ui.form.invalid_count', { count: issues.length }) : null
  const spoken = pending ? t('ui.form.saving') : dirty ? t('ui.form.unsaved') : ''
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
      <p id={reasonId} className={cn('min-h-5 min-w-0 flex-1 text-sm font-medium', reason && 'text-destructive')}>{reason ?? message ?? t('ui.form.unsaved')}</p>
      <div className="flex items-center gap-2">
        <Button variant="ghost" onClick={onDiscard} disabled={pending}>{t('ui.form.discard')}</Button>
        {/* aria-disabled, not disabled: a click on it does nothing, but it stays focusable, and
            the reason beside it is what describes it. */}
        <Button
          onClick={() => { if (!blocked) onSave() }}
          disabled={pending}
          aria-disabled={blocked || undefined}
          aria-describedby={blocked ? reasonId : undefined}
          aria-busy={pending ? true : undefined}
          className={cn(blocked && 'cursor-not-allowed opacity-50')}
        >
          {pending && <Loader2Icon className="animate-spin" aria-hidden="true" />}
          {pending ? t('ui.form.saving') : (saveLabel ?? t('ui.form.save'))}
        </Button>
      </div>
    </div>
      )}
    </>
  )
}

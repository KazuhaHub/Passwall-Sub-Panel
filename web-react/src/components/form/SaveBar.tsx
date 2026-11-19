import type { ReactNode } from 'react'
import { Loader2Icon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

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
  const { t } = useTranslation('common')
  if (!dirty && !pending) return null
  return (
    <div
      role="region"
      aria-label={t('ui.form.unsaved')}
      data-slot="save-bar"
      className={cn(
        'sticky bottom-0 z-20 -mx-1 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-t-xl border-t bg-popover/95 px-4 py-2.5 shadow-[0_-4px_12px_-6px_rgb(0_0_0/0.2)] backdrop-blur supports-backdrop-filter:bg-popover/80',
        className,
      )}
    >
      <p className="min-w-0 flex-1 text-sm font-medium">{message ?? t('ui.form.unsaved')}</p>
      <div className="flex items-center gap-2">
        <Button variant="ghost" onClick={onDiscard} disabled={pending}>{t('ui.form.discard')}</Button>
        <Button onClick={onSave} disabled={pending || disabled} aria-busy={pending ? true : undefined}>
          {pending && <Loader2Icon className="animate-spin" aria-hidden="true" />}
          {pending ? t('ui.form.saving') : (saveLabel ?? t('ui.form.save'))}
        </Button>
      </div>
    </div>
  )
}

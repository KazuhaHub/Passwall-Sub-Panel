import type { ReactNode, RefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { IconWarning } from '@/lib/icons'
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogMedia, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { useInertMuiModals } from '@/lib/inertModals'
import { cn } from '@/lib/utils'

// Above every MUI layer the page may still have open (ConfirmHost uses the same level, for the same reason) and
// below the toasts, so the answer to a delete is never hidden by the dialog that asked.
const ABOVE_EVERY_MODAL = 'z-[1600]'

/**
 * The shell of the deletion dialogs: a title, a scrolling body and a footer that always starts with Cancel, and
 * focus that opens ON Cancel. A delete takes several lines at once, and a stray Enter typed while the dialog was
 * still coming up must not be able to confirm it, so the confirm button is never the focused one. (ConfirmHost
 * offers the same through `initialFocus: 'cancel'`, but its body is a string and these dialogs draw paths.)
 */
export function ImpactDialogFrame({ title, description, cancelRef, cancelLabel, cancelDisabled, onClose, destructive = true, wide = false, children, actions }: {
  title: string
  description?: ReactNode
  cancelRef: RefObject<HTMLButtonElement | null>
  /** "Cancel", or "Close" when the dialog has nothing to confirm. */
  cancelLabel?: string
  /** While a request is in flight there is nothing to cancel. */
  cancelDisabled?: boolean
  onClose: () => void
  destructive?: boolean
  wide?: boolean
  children?: ReactNode
  /** The buttons after Cancel. */
  actions?: ReactNode
}) {
  const { t } = useTranslation('common')
  // The nodes page still hosts MUI editors on this branch; they are made inert while a dialog sits above them.
  useInertMuiModals(true)
  return (
    <AlertDialog open onOpenChange={open => { if (!open) onClose() }}>
      <AlertDialogContent
        initialFocus={cancelRef}
        overlay={{ className: ABOVE_EVERY_MODAL }}
        className={cn(ABOVE_EVERY_MODAL, wide ? 'data-[size=default]:sm:max-w-2xl' : 'data-[size=default]:sm:max-w-xl')}
      >
        <AlertDialogHeader>
          {destructive && (
            <AlertDialogMedia className="bg-destructive/10 text-destructive"><IconWarning aria-hidden /></AlertDialogMedia>
          )}
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description && <AlertDialogDescription render={<div />}>{description}</AlertDialogDescription>}
        </AlertDialogHeader>
        {children && <div className="-mx-4 flex max-h-[min(55dvh,28rem)] flex-col gap-3 overflow-y-auto px-4 text-sm">{children}</div>}
        <AlertDialogFooter>
          <AlertDialogCancel ref={cancelRef} disabled={cancelDisabled}>{cancelLabel ?? t('actions.cancel')}</AlertDialogCancel>
          {actions}
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

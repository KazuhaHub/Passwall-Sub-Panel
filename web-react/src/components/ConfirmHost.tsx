import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { IconWarning } from '@/lib/icons'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { cn } from '@/lib/utils'

export interface ConfirmOpts {
  title: string
  message: string
  confirmText?: string
  cancelText?: string
  destructive?: boolean
  /**
   * Which button takes focus when the dialog opens. Default 'confirm', so Enter answers
   * yes. Use 'cancel' for an irreversible action (a delete): focus then starts on Cancel
   * and a stray Enter, typed while the dialog was still coming up, cannot confirm it.
   */
  initialFocus?: 'confirm' | 'cancel'
}

interface InternalState {
  open: boolean
  opts: ConfirmOpts | null
}

let resolver: ((v: boolean) => void) | null = null
let setStateExternal: ((s: InternalState) => void) | null = null

// Promise-style confirmation. Resolves true when user confirms,
// false when they cancel or close the dialog.
export function confirm(opts: ConfirmOpts): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
    if (!setStateExternal) {
      console.warn('ConfirmHost not mounted')
      resolve(false)
      return
    }
    // A confirm raised while another is open replaces it; the replaced caller is told
    // "no" rather than left awaiting a promise nothing will ever settle.
    resolver?.(false)
    resolver = resolve
    setStateExternal({ open: true, opts })
  })
}

// Where the confirm sits in the stack. It must be ABOVE every layer a page can have open
// under it: dialogs, sheets and the dialogs a sheet raises over itself, popovers and
// menus, and tooltips, because a tooltip left open on the button that raised the confirm
// would otherwise paint over it. It must stay BELOW the toasts
// (components/feedback/ToastHost.tsx, z-[1700]), so a toast about the answer is never
// hidden by the dialog. A route link in a sheet can trip the policy page's leave guard,
// whose confirm would otherwise open beneath the sheet's backdrop where no click could
// reach it. The shadcn primitives sit at z-50, so the level is set explicitly.
const ABOVE_EVERY_MODAL = 'z-[1600]'

export default function ConfirmHost() {
  const { t } = useTranslation('common')
  const [state, setState] = useState<InternalState>({ open: false, opts: null })
  const confirmRef = useRef<HTMLButtonElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    setStateExternal = setState
    return () => { setStateExternal = null }
  }, [])

  function close(answer: boolean) {
    // opts stay while the exit animation runs so the text does not vanish first.
    setState(s => ({ open: false, opts: s.opts }))
    resolver?.(answer)
    resolver = null
  }

  const opts = state.opts
  const destructive = opts?.destructive
  return (
    <AlertDialog open={state.open} onOpenChange={open => { if (!open) close(false) }}>
      <AlertDialogContent
        // The backdrop cancels, as the previous dialog's did; Base UI's alert
        // dialog otherwise ignores outside clicks.
        overlay={{ className: ABOVE_EVERY_MODAL, onClick: () => close(false) }}
        className={cn(ABOVE_EVERY_MODAL, 'data-[size=default]:max-w-md')}
        // The confirm button takes focus by default: Enter answers yes, Esc no. A
        // destructive caller can ask for Cancel instead (see ConfirmOpts.initialFocus).
        initialFocus={opts?.initialFocus === 'cancel' ? cancelRef : confirmRef}
      >
        <AlertDialogHeader>
          {destructive && (
            <AlertDialogMedia className="bg-destructive/10 text-destructive">
              <IconWarning aria-hidden />
            </AlertDialogMedia>
          )}
          <AlertDialogTitle>{opts?.title ?? ''}</AlertDialogTitle>
          <AlertDialogDescription className="whitespace-pre-line">{opts?.message ?? ''}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel ref={cancelRef}>{opts?.cancelText ?? t('actions.cancel')}</AlertDialogCancel>
          <AlertDialogAction
            ref={confirmRef}
            onClick={() => close(true)}
            className={cn(destructive && 'bg-destructive text-white hover:bg-destructive/90 focus-visible:ring-destructive/40 dark:text-background')}
          >
            {opts?.confirmText ?? t('actions.ok')}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

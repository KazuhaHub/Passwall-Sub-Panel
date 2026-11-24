import { useState, type FormEvent, type ReactNode, type RefObject } from 'react'
import { Dialog as DialogPrimitive } from '@base-ui/react/dialog'
import { ChevronDownIcon, ChevronUpIcon, XIcon } from 'lucide-react'
import { useSpacedT } from '@/i18n/spacing'
import { useIsMobile } from '@/hooks/use-mobile'
import { Button } from '@/components/ui/button'
import { DialogFooterSlot } from '@/components/ui/dialog-footer-slot'
import { Dialog, DialogDescription, DialogOverlay, DialogPortal, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { TOUCH_TARGET } from '@/components/common/touchTarget'

export type DetailDialogSize = 'sm' | 'md' | 'lg' | 'xl'

// Widths on a desktop. sm: a confirm-sized form; md: the default; lg: a form with a table or two columns;
// xl: wide tables and charts. Past the viewport the dialog is 1rem short of each edge.
const WIDTH: Record<DetailDialogSize, string> = {
  sm: 'md:max-w-[28rem]',
  md: 'md:max-w-[36rem]',
  lg: 'md:max-w-[48rem]',
  xl: 'md:max-w-[64rem]',
}

/**
 * Previous / next for a dialog that shows one record of a list: the rows the list has loaded, in the
 * order it shows them. A missing handler means "at that end" and disables the button.
 * No "3 of 20" is shown: a list that is paged by the server only knows the rows of this page, and a count of those
 * would read as the number of records.
 */
export interface DetailNav {
  onPrev?: () => void
  onNext?: () => void
}

export interface DetailDialogProps {
  open: boolean
  /** Called for Esc, the close button and a click outside. Wire it to useUrlSelection().clear so Back and Esc agree. */
  onClose: () => void
  title: ReactNode
  /** A status dot or badge beside the title. */
  status?: ReactNode
  description?: ReactNode
  /** Buttons under the title (open the full page, edit, more). At most one primary. */
  actions?: ReactNode
  /** Fixed under the body: the Save bar or the buttons that finish the task. */
  footer?: ReactNode
  children?: ReactNode
  /** See the width table above. Default md. */
  size?: DetailDialogSize
  nav?: DetailNav
  /** The element to focus on open (a Cancel button for a destructive question). Default: the first focusable. */
  initialFocus?: RefObject<HTMLElement | null>
  /** Makes the body and footer one `<form>`: Enter submits and a submit button in the footer works. */
  onSubmit?: (e: FormEvent<HTMLFormElement>) => void
  className?: string
}

/**
 * The container for everything that opens from a row and is not a page: a centred dialog on a desktop and a bottom
 * sheet on a phone (under 768px). Title, status, actions, the previous / next buttons and the close button are fixed
 * on top, the body scrolls on its own (at most 85vh, 92dvh on a phone) and the footer stays in view.
 *
 * Drive `open` from useUrlSelection, so the opened record is a link, Back closes it and Esc and Back do the
 * same thing. Focus is trapped while it is open and returns to what opened it, and a dialog opened from this
 * one stacks above it with its own trap: Esc closes only the top one (Base UI's dialog does all of that).
 *
 * It replaces the right-hand DetailSheet. Anything with tabs or more than a screen of content is a page instead.
 */
export function DetailDialog({ open, onClose, title, status, description, actions, footer, children, size = 'md', nav, initialFocus, onSubmit, className }: DetailDialogProps) {
  const { t } = useSpacedT()
  const phone = useIsMobile()
  const [slot, setSlot] = useState<HTMLElement | null>(null)

  const body = <div data-slot="detail-dialog-body" className="min-h-0 flex-1 overflow-y-auto p-4">{children}</div>
  // Always mounted (hidden while empty) so a SaveBar can portal into it. The bottom padding keeps the buttons above a phone's home indicator.
  const foot = (
    <div ref={setSlot} data-slot="detail-dialog-footer" className="flex flex-shrink-0 flex-row flex-wrap items-center justify-end gap-2 border-t bg-muted/40 p-4 pb-[max(1rem,env(safe-area-inset-bottom))] empty:hidden">{footer}</div>
  )

  return (
    <Dialog open={open} onOpenChange={next => { if (!next) onClose() }}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Popup
          data-slot="detail-dialog"
          data-layout={phone ? 'sheet' : 'dialog'}
          data-size={size}
          initialFocus={initialFocus}
          className={cn(
            'fixed z-50 flex flex-col overflow-hidden bg-popover text-sm text-popover-foreground outline-none ring-1 ring-foreground/10',
            phone
              // A bottom sheet: it slides up from the edge and is never taller than the visible viewport.
              ? 'inset-x-0 bottom-0 max-h-[92dvh] w-full rounded-t-xl transition-transform duration-200 ease-out data-ending-style:translate-y-full data-starting-style:translate-y-full data-closed:duration-[140ms]'
              : cn('top-1/2 left-1/2 max-h-[85vh] w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-xl shadow-lg data-open:duration-200 data-closed:duration-[120ms] data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95', WIDTH[size]),
            // Under a dialog opened from this one: stay put, but step back.
            'data-nested-dialog-open:brightness-95',
            className,
          )}
        >
          {phone && <div aria-hidden className="mx-auto mt-2 h-1 w-10 flex-shrink-0 rounded-full bg-muted-foreground/30" />}
          <header className="flex flex-shrink-0 items-start gap-2 border-b p-4">
            <div className="flex min-w-0 flex-1 flex-col gap-2">
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                <DialogTitle className="min-w-0 truncate text-base leading-snug">{title}</DialogTitle>
                {status}
              </div>
              {description && <DialogDescription>{description}</DialogDescription>}
              {actions && <div className="flex flex-wrap items-center gap-2 pt-1">{actions}</div>}
            </div>
            <div className="flex flex-shrink-0 items-center gap-0.5">
              {nav && (
                <>
                  <Button type="button" variant="ghost" size="icon-sm" className={TOUCH_TARGET} disabled={!nav.onPrev} onClick={nav.onPrev} aria-label={t('ui.data.detail_prev')} title={t('ui.data.detail_prev')}><ChevronUpIcon /></Button>
                  <Button type="button" variant="ghost" size="icon-sm" className={TOUCH_TARGET} disabled={!nav.onNext} onClick={nav.onNext} aria-label={t('ui.data.detail_next')} title={t('ui.data.detail_next')}><ChevronDownIcon /></Button>
                </>
              )}
              <DialogPrimitive.Close
                render={<Button type="button" variant="ghost" size="icon-sm" className={TOUCH_TARGET} aria-label={t('ui.data.detail_close')} title={t('ui.data.detail_close')} />}
              >
                <XIcon />
              </DialogPrimitive.Close>
            </div>
          </header>
          <DialogFooterSlot.Provider value={slot}>
            {onSubmit ? (
              <form noValidate className="flex min-h-0 flex-1 flex-col" onSubmit={onSubmit}>{body}{foot}</form>
            ) : (
              <>{body}{foot}</>
            )}
          </DialogFooterSlot.Provider>
        </DialogPrimitive.Popup>
      </DialogPortal>
    </Dialog>
  )
}

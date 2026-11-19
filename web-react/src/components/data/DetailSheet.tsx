import type { ReactNode } from 'react'
import { XIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Sheet, SheetClose, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { cn } from '@/lib/utils'

export interface DetailSheetProps {
  open: boolean
  /** Called for Esc, the close button and a click outside. Wire it to useUrlSelection().clear so Back and Esc agree. */
  onClose: () => void
  title: ReactNode
  /** A status dot or badge beside the title. */
  status?: ReactNode
  description?: ReactNode
  /** Buttons under the title (open the full page, edit, more). At most one primary. */
  actions?: ReactNode
  footer?: ReactNode
  children?: ReactNode
  className?: string
}

/**
 * The quick-look panel for the row selected in a list (plan 3.1): title, status
 * and actions on top, a body that scrolls on its own, an optional footer. Drive
 * `open` from useUrlSelection, so the opened record is a link, Back closes it and
 * Esc and Back do the same thing.
 *
 * Focus is trapped while it is open and returns to the row that opened it
 * (Base UI's dialog does both). It is full width on a phone, where a quarter
 * of the screen of list behind it is no use.
 */
export function DetailSheet({ open, onClose, title, status, description, actions, footer, children, className }: DetailSheetProps) {
  const { t } = useTranslation('common')
  return (
    <Sheet open={open} onOpenChange={next => { if (!next) onClose() }}>
      <SheetContent
        side="right"
        showCloseButton={false}
        className={cn(
          'gap-0 p-0 data-[side=right]:w-full data-[side=right]:sm:w-[30rem] data-[side=right]:sm:max-w-[30rem]',
          className,
        )}
      >
        <SheetHeader className="gap-2 border-b p-4 pr-14">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
            <SheetTitle className="min-w-0 truncate text-base">{title}</SheetTitle>
            {status}
          </div>
          {description && <SheetDescription>{description}</SheetDescription>}
          {actions && <div className="flex flex-wrap items-center gap-2 pt-1">{actions}</div>}
          <SheetClose
            render={<Button variant="ghost" size="icon-sm" className="absolute top-3 right-3" aria-label={t('ui.data.detail_close')} title={t('ui.data.detail_close')} />}
          >
            <XIcon />
          </SheetClose>
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-y-auto p-4">{children}</div>
        {footer && <SheetFooter className="mt-0 flex-row flex-wrap justify-end border-t">{footer}</SheetFooter>}
      </SheetContent>
    </Sheet>
  )
}

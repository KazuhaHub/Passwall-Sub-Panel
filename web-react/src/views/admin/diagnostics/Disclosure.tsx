import { useId, type ReactNode } from 'react'
import { IconChevronDown } from '@/lib/icons'
import { cn } from '@/lib/utils'

/**
 * A header button that shows or hides its content. The content is not rendered
 * while closed, so a closed disclosure costs nothing on a page that holds
 * hundreds of rows of raw data. Open state belongs to the caller: the page
 * keeps it across refreshes, so a refresh never folds what someone opened.
 */
export function Disclosure({ open, onToggle, summary, children, className, buttonClassName }: {
  open: boolean
  onToggle: () => void
  summary: ReactNode
  children: ReactNode
  className?: string
  buttonClassName?: string
}) {
  const id = useId()
  return (
    <div className={className}>
      <button type="button" aria-expanded={open} aria-controls={open ? id : undefined} onClick={onToggle}
        className={cn(
          'flex w-full min-w-0 items-center gap-2 rounded-md text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50 pointer-coarse:min-h-[2rem]',
          buttonClassName,
        )}>
        <IconChevronDown aria-hidden className={cn('size-4 shrink-0 text-muted-foreground transition-transform', !open && '-rotate-90')} />
        <span className="min-w-0 flex-1">{summary}</span>
      </button>
      {open && <div id={id}>{children}</div>}
    </div>
  )
}

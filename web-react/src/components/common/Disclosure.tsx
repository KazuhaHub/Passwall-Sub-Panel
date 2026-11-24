import { useId, useState, type ReactNode } from 'react'
import { IconChevronDown } from '@/lib/icons'
import { cn } from '@/lib/utils'

/**
 * A header button that shows or hides its content. The content is NOT MOUNTED while closed (not merely hidden), so
 * a closed disclosure costs nothing on a page that holds hundreds of rows of raw data, and a credential that is not
 * shown is not in the page either.
 *
 * Controlled when `open` is given (the page keeps the state across refreshes, so a refresh never folds what someone
 * opened; pass `onToggle`), otherwise it keeps its own state, starting from `defaultOpen`. `className` styles the
 * wrapper (a border, a divider), `buttonClassName` the header button and `bodyClassName` the content box.
 */
export function Disclosure({ open: controlled, onToggle, defaultOpen = false, summary, children, className, buttonClassName, bodyClassName }: {
  open?: boolean
  onToggle?: () => void
  defaultOpen?: boolean
  summary: ReactNode
  children: ReactNode
  className?: string
  buttonClassName?: string
  bodyClassName?: string
}) {
  const id = useId()
  const [own, setOwn] = useState(defaultOpen)
  const open = controlled ?? own
  return (
    <div className={className}>
      <button type="button" aria-expanded={open} aria-controls={open ? id : undefined} onClick={() => (controlled === undefined ? setOwn(v => !v) : onToggle?.())}
        className={cn(
          'flex w-full min-w-0 items-center gap-2 rounded-md text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50 pointer-coarse:min-h-[2rem]',
          buttonClassName,
        )}>
        <IconChevronDown aria-hidden className={cn('size-4 shrink-0 text-muted-foreground transition-transform', !open && '-rotate-90')} />
        <span className="min-w-0 flex-1">{summary}</span>
      </button>
      {open && <div id={id} className={bodyClassName}>{children}</div>}
    </div>
  )
}

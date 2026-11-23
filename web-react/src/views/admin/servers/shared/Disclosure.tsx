import { useId, useState, type ReactNode } from 'react'
import { IconChevronRight } from '@/lib/icons'
import { cn } from '@/lib/utils'

/**
 * A fold for reference text and for material that should not sit on screen unasked. The body is NOT MOUNTED while
 * closed (not merely hidden), which is the point for the installation secrets: a credential that is not shown is not
 * in the page either.
 */
export function Disclosure({ label, children, defaultOpen = false, className }: { label: ReactNode; children: ReactNode; defaultOpen?: boolean; className?: string }) {
  const id = useId()
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className={cn('rounded-lg border', className)}>
      <button type="button" id={id} aria-expanded={open} aria-controls={`${id}-body`} onClick={() => setOpen(v => !v)}
        className="flex min-h-(--control-h) w-full items-center gap-1.5 rounded-lg px-3 text-left text-sm font-medium outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
        <IconChevronRight aria-hidden className={cn('size-4 shrink-0 transition-transform', open && 'rotate-90')} />
        {label}
      </button>
      {open && <div id={`${id}-body`} role="region" aria-labelledby={id} className="flex flex-col gap-3 border-t px-3 py-3">{children}</div>}
    </div>
  )
}

import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/**
 * THE EMPTY STATE (plan 3.4): one sentence saying what is missing and why that
 * is fine, and at most one action that fixes it. Not a blank table and not a
 * paragraph; when there is nothing to do about it, `action` is simply omitted.
 * `compact` is for inside a card or a sheet, where the dashed frame would be
 * one border too many.
 */
export function EmptyState({ icon, title, description, action, compact, className }: {
  icon?: ReactNode
  title: string
  description?: string
  action?: ReactNode
  compact?: boolean
  className?: string
}) {
  return (
    <div
      data-slot="empty-state"
      className={cn(
        'flex flex-col items-center justify-center gap-2 text-center',
        compact ? 'px-4 py-8' : 'rounded-xl border border-dashed px-6 py-14',
        className,
      )}
    >
      {icon && (
        <div aria-hidden className="mb-1 flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground [&_svg]:size-6">
          {icon}
        </div>
      )}
      <div className="text-base font-medium">{title}</div>
      {description && <p className="max-w-md text-sm text-muted-foreground">{description}</p>}
      {action && <div className="mt-2">{action}</div>}
    </div>
  )
}

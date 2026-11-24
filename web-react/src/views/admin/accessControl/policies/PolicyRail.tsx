import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/**
 * The match-order rail (S2): a 2px vertical line down the left, a numbered dot
 * per step. The same line and dots draw the test sheet's trace, so the two
 * read as one picture of the order. Numbers come from pipelineSteps(), never
 * from here.
 */
export function RailStep({ n, title, caption, actions, children, last = false }: {
  n: number
  title: ReactNode
  caption?: ReactNode
  actions?: ReactNode
  children?: ReactNode
  last?: boolean
}) {
  return (
    <div className={cn('relative pl-7 sm:pl-10', !last && 'pb-4')}>
      {!last && <span aria-hidden className="absolute top-5 bottom-0 left-2 w-0.5 bg-border sm:top-6 sm:left-[11px]" />}
      <span aria-hidden className="absolute top-0 left-0 flex size-5 items-center justify-center rounded-full bg-muted text-xs font-semibold sm:size-6">{n}</span>
      <div className={cn('flex min-h-5 flex-wrap items-center justify-between gap-2 sm:min-h-6', children && 'mb-2')}>
        <h2 className="min-w-0 text-base font-semibold">
          {title}
          {caption ? <span className="text-[13px] font-normal text-muted-foreground">{' · '}{caption}</span> : null}
        </h2>
        {actions && <div className="ml-auto shrink-0 whitespace-nowrap">{actions}</div>}
      </div>
      {children}
    </div>
  )
}

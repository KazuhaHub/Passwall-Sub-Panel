import type { ReactNode } from 'react'

import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { MoreMenu, type MoreMenuItem } from './MoreMenu'

/**
 * THE ONE PAGE HEADER (plan 3.1): title, an optional count, an optional one-line
 * description, and on the right at most one primary action, any number of secondary
 * ones, and a "更多" menu for the rest. Pages do not assemble their own; a page that
 * needs a different shape is a sign the shape belongs here.
 *
 * `primary` is the single solid button on the page: the caller passes a `<Button>`
 * (default variant). `secondary` are outline or ghost buttons. `more` holds what is
 * left, destructive items last. On a phone the actions drop under the title and
 * wrap; the primary action is the right-most button.
 */
export function PageHeader({ title, count, description, primary, secondary, more, className }: {
  title: ReactNode
  count?: number | string
  description?: ReactNode
  primary?: ReactNode
  secondary?: ReactNode
  more?: MoreMenuItem[]
  className?: string
}) {
  const hasActions = primary || secondary || (more && more.length > 0)
  return (
    <header data-slot="page-header" className={cn('mb-4 flex flex-wrap items-start justify-between gap-x-4 gap-y-3', className)}>
      <div className="min-w-0 flex-1 basis-60">
        <div className="flex min-w-0 items-center gap-2">
          <h1 className="min-w-0 text-xl font-semibold tracking-tight break-words">{title}</h1>
          {count !== undefined && count !== '' && (
            <Badge variant="secondary" className="shrink-0 tabular-nums" data-slot="page-header-count">{count}</Badge>
          )}
        </div>
        {description ? <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {hasActions && (
        <div data-slot="page-header-actions" className="flex shrink-0 flex-wrap items-center gap-2">
          {secondary}
          {primary}
          {more && more.length > 0 && <MoreMenu items={more} />}
        </div>
      )}
    </header>
  )
}

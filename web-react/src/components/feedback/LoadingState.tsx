import { useTranslation } from 'react-i18next'

import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

// THE LOADING STATE (plan 3.4) is a skeleton in the shape of what is coming, not a
// spinner in the middle of nothing: the page keeps its layout, so nothing jumps
// when the data arrives. Each helper is a role=status region with a hidden label,
// which is what a screen reader hears instead of silence.

function Region({ className, children }: { className?: string; children: React.ReactNode }) {
  const { t } = useTranslation('common')
  return (
    <div role="status" aria-busy="true" data-slot="loading-state" className={className}>
      <span className="sr-only">{t('ui.feedback.loading')}</span>
      <div aria-hidden>{children}</div>
    </div>
  )
}

/**
 * A page while its code or data loads: the same frame as a real page (`p-4 sm:p-6`), a title bar, a toolbar line and
 * a table block, spaced at the page rhythm (16px), so nothing jumps when the page appears. The Suspense fallback of a
 * route. `framed={false}` omits the frame and the title, for a skeleton that sits inside a page that already has its
 * own padding and header (the toolbar and the table block remain).
 */
export function PageSkeleton({ className, framed = true }: { className?: string; framed?: boolean }) {
  return (
    <Region className={cn(framed && 'p-4 sm:p-6', className)}>
      <div className="space-y-4">
        {framed && <Skeleton className="h-7 w-48" />}
        <div className="flex flex-wrap gap-2">
          <Skeleton className="h-(--control-h) w-full sm:w-64" />
          <Skeleton className="h-(--control-h) w-24" />
          <Skeleton className="h-(--control-h) w-24" />
        </div>
        <div className="overflow-hidden rounded-xl border bg-card">
          <div className="flex h-(--row-h) items-center gap-4 border-b bg-muted/50 px-3">
            {Array.from({ length: 5 }, (_, c) => <Skeleton key={c} className={cn('h-3.5', c === 0 ? 'w-1/4' : 'flex-1')} />)}
          </div>
          <TableSkeleton rows={6} cols={5} />
        </div>
      </div>
    </Region>
  )
}

/** The body of a table: `rows` rows of `cols` cells at the page's row height. */
export function TableSkeleton({ rows = 6, cols = 5, className }: { rows?: number; cols?: number; className?: string }) {
  return (
    <Region className={className}>
      <div className="space-y-px">
        {Array.from({ length: rows }, (_, r) => (
          <div key={r} className="flex h-(--row-h) items-center gap-4 border-b px-3">
            {Array.from({ length: cols }, (_, c) => (
              <Skeleton key={c} className={cn('h-3.5', c === 0 ? 'w-1/4' : 'flex-1')} />
            ))}
          </div>
        ))}
      </div>
    </Region>
  )
}

/** A card's body: a few lines of text. */
export function CardSkeleton({ lines = 3, className }: { lines?: number; className?: string }) {
  return (
    <Region className={cn('space-y-2', className)}>
      {Array.from({ length: lines }, (_, i) => (
        <Skeleton key={i} className={cn('h-4', i === lines - 1 ? 'w-2/3' : 'w-full')} />
      ))}
    </Region>
  )
}

/** The default: the page-shaped one, centred where a route's content will be. */
export function LoadingState({ className }: { className?: string }) {
  return <PageSkeleton className={className} />
}

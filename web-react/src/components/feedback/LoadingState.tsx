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

/** A page: a heading bar and a body block. The Suspense fallback of a route. */
export function PageSkeleton({ className }: { className?: string }) {
  return (
    <Region className={cn('space-y-3', className)}>
      <Skeleton className="h-7 w-48" />
      <Skeleton className="h-4 w-80 max-w-full" />
      <Skeleton className="mt-4 h-64 w-full" />
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

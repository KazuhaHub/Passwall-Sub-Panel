import { lazy, Suspense } from 'react'
import { Link } from 'react-router'
import type { TrafficHistoryItem } from '@/api/traffic'
import { ErrorState } from '@/components/feedback/ErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { IconChevronRight } from '@/lib/icons'
import { TzNote } from '../traffic/TzNote'
import { useAdminT } from '../traffic/useAdminT'

// ECharts is the heaviest thing on this page; it loads after the first paint.
const TrafficChart = lazy(() => import('@/components/TrafficChart'))

const HEIGHT = 280

/** The last seven days of panel-wide traffic, bucketed by day in the panel's time zone. */
export function TrendCard({ items, loading, failed, onRetry }: {
  items: TrafficHistoryItem[]
  loading: boolean
  failed: boolean
  onRetry: () => unknown
}) {
  const t = useAdminT()
  const placeholder = <Skeleton className="w-full" style={{ height: HEIGHT }} />
  return (
    <Card size="sm" className="min-w-0">
      <CardHeader>
        <CardTitle>{t('dashboard.trend_title')}</CardTitle>
        <CardAction>
          <Link to="/admin/traffic" className="inline-flex min-h-8 items-center gap-1 rounded text-sm text-brand outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50">
            {t('dashboard.traffic.view_all')}<IconChevronRight className="size-3.5" aria-hidden />
          </Link>
        </CardAction>
      </CardHeader>
      <CardContent>
        {/* The trend is bucketed in PANEL time; say so (and the viewer's own zone) only when they differ. */}
        <TzNote className="mb-2" />
        {failed ? <ErrorState compact title={t('traffic.load_failed')} retry={onRetry} />
          : loading ? placeholder
            : <Suspense fallback={placeholder}><TrafficChart items={items} height={HEIGHT} /></Suspense>}
      </CardContent>
    </Card>
  )
}

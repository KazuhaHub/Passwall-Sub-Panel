import { useMemo } from 'react'
import { Link } from 'react-router'
import type { ColumnDef } from '@tanstack/react-table'
import type { TrafficRow } from '@/api/traffic'
import { DataTable } from '@/components/data/DataTable'
import { Card, CardAction, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { IconChevronRight } from '@/lib/icons'
import { bytesToHuman } from '@/utils/trafficBytes'
import { useAdminT } from '../traffic/useAdminT'

/** Five heaviest users of the current period. The name opens that user's trend on the traffic page. */
export function TopTrafficCard({ rows, loading, failed, onRetry }: {
  rows: TrafficRow[]
  loading: boolean
  failed: boolean
  onRetry: () => void
}) {
  const t = useAdminT()
  const columns = useMemo<ColumnDef<TrafficRow, any>[]>(() => [
    { id: 'rank', header: t('dashboard.traffic.table.rank'), enableSorting: false,
      meta: { label: t('dashboard.traffic.table.rank'), hideable: false, className: 'w-12 text-muted-foreground tabular-nums' },
      cell: ctx => ctx.row.index + 1 },
    { id: 'upn', header: t('dashboard.traffic.table.upn'), enableSorting: false,
      meta: { label: t('dashboard.traffic.table.upn'), hideable: false, className: 'max-w-0 w-full' },
      cell: ctx => (
        <Link to={`/admin/traffic?user=${ctx.row.original.user_id}`} className="block truncate font-medium hover:underline" title={ctx.row.original.upn}>
          {ctx.row.original.upn}
        </Link>
      ) },
    { id: 'period', header: t('dashboard.traffic.table.period_used'), enableSorting: false,
      meta: { label: t('dashboard.traffic.table.period_used'), hideable: false, align: 'right', className: 'whitespace-nowrap tabular-nums' },
      cell: ctx => bytesToHuman(ctx.row.original.period_used_bytes) },
    { id: 'today', header: t('dashboard.traffic.table.today_used'), enableSorting: false,
      meta: { label: t('dashboard.traffic.table.today_used'), hideable: false, align: 'right', className: 'whitespace-nowrap tabular-nums' },
      cell: ctx => bytesToHuman(ctx.row.original.today_used_bytes) },
  ], [t])

  return (
    <Card size="sm" className="min-w-0">
      <CardHeader>
        <CardTitle>{t('dashboard.traffic.title')}</CardTitle>
        <CardAction>
          <Link to="/admin/traffic?tab=rank" className="inline-flex min-h-8 items-center gap-1 rounded text-sm text-brand outline-none hover:underline focus-visible:ring-3 focus-visible:ring-ring/50">
            {t('dashboard.traffic.view_all')}<IconChevronRight className="size-3.5" aria-hidden />
          </Link>
        </CardAction>
      </CardHeader>
      <CardContent>
        <DataTable
          tableId="admin.dashboard.topTraffic"
          aria-label={t('dashboard.traffic.title')}
          data={rows}
          columns={columns}
          getRowId={r => String(r.user_id)}
          pagination={false}
          columnMenu={false}
          maxHeight={null}
          loading={loading}
          error={failed}
          onRetry={onRetry}
          empty={t('dashboard.traffic.empty')}
        />
      </CardContent>
    </Card>
  )
}

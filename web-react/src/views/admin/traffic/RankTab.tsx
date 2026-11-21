import { useMemo } from 'react'
import { Link } from 'react-router'
import type { ColumnDef } from '@tanstack/react-table'
import type { NodeTrafficRow, TrafficRow } from '@/api/traffic'
import { AsyncButton } from '@/components/common/AsyncButton'
import { DataTable } from '@/components/data/DataTable'
import { NativeSelect } from '@/components/ui/native-select'
import { Label } from '@/components/ui/label'
import { IconRefresh } from '@/lib/icons'
import { bytesToHuman } from '@/utils/trafficBytes'
import { useAdminT } from './useAdminT'

export const RANK_LIMITS = [10, 20, 50] as const

const NUM = 'whitespace-nowrap tabular-nums'

/**
 * The leaderboard: the heaviest users or nodes of the current period. The server decides the order, so
 * no column sorts; the table shows the whole list (at most 50) without paging. A user's name opens that
 * user's trend.
 */
export function RankTab({ scope, userRows, nodeRows, limit, onLimit, loading, failed, fetching, onRefresh }: {
  scope: 'user' | 'node'
  userRows: TrafficRow[]
  nodeRows: NodeTrafficRow[]
  limit: number
  onLimit: (n: number) => void
  loading: boolean
  failed: boolean
  fetching: boolean
  onRefresh: () => unknown
}) {
  const t = useAdminT()

  const userColumns = useMemo<ColumnDef<TrafficRow, any>[]>(() => [
    { id: 'rank', header: t('traffic.rank_table.rank'), enableSorting: false,
      meta: { label: t('traffic.rank_table.rank'), hideable: false, className: 'w-12 text-muted-foreground tabular-nums' }, cell: c => c.row.index + 1 },
    { id: 'upn', header: t('traffic.rank_table.upn'), enableSorting: false,
      meta: { label: t('traffic.rank_table.upn'), hideable: false, className: 'max-w-0 w-full' },
      cell: c => (
        <Link to={`/admin/traffic?user=${c.row.original.user_id}`} className="block truncate font-medium hover:underline" title={c.row.original.upn}>{c.row.original.upn}</Link>
      ) },
    { id: 'period', header: t('traffic.rank_table.period_used'), enableSorting: false,
      meta: { label: t('traffic.rank_table.period_used'), align: 'right', className: NUM }, cell: c => bytesToHuman(c.row.original.period_used_bytes) },
    { id: 'today', header: t('traffic.rank_table.today_used'), enableSorting: false,
      meta: { label: t('traffic.rank_table.today_used'), align: 'right', className: NUM }, cell: c => bytesToHuman(c.row.original.today_used_bytes) },
    { id: 'permanent', header: t('traffic.rank_table.permanent'), enableSorting: false,
      meta: { label: t('traffic.rank_table.permanent'), align: 'right', className: NUM }, cell: c => bytesToHuman(c.row.original.permanent_total_bytes) },
  ], [t])

  const nodeColumns = useMemo<ColumnDef<NodeTrafficRow, any>[]>(() => [
    { id: 'rank', header: t('traffic.rank_table.rank'), enableSorting: false,
      meta: { label: t('traffic.rank_table.rank'), hideable: false, className: 'w-12 text-muted-foreground tabular-nums' }, cell: c => c.row.index + 1 },
    { id: 'name', header: t('traffic.node_table.name'), enableSorting: false,
      meta: { label: t('traffic.node_table.name'), hideable: false, className: 'max-w-0 w-1/3' },
      cell: c => {
        const r = c.row.original
        // A tunnel listener is hidden from subscriptions; its traffic reads as a line of the landing it feeds.
        const name = r.tunnel_line ? t('traffic.node_tunnel_line', { parent: r.parent_name ?? `#${r.parent_node_id ?? 0}`, line: r.line_name ?? '' }) : r.display_name
        return <span className="block truncate font-medium" title={name}>{name}</span>
      } },
    { id: 'panel', header: t('traffic.node_table.panel'), enableSorting: false,
      meta: { label: t('traffic.node_table.panel'), className: 'max-w-0 w-1/5 text-muted-foreground' },
      cell: c => <span className="block truncate" title={c.row.original.panel_name}>{c.row.original.panel_name}</span> },
    { id: 'region', header: t('traffic.node_table.region'), enableSorting: false,
      meta: { label: t('traffic.node_table.region'), className: 'whitespace-nowrap' }, cell: c => c.row.original.region },
    { id: 'period', header: t('traffic.node_table.month_used'), enableSorting: false,
      meta: { label: t('traffic.node_table.month_used'), align: 'right', className: NUM }, cell: c => bytesToHuman(c.row.original.period_used_bytes) },
    { id: 'today', header: t('traffic.rank_table.today_used'), enableSorting: false,
      meta: { label: t('traffic.rank_table.today_used'), align: 'right', className: NUM }, cell: c => bytesToHuman(c.row.original.today_used_bytes) },
    { id: 'permanent', header: t('traffic.rank_table.permanent'), enableSorting: false,
      meta: { label: t('traffic.rank_table.permanent'), align: 'right', className: NUM }, cell: c => bytesToHuman(c.row.original.permanent_total_bytes) },
  ], [t])

  const common = { pagination: false as const, loading, error: failed, onRetry: () => void onRefresh(), maxHeight: null }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-x-3 gap-y-3">
        <div className="min-w-0">
          <Label htmlFor="traffic-limit" className="mb-1.5">{t('traffic.rank_limit')}</Label>
          <NativeSelect id="traffic-limit" className="w-36" value={limit} onChange={e => onLimit(Number(e.target.value))}>
            {RANK_LIMITS.map(n => <option key={n} value={n}>{t(`traffic.limit.${n}`)}</option>)}
          </NativeSelect>
        </div>
        {/* The button's wait is the query's fetching state, not isPending: that is false once data exists,
            so a refetch on a slow link would look like a click that did nothing. */}
        <AsyncButton variant="outline" icon={<IconRefresh />} pending={fetching} onClick={onRefresh}>{t('traffic.refresh')}</AsyncButton>
        <p className="min-w-0 basis-60 text-xs text-muted-foreground">{t('traffic.rank_note')}</p>
      </div>
      {scope === 'user'
        ? <DataTable aria-label={t('traffic.tab_rank')} data={userRows} columns={userColumns} getRowId={r => String(r.user_id)} columnMenu={false} {...common} />
        : <DataTable aria-label={t('traffic.tab_rank')} data={nodeRows} columns={nodeColumns} getRowId={r => String(r.node_id)} columnMenu={false} {...common} />}
    </div>
  )
}

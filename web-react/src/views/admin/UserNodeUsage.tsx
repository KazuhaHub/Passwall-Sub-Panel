import { useEffect, useMemo, useState } from 'react'

import type { UserNodeUsageRow } from '@/api/traffic'
import { DataTablePagination } from '@/components/data/DataTablePagination'
import { FilterBar } from '@/components/data/FilterBar'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { TableSkeleton } from '@/components/feedback/LoadingState'
import { Table, TableBody, TableCell, TableFooter, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useUserNodeUsage } from '@/query/traffic'
import { useQueryScope } from '@/query/useQueryScope'
import { UsageBasisToggle, type UsageBasis } from './UsageBasisToggle'
import { FactorBadge } from './users/FactorBadge'
import { useAdminT } from './users/useAdminT'
import { ChargedCell, RawCell, SortHead, sumUsage, usageMetric, type SortDir, type SortKey } from './users/usageShared'

const PAGE_SIZES = [10, 25, 50] as const

/** UserNodeUsage shows one user's usage broken down per node (lifetime / current-period / today, each
 *  split up and down). Lives on the Traffic page's Trend tab (below the chart) when a single user is
 *  selected. All rows are fetched once and paginated / searched / sorted CLIENT-SIDE — a user's node
 *  count is bounded by their group coverage, and the backend already batches the per-node lookup. The
 *  total row always reflects ALL nodes (not the filtered or current page). */
export function UserNodeUsage({ userId }: { userId: number }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const { data: rows, isPending, isError, error, refetch } = useUserNodeUsage(scope, userId)
  const [keyword, setKeyword] = useState('')
  const [orderBy, setOrderBy] = useState<SortKey>('period')
  const [orderDir, setOrderDir] = useState<SortDir>('desc')
  const [basis, setBasis] = useState<UsageBasis>('charged')
  const charged = basis === 'charged'
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<number>(10)

  // Switching user resets the client-side view state. The rows themselves come from the query, which is
  // keyed by user id — so a new target starts pending rather than briefly showing the previous user's nodes.
  useEffect(() => { setPage(1); setKeyword('') }, [userId])

  // Grand total = EVERY node, always — independent of search / paging so it keeps matching the
  // user-level period/lifetime figures shown elsewhere.
  const grand = useMemo(() => sumUsage(rows ?? []), [rows])

  const filtered = useMemo(() => {
    const all = rows ?? []
    const kw = keyword.trim().toLowerCase()
    const f = kw
      ? all.filter(r =>
          (r.display_name || '').toLowerCase().includes(kw) ||
          (r.region || '').toLowerCase().includes(kw) ||
          (r.client_email || '').toLowerCase().includes(kw))
      : all
    const metric = (r: UserNodeUsageRow) => usageMetric(r, orderBy, charged)
    return [...f].sort((a, b) => orderDir === 'desc' ? metric(b) - metric(a) : metric(a) - metric(b))
  }, [rows, keyword, orderBy, orderDir, charged])

  const paged = filtered.slice((page - 1) * pageSize, page * pageSize)

  const setSort = (k: SortKey) => {
    if (orderBy === k) setOrderDir(d => d === 'asc' ? 'desc' : 'asc')
    else { setOrderBy(k); setOrderDir('desc') }
    setPage(1)
  }

  const title = <h3 className="text-sm font-medium">{t('users.nodeusage.title')}</h3>

  if (isPending) return <div className="space-y-2">{title}<TableSkeleton rows={3} cols={4} /></div>
  // A failed read is NOT "this user has no nodes" — that claim would send an admin hunting for a
  // group-coverage problem that may not exist.
  if (isError) {
    return (
      <div className="space-y-2">
        {title}
        <ErrorState compact title={t('users.nodeusage.unavailable')} reason={error} retry={() => refetch()} />
      </div>
    )
  }
  if (!rows || rows.length === 0) {
    return <div className="space-y-2">{title}<EmptyState compact title={t('users.nodeusage.empty')} /></div>
  }

  const metricCells = (c: { life: number; lifeRaw: number; lifeUp: number; lifeDown: number; per: number; perRaw: number; perUp: number; perDown: number; today: number; todayUp: number; todayDown: number }) => (
    <>
      <TableCell>{charged ? <ChargedCell charged={c.life} raw={c.lifeRaw} t={t} /> : <RawCell total={c.lifeRaw} up={c.lifeUp} down={c.lifeDown} />}</TableCell>
      <TableCell>{charged ? <ChargedCell charged={c.per} raw={c.perRaw} t={t} /> : <RawCell total={c.perRaw} up={c.perUp} down={c.perDown} />}</TableCell>
      <TableCell><RawCell total={c.today} up={c.todayUp} down={c.todayDown} /></TableCell>
    </>
  )

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2">
        {title}
        <UsageBasisToggle value={basis} onChange={setBasis} />
      </div>
      <FilterBar
        aria-label={t('users.nodeusage.title')}
        search={{ value: keyword, onChange: v => { setKeyword(v); setPage(1) }, placeholder: t('users.nodeusage.search') }}
      />
      {charged && <p className="text-xs text-muted-foreground">{t('users.nodeusage.charged_hint')}</p>}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('users.nodeusage.node')}</TableHead>
            <SortHead active={orderBy === 'lifetime'} dir={orderDir} onSort={() => setSort('lifetime')}>{t('users.nodeusage.lifetime')}</SortHead>
            <SortHead active={orderBy === 'period'} dir={orderDir} onSort={() => setSort('period')}>{t('users.nodeusage.period')}</SortHead>
            <SortHead active={orderBy === 'today'} dir={orderDir} onSort={() => setSort('today')}>{charged ? t('users.nodeusage.today_raw') : t('users.nodeusage.today')}</SortHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {paged.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} className="text-muted-foreground">{t('users.nodeusage.no_match')}</TableCell>
            </TableRow>
          )}
          {paged.map((r, i) => {
            const name = r.display_name || r.client_email || `#${r.node_id}`
            return (
              <TableRow key={r.node_id || r.client_email || i}>
                <TableCell className="max-w-56">
                  <div className="flex items-center gap-1.5">
                    <span className="min-w-0 truncate font-medium" title={name}>{name}</span>
                    <FactorBadge milli={r.traffic_factor_milli} />
                  </div>
                  {r.region && <div className="truncate text-xs text-muted-foreground" title={r.region}>{r.region}</div>}
                </TableCell>
                {metricCells({
                  life: r.charged_lifetime_bytes, lifeRaw: r.lifetime_total_bytes, lifeUp: r.lifetime_up_bytes, lifeDown: r.lifetime_down_bytes,
                  per: r.charged_period_bytes, perRaw: r.period_total_bytes, perUp: r.period_up_bytes, perDown: r.period_down_bytes,
                  today: r.today_total_bytes, todayUp: r.today_up_bytes, todayDown: r.today_down_bytes,
                })}
              </TableRow>
            )
          })}
        </TableBody>
        <TableFooter>
          {/* Always the grand total of EVERY node — not affected by search or page. */}
          <TableRow>
            <TableCell className="font-semibold">{t('users.nodeusage.total')}</TableCell>
            {metricCells({
              life: grand.chargedLifetime, lifeRaw: grand.lifetime, lifeUp: grand.lifeUp, lifeDown: grand.lifeDown,
              per: grand.chargedPeriod, perRaw: grand.period, perUp: grand.periodUp, perDown: grand.periodDown,
              today: grand.today, todayUp: grand.todayUp, todayDown: grand.todayDown,
            })}
          </TableRow>
        </TableFooter>
      </Table>
      <DataTablePagination
        page={page} pageSize={pageSize} total={filtered.length} shown={paged.length}
        pageSizeOptions={PAGE_SIZES}
        onPageChange={setPage} onPageSizeChange={s => { setPageSize(s); setPage(1) }}
      />
    </div>
  )
}

import { useMemo, useState } from 'react'

import type { UserServerUsageRow } from '@/api/traffic'
import { EmptyState } from '@/components/feedback/EmptyState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { TableSkeleton } from '@/components/feedback/LoadingState'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableFooter, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useUserServerUsage } from '@/query/traffic'
import { useQueryScope } from '@/query/useQueryScope'
import { UsageBasisToggle, type UsageBasis } from './UsageBasisToggle'
import { FactorBadge } from './users/FactorBadge'
import { useAdminT } from './users/useAdminT'
import { ChargedCell, RawCell, SortHead, sumUsage, usageMetric, type SortDir, type SortKey } from './users/usageShared'

/** UserServerUsage shows one user's usage broken down per SERVER (upstream panel) — the per-node rows
 *  aggregated by server: "how much did this user pull from each server". Servers per user are few,
 *  so the whole list renders without search or paging; the total row is the user's grand total across
 *  servers. It renders no page frame of its own: it sits inside a card on the Traffic page. */
export function UserServerUsage({ userId }: { userId: number }) {
  const t = useAdminT()
  const scope = useQueryScope()
  const { data: rows, isPending, isError, error, refetch } = useUserServerUsage(scope, userId)
  const [orderBy, setOrderBy] = useState<SortKey>('period')
  const [orderDir, setOrderDir] = useState<SortDir>('desc')
  const [basis, setBasis] = useState<UsageBasis>('charged')
  const charged = basis === 'charged'

  const grand = useMemo(() => sumUsage(rows ?? []), [rows])
  const sorted = useMemo(() => {
    const metric = (r: UserServerUsageRow) => usageMetric(r, orderBy, charged)
    return [...(rows ?? [])].sort((a, b) => orderDir === 'desc' ? metric(b) - metric(a) : metric(a) - metric(b))
  }, [rows, orderBy, orderDir, charged])

  const setSort = (k: SortKey) => {
    if (orderBy === k) setOrderDir(d => d === 'asc' ? 'desc' : 'asc')
    else { setOrderBy(k); setOrderDir('desc') }
  }

  const title = <h3 className="text-sm font-medium">{t('users.serverusage.title')}</h3>

  if (isPending) return <div className="space-y-2">{title}<TableSkeleton rows={3} cols={4} /></div>
  // A failed read is NOT "this user has no servers" — that claim would send an admin looking for a
  // coverage problem that may not exist.
  if (isError) {
    return (
      <div className="space-y-2">
        {title}
        <ErrorState compact title={t('users.serverusage.unavailable')} reason={error} retry={() => refetch()} />
      </div>
    )
  }
  if (!rows || rows.length === 0) {
    return <div className="space-y-2">{title}<EmptyState compact title={t('users.serverusage.empty')} /></div>
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
      {charged && <p className="text-xs text-muted-foreground">{t('users.nodeusage.charged_hint')}</p>}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('users.serverusage.server')}</TableHead>
            <SortHead active={orderBy === 'lifetime'} dir={orderDir} onSort={() => setSort('lifetime')}>{t('users.nodeusage.lifetime')}</SortHead>
            <SortHead active={orderBy === 'period'} dir={orderDir} onSort={() => setSort('period')}>{t('users.nodeusage.period')}</SortHead>
            <SortHead active={orderBy === 'today'} dir={orderDir} onSort={() => setSort('today')}>{charged ? t('users.nodeusage.today_raw') : t('users.nodeusage.today')}</SortHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {sorted.map((r, i) => {
            const name = r.server_name || `#${r.panel_id}`
            return (
              <TableRow key={r.panel_id || i}>
                <TableCell className="max-w-56">
                  <div className="flex items-center gap-1.5">
                    <span className="min-w-0 truncate font-medium" title={name}>{name}</span>
                    {r.traffic_factor_milli === null ? (
                      <Badge variant="outline" data-testid="traffic-factor-mixed" className="text-[11px]"
                        title={t('users.serverusage.factor_mixed_title')}>
                        {t('users.serverusage.factor_mixed')}
                      </Badge>
                    ) : (
                      <FactorBadge milli={r.traffic_factor_milli} />
                    )}
                  </div>
                  <div className="truncate text-xs text-muted-foreground">{t('users.serverusage.node_count', { n: r.node_count })}</div>
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
    </div>
  )
}

import { lazy, Suspense, useCallback, useMemo, type ReactNode } from 'react'
import type { Node, User } from '@/api/types'
import type { TrafficHistoryPeriod, TrafficHistoryResponse } from '@/api/traffic'
import { AsyncButton } from '@/components/common/AsyncButton'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { ErrorState } from '@/components/feedback/ErrorState'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Card, CardContent } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { IconRefresh } from '@/lib/icons'
import { bytesToHuman } from '@/utils/trafficBytes'
import { UserServerUsage } from '../UserServerUsage'
import { EntityPicker, type PickerOption } from './EntityPicker'
import { TzField } from './TzField'
import { TzNote } from './TzNote'
import type { TrendWindow } from './range'
import { useAdminT } from './useAdminT'

// ECharts loads after the first paint, with the rest of the page already usable.
const TrafficChart = lazy(() => import('@/components/TrafficChart'))

const CHART_HEIGHT = 360

function Labeled({ label, htmlFor, children }: { label: string; htmlFor: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <Label htmlFor={htmlFor} className="mb-1.5">{label}</Label>
      {children}
    </div>
  )
}

/**
 * The trend view: who or what (a user or a node), how far back, how coarse, in which time zone, then the
 * four figures and the chart. Everything that selects data is URL state owned by the page; this component
 * only draws it and reports changes.
 */
export function TrendTab({ scope, users, nodes, userId, nodeId, window: win, tz, panelTz, onUser, onNode, onRange, onPeriod, onTz, history, loading, failed, fetching, onRefresh }: {
  scope: 'user' | 'node'
  users: readonly User[]
  nodes: readonly Node[]
  userId: number
  nodeId: number
  window: TrendWindow
  /** The zone the chart is bucketed in (the URL's choice, or the panel/browser default). */
  tz: string
  panelTz: string
  onUser: (id: number) => void
  onNode: (id: number) => void
  onRange: (days: number) => void
  onPeriod: (p: TrafficHistoryPeriod) => void
  onTz: (tz: string) => void
  history: TrafficHistoryResponse | undefined
  loading: boolean
  failed: boolean
  /** A read is in flight (including a refetch), for the Refresh button; `loading` is only the first one. */
  fetching: boolean
  onRefresh: () => unknown
}) {
  const t = useAdminT()
  const items = useMemo(() => history?.items ?? [], [history])
  const summary = useMemo(() => {
    let total = 0, up = 0, down = 0
    for (const it of items) { total += it.total_bytes; up += it.up_bytes; down += it.down_bytes }
    return { total, up, down }
  }, [items])

  const userOptions = useMemo<PickerOption[]>(() => users.map(u => ({ id: u.id, label: u.display_name ? `${u.display_name} (${u.upn})` : u.upn })), [users])
  const nodeOptions = useMemo<PickerOption[]>(() => nodes.map(n => ({ id: n.id, label: `${n.display_name} · ${n.region}` })), [nodes])
  const fallbackLabel = useCallback((id: number) => `#${id}`, [])

  const { rangeDays, period, rangeOptions } = win
  const placeholder = <Skeleton className="w-full" style={{ height: CHART_HEIGHT }} />

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end gap-x-3 gap-y-3">
        <div className="w-full sm:w-72">
          {scope === 'user'
            ? (
              <Labeled label={t('traffic.scope_user')} htmlFor="traffic-user">
                <EntityPicker id="traffic-user" allLabel={t('traffic.trend.all_users')} options={userOptions} value={userId} onChange={onUser}
                  placeholder={t('traffic.trend.search_user')} emptyLabel={t('traffic.trend.no_match')} fallbackLabel={fallbackLabel} />
              </Labeled>
            )
            : (
              <Labeled label={t('traffic.scope_node')} htmlFor="traffic-node">
                <EntityPicker id="traffic-node" allLabel={t('traffic.trend.all_nodes')} options={nodeOptions} value={nodeId} onChange={onNode}
                  placeholder={t('traffic.trend.search_node')} emptyLabel={t('traffic.trend.no_match')} fallbackLabel={fallbackLabel} />
              </Labeled>
            )}
        </div>
        <Labeled label={t('traffic.trend.range')} htmlFor="traffic-range">
          <NativeSelect id="traffic-range" className="w-40" value={rangeDays} onChange={e => onRange(Number(e.target.value))}>
            {rangeOptions.map(d => <option key={d} value={d}>{t(`traffic.trend.range_${d}`)}</option>)}
          </NativeSelect>
        </Labeled>
        <div className="min-w-0">
          <span id="traffic-period-label" className="mb-1.5 flex text-sm leading-none font-medium">{t('traffic.trend.granularity')}</span>
          {/* Hour buckets exist only for a recent window (7 days at most); past that the button is not offered,
              rather than disabled, to leave one dead control fewer. Today has only hour buckets. */}
          <SegmentedControl
            aria-labelledby="traffic-period-label"
            value={period}
            onChange={onPeriod}
            options={[
              ...(rangeDays <= 7 ? [{ value: 'hour' as const, label: t('traffic.trend.period_hour') }] : []),
              { value: 'day' as const, label: t('traffic.trend.period_day'), disabled: rangeDays === 1 },
              { value: 'week' as const, label: t('traffic.trend.period_week'), disabled: rangeDays === 1 },
              { value: 'month' as const, label: t('traffic.trend.period_month'), disabled: rangeDays === 1 },
            ]}
          />
        </div>
        <TzField value={tz} panelTz={panelTz} onCommit={onTz} />
        {/* The Refresh button's own wait comes from the query's fetching state; `loading` (isPending) is
            only true for the first load, so a refetch on a slow link would look like a dead button. */}
        <AsyncButton variant="outline" icon={<IconRefresh />} pending={fetching} onClick={onRefresh}>{t('traffic.refresh')}</AsyncButton>
      </div>

      <KpiGrid>
        <KpiTile label={t('traffic.trend.summary_total')} value={bytesToHuman(summary.total)} />
        <KpiTile label={t('traffic.trend.summary_up')} value={bytesToHuman(summary.up)} />
        <KpiTile label={t('traffic.trend.summary_down')} value={bytesToHuman(summary.down)} />
        <KpiTile label={t('traffic.trend.summary_range')} value={`${history?.since || '-'} → ${history?.until || '-'}`} valueClassName="text-sm font-medium whitespace-nowrap" />
      </KpiGrid>

      <Card size="sm" className="min-w-0">
        <CardContent>
          <TzNote className="mb-2" />
          {failed ? <ErrorState compact title={t('traffic.load_failed')} retry={onRefresh} />
            : loading ? placeholder
              : <Suspense fallback={placeholder}><TrafficChart items={items} height={CHART_HEIGHT} /></Suspense>}
          <p className="mt-3 text-xs text-muted-foreground">{t('traffic.trend.chart_note')}</p>
        </CardContent>
      </Card>

      {/* Per-SERVER usage, only when one specific user is selected. Since v3.9.0 a shared client spans many
          inbounds and the upstream reports one aggregate counter for it, so the per-node split no longer
          exists; the per-server view is the faithful replacement. */}
      {scope === 'user' && userId !== 0 && (
        <Card size="sm" className="min-w-0">
          <CardContent><UserServerUsage userId={userId} /></CardContent>
        </Card>
      )}
    </div>
  )
}

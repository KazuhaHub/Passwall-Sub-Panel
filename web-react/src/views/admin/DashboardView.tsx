import { useMemo } from 'react'
import type { TrafficHistoryParams } from '@/api/traffic'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { PageHeader } from '@/components/common/PageHeader'
import { ErrorState } from '@/components/feedback/ErrorState'
import { IconGroups, IconServer, IconTime, IconUsers } from '@/lib/icons'
import { useDashboardSummary, useTrafficTrend } from '@/query/dashboard'
import { useTopTraffic } from '@/query/traffic'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { panelDayStr } from '@/utils/datetime'
import { ExpiringCard, NodeAlertsCard } from './dashboard/AttentionCards'
import { TopTrafficCard } from './dashboard/TopTrafficCard'
import { TrendCard } from './dashboard/TrendCard'
import { useAdminT } from './traffic/useAdminT'

/**
 * The first page an administrator sees, ordered by what they act on: the headline figures, then what
 * needs attention (each card links to the page where it is resolved), then the trends.
 *
 * Every figure comes from one pre-aggregated summary read plus two best-effort reads (the 7-day trend
 * and the top-5 table), so the page does not download the user or node lists to count them.
 */
export default function DashboardView() {
  const t = useAdminT()

  // The 7-day trend is windowed and bucketed on PANEL-zone day boundaries, consistent with traffic
  // resets, expiry and the traffic page, not the browser's zone. Empty falls back to the browser's.
  const panelTz = useSiteStore(s => s.timezone)
  const scope = useQueryScope()

  const summaryQuery = useDashboardSummary(scope)
  const summary = summaryQuery.data
  const loading = summaryQuery.isPending

  const topQuery = useTopTraffic(scope, 5)

  const trendParams = useMemo<TrafficHistoryParams>(() => ({
    period: 'day',
    since: panelDayStr(panelTz, -6),
    until: panelDayStr(panelTz, 0),
    tz: panelTz || undefined,
  }), [panelTz])
  const trendQuery = useTrafficTrend(scope, trendParams)

  // Every figure below is read as `summary?.x ?? 0`, so a failed summary would render "0 users, all
  // nodes healthy": a clean bill of health for a fleet nobody read. Say so instead.
  if (summaryQuery.isError) {
    return (
      <div className="p-4 sm:p-6">
        <PageHeader title={t('dashboard.title')} />
        <ErrorState title={t('dashboard.summary_failed')} reason={summaryQuery.error} retry={() => summaryQuery.refetch()} />
      </div>
    )
  }

  const enabled = summary?.user_enabled ?? 0
  const disabled = summary?.user_disabled ?? 0
  const emergency = summary?.user_emergency ?? 0
  const nodeAlerts = summary?.node_alerts ?? []
  const healthy = summary?.node_healthy ?? 0
  const enabledNodes = summary?.node_enabled ?? 0

  return (
    <div className="p-4 sm:p-6">
      <PageHeader title={t('dashboard.title')} />

      <KpiGrid className="mt-0 mb-6">
        <KpiTile
          icon={<IconUsers aria-hidden />}
          label={t('dashboard.metric.users')}
          value={loading ? undefined : summary?.user_total ?? 0}
          caption={loading ? undefined : t('dashboard.metric.users_breakdown', { enabled, disabled })}
        />
        <KpiTile
          icon={<IconServer aria-hidden />}
          label={t('dashboard.metric.nodes')}
          value={loading ? undefined : enabledNodes}
          // Nothing to break down on a fresh panel: "0 / 0 healthy" is true but reads as broken.
          caption={loading || enabledNodes === 0 ? undefined : t('dashboard.metric.nodes_breakdown', { healthy, total: enabledNodes })}
          tone={nodeAlerts.length > 0 ? 'danger' : undefined}
        />
        <KpiTile
          icon={<IconGroups aria-hidden />}
          label={t('dashboard.metric.groups')}
          value={loading ? undefined : summary?.group_count ?? 0}
        />
        <KpiTile
          icon={<IconTime aria-hidden />}
          label={t('dashboard.metric.emergency_active')}
          value={loading ? undefined : emergency}
          caption={loading ? undefined : t('dashboard.metric.emergency_active_hint')}
          tone={emergency > 0 ? 'warning' : undefined}
        />
      </KpiGrid>

      <section aria-labelledby="dashboard-attention" className="mb-6">
        <h2 id="dashboard-attention" className="mb-2 text-sm font-medium text-muted-foreground">{t('dashboard.attention_title')}</h2>
        <div className="grid gap-4 md:grid-cols-2">
          <NodeAlertsCard alerts={nodeAlerts} loading={loading} />
          <ExpiringCard users={summary?.expiring_users ?? []} loading={loading} panelTz={panelTz} />
        </div>
      </section>

      <div className="grid gap-4">
        <TrendCard
          items={trendQuery.data ?? []}
          loading={trendQuery.isPending}
          failed={trendQuery.isError}
          onRetry={() => trendQuery.refetch()}
        />
        <TopTrafficCard
          rows={topQuery.data ?? []}
          loading={topQuery.isPending}
          failed={topQuery.isError}
          onRetry={() => void topQuery.refetch()}
        />
      </div>
    </div>
  )
}

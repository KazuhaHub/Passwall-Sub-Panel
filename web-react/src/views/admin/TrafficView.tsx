import { useCallback, useEffect, useMemo, useState } from 'react'
import { pollTrafficNow, type TrafficHistoryParams, type TrafficHistoryPeriod } from '@/api/traffic'
import { getUISettings } from '@/api/settings'
import { AsyncButton } from '@/components/common/AsyncButton'
import { PageHeader } from '@/components/common/PageHeader'
import { pushSnack } from '@/components/feedback/toasts'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { urlCodecs, useUrlStates } from '@/hooks/useUrlState'
import { useNodesList } from '@/query/nodes'
import { type HistoryTarget, useHistoryTarget, useTopNodes, useTopTraffic } from '@/query/traffic'
import { useUsersList } from '@/query/users'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { RANK_LIMITS, RankTab } from './traffic/RankTab'
import { TrendTab } from './traffic/TrendTab'
import { resolveTrendWindow } from './traffic/range'
import { browserTz } from './traffic/tz'
import { useAdminT } from './traffic/useAdminT'

// Everything that selects data is in the URL, so a refresh, Back and a shared link all land on the same
// view. `tab`, `scope` and `user` are the parameters the page has always read ("view usage" on a user opens
// `?user=N`); the rest are new. A default is left out of the URL to keep links short.
const PERIODS = ['hour', 'day', 'week', 'month'] as const
const SCHEMA = {
  tab: urlCodecs.oneOf(['trend', 'rank'] as const, 'trend'),
  scope: urlCodecs.oneOf(['user', 'node'] as const, 'user'),
  user: urlCodecs.int(0, { min: 0 }),
  node: urlCodecs.int(0, { min: 0 }),
  range: urlCodecs.int(30, { min: 1 }),
  period: urlCodecs.oneOf(PERIODS, 'day'),
  limit: urlCodecs.int(20, { min: 1 }),
  tz: urlCodecs.string(''),
}

function dateString(d: Date) {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function daysAgo(n: number) {
  const d = new Date()
  d.setHours(0, 0, 0, 0)
  d.setDate(d.getDate() - n)
  return d
}

export default function TrafficView() {
  const t = useAdminT()
  const [url, setUrl] = useUrlStates(SCHEMA)
  const { tab, scope, node: nodeId } = url
  const scopeQ = useQueryScope()
  const panelTz = useSiteStore(s => s.timezone)

  // Server-side history retention (the traffic_history_days setting) decides which ranges are offered; 0
  // means "keep everything", read here as unlimited. Loaded once; until it answers, a year is assumed.
  const [historyDays, setHistoryDays] = useState<number>(365)
  useEffect(() => {
    void getUISettings().then(s => {
      const d = Number(s.traffic_history_days) || 0
      setHistoryDays(d > 0 ? d : Number.POSITIVE_INFINITY)
    }).catch(() => { /* keep the default */ })
  }, [])

  const win = useMemo(() => resolveTrendWindow(url.range, url.period as TrafficHistoryPeriod, historyDays), [url.range, url.period, historyDays])
  const limit = (RANK_LIMITS as readonly number[]).includes(url.limit) ? url.limit : 20

  // The chart's zone: the one in the URL, else the panel's, else the browser's. Not remembered across
  // sessions: an administrator who wants a different view picks it again.
  const defaultTz = panelTz || browserTz()
  const tz = url.tz || defaultTz

  // Leaderboards: one cache entry per (scope, limit). Both are read whichever tab is open, as before.
  const userRankQuery = useTopTraffic(scopeQ, limit)
  const nodeRankQuery = useTopNodes(scopeQ, limit)
  const activeRank = scope === 'node' ? nodeRankQuery : userRankQuery

  // Pickers are slow-moving dictionaries: loaded once and invalidated on write, not re-read per filter change.
  const usersQuery = useUsersList(scopeQ, { page: 1, page_size: 200 })
  const nodesQuery = useNodesList(scopeQ, { page: 1, page_size: 500 })

  // Which series the chart draws. Modelled explicitly so the panel-wide, per-user and per-node answers
  // can never share a cache entry.
  const historyTarget = useMemo<HistoryTarget>(
    () => (scope === 'node'
      ? { kind: 'node', nodeId }
      : (url.user > 0 ? { kind: 'user', userId: url.user } : { kind: 'panel' })),
    [scope, nodeId, url.user],
  )
  const historyParams = useMemo<TrafficHistoryParams>(() => ({
    period: win.period,
    since: dateString(daysAgo(win.rangeDays - 1)),
    until: dateString(new Date()),
    tz: tz || undefined,
  }), [win.period, win.rangeDays, tz])
  const historyQuery = useHistoryTarget(scopeQ, historyTarget, historyParams)

  const refreshRank = useCallback(() => activeRank.refetch(), [activeRank])
  const refreshHistory = useCallback(() => historyQuery.refetch(), [historyQuery])

  async function pollNow() {
    await pollTrafficNow()
    pushSnack(t('traffic.poll_done'), 'success')
    await Promise.all([refreshRank(), refreshHistory()])
  }

  // The window the page actually queries is written back only when the person changes something, so a
  // link with an impossible pair still opens on a valid one without rewriting the address bar.
  const setRange = (days: number) => setUrl({ range: days, period: win.period })
  const setPeriod = (p: TrafficHistoryPeriod) => setUrl({ period: p })

  return (
    <div className="p-4 sm:p-6">
      <PageHeader
        title={t('traffic.title')}
        primary={<AsyncButton onClick={pollNow}>{t('traffic.poll_now')}</AsyncButton>}
      />

      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <Tabs value={tab} onValueChange={v => setUrl({ tab: v as 'trend' | 'rank' })}>
          <TabsList>
            <TabsTrigger value="trend">{t('traffic.tab_trend')}</TabsTrigger>
            <TabsTrigger value="rank">{t('traffic.tab_rank')}</TabsTrigger>
          </TabsList>
        </Tabs>
        <SegmentedControl
          aria-label={t('traffic.scope_label')}
          value={scope}
          onChange={v => setUrl({ scope: v })}
          options={[
            { value: 'user', label: t('traffic.scope_user') },
            { value: 'node', label: t('traffic.scope_node') },
          ]}
        />
      </div>

      {tab === 'rank'
        ? (
          <RankTab
            scope={scope}
            userRows={scope === 'user' ? (userRankQuery.data ?? []) : []}
            nodeRows={scope === 'node' ? (nodeRankQuery.data ?? []) : []}
            limit={limit}
            onLimit={n => setUrl({ limit: n })}
            loading={activeRank.isPending}
            failed={activeRank.isError}
            fetching={activeRank.isFetching}
            onRefresh={refreshRank}
          />
        )
        : (
          <TrendTab
            scope={scope}
            users={usersQuery.data?.items ?? []}
            nodes={nodesQuery.data?.items ?? []}
            userId={url.user}
            nodeId={nodeId}
            window={win}
            tz={tz}
            panelTz={panelTz}
            onUser={id => setUrl({ user: id })}
            onNode={id => setUrl({ node: id })}
            onRange={setRange}
            onPeriod={setPeriod}
            onTz={next => setUrl({ tz: next === defaultTz ? '' : next })}
            history={historyQuery.data}
            loading={historyQuery.isPending}
            failed={historyQuery.isError}
            fetching={historyQuery.isFetching}
            onRefresh={refreshHistory}
          />
        )}
    </div>
  )
}

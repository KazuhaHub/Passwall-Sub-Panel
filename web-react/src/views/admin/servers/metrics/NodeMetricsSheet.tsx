import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { IconRefresh } from '@/lib/icons'
import { AsyncButton } from '@/components/common/AsyncButton'
import { KpiGrid, KpiTile } from '@/components/common/KpiTile'
import { StatusBadge } from '@/components/common/StatusBadge'
import type { Tone } from '@/components/common/tones'
import { DetailSheet } from '@/components/data/DetailSheet'
import { CardSkeleton } from '@/components/feedback/LoadingState'
import { ErrorState } from '@/components/feedback/ErrorState'
import { SegmentedControl } from '@/components/form/SegmentedControl'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  getNodeMetricsCurrent, getNodeMetricsHistory, requestNodeMetricsRefresh,
  type HistoryResolution, type NodeHealthFinding, type NodeMetricsCurrent, type NodeMetricsHistory,
  type NodeMetricsSeriesPoint,
} from '@/api/nodeMetrics'
import type { Server } from '@/api/servers'
import { useSiteStore } from '@/stores/site'
import { Notice } from '@/components/common/Notice'
import { useAdminT } from '../../useAdminT'
import NodeDiagnosticsSheet from '../dialogs/NodeDiagnosticsSheet'
import NodeMetricsChart, { formatBits, formatBytes, formatValue, metricsTimeFormatter, type MetricsSeries } from './NodeMetricsChart'

const RANGES: { key: string; hours: number; resolution: HistoryResolution }[] = [
  { key: '1h', hours: 1, resolution: 'minute' },
  { key: '24h', hours: 24, resolution: 'auto' },
  { key: '7d', hours: 24 * 7, resolution: 'hour' },
  { key: '30d', hours: 24 * 30, resolution: 'hour' },
  { key: '90d', hours: 24 * 90, resolution: 'hour' },
]
type Status = 'loading' | 'ready' | 'error'
type TabKey = 'overview' | 'performance' | 'network' | 'diagnostics'
const TABS: TabKey[] = ['overview', 'performance', 'network', 'diagnostics']
const TAB_LABEL: Record<TabKey, string> = {
  overview: 'tabOverview', performance: 'tabPerformance', network: 'tabNetwork', diagnostics: 'tabDiagnostics',
}

/** How each health or freshness word is drawn: healthy and current are a quiet dot, anything else a badge that says why. */
const HEALTH_TONE: Record<string, Tone> = {
  healthy: 'success', fresh: 'success', critical: 'danger', warning: 'warning', stale: 'warning',
  missing: 'info', warming_up: 'info', unavailable: 'neutral', unsupported: 'neutral',
}

/**
 * Host metrics of one Passwall Node server, in a wide sheet over the servers page: the current
 * readings, the history charts for a chosen range, the network figures and the diagnostics entry.
 * Mount it with the server and `open`; nothing is requested while it is closed.
 *
 * The requests are the same as the dialog this replaced: the current readings and the history
 * together on open and on a range change; "Refresh now" asks the node for a sample, then polls the
 * current readings every 2 seconds for up to 30 seconds and reloads everything when a newer sample
 * arrives.
 */
export default function NodeMetricsSheet({ server, open, onClose }: {
  server: Server | null; open: boolean; onClose: () => void
}) {
  const t = useAdminT()
  const { i18n } = useTranslation()
  const timeZone = useSiteStore(state => state.timezone)
  const [tab, setTab] = useState<TabKey>('overview')
  const [range, setRange] = useState(RANGES[0])
  const [data, setData] = useState<{ serverID: number; current: NodeMetricsCurrent; history: NodeMetricsHistory } | null>(null)
  const [status, setStatus] = useState<Status>('loading')
  const [refreshing, setRefreshing] = useState(false)
  const [notice, setNotice] = useState<'refreshTimeout' | 'refreshFailed' | null>(null)
  const [diagnostics, setDiagnostics] = useState(false)
  const refreshController = useRef<AbortController | null>(null)
  const serverID = server?.id ?? 0
  // Keep old history during a range fetch, but never show another server's data.
  const current = data?.serverID === serverID ? data.current : null
  const history = data?.serverID === serverID ? data.history : null
  const dateFormat = metricsTimeFormatter(i18n?.resolvedLanguage, timeZone, true)

  const load = useCallback(async (signal?: AbortSignal) => {
    if (!serverID) return
    setStatus('loading')
    try {
      const to = new Date()
      const from = new Date(to.getTime() - range.hours * 3600_000)
      const [currentBody, historyBody] = await Promise.all([
        getNodeMetricsCurrent(serverID, signal),
        getNodeMetricsHistory(serverID, from.toISOString(), to.toISOString(), range.resolution, signal),
      ])
      if (signal?.aborted) return
      setData({ serverID, current: currentBody, history: historyBody })
      setStatus('ready')
    } catch {
      if (!signal?.aborted) setStatus('error')
    }
  }, [serverID, range])

  useEffect(() => {
    if (!open || !serverID) return
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [open, serverID, load])

  useEffect(() => {
    setNotice(null)
    setRefreshing(false)
    setDiagnostics(false)
    return () => refreshController.current?.abort()
  }, [open, serverID])

  async function refresh() {
    if (!serverID || refreshing) return
    const controller = new AbortController()
    refreshController.current?.abort()
    refreshController.current = controller
    setRefreshing(true)
    setNotice(null)
    const baseline = current?.received_at
    try {
      await requestNodeMetricsRefresh(serverID, controller.signal)
      const deadline = Date.now() + 30_000
      while (!controller.signal.aborted && Date.now() < deadline) {
        await new Promise<void>(resolve => {
          const finish = () => { clearTimeout(timer); controller.signal.removeEventListener('abort', finish); resolve() }
          const timer = setTimeout(finish, 2000)
          controller.signal.addEventListener('abort', finish, { once: true })
        })
        if (controller.signal.aborted) return
        const body = await getNodeMetricsCurrent(serverID, controller.signal)
        if (body.received_at && body.received_at !== baseline) {
          // Refresh current readings, findings and history together. Otherwise
          // Diagnostics could keep showing a warning that the node cleared.
          await load(controller.signal)
          return
        }
      }
      if (!controller.signal.aborted) setNotice('refreshTimeout')
    } catch {
      if (!controller.signal.aborted) setNotice('refreshFailed')
    } finally {
      if (!controller.signal.aborted) setRefreshing(false)
    }
  }

  const chartProps = useMemo(() => ({
    times: history?.series.map(point => point.at) ?? [],
    coverage: history?.series.map(point => point.coverage_seconds) ?? [],
    from: history?.from || undefined, to: history?.to || undefined,
  }), [history])
  const findings = current?.health?.findings ?? []
  const historyTab = tab === 'performance' || tab === 'network'

  const header = current && (
    <>
      {current.available && <Health word={current.health?.status ?? 'unavailable'} />}
      <Health word={current.freshness} />
    </>
  )

  return (
    <>
      <DetailSheet
        open={open}
        onClose={onClose}
        title={t('admin:nodeMetrics.title', { name: server?.name ?? '' })}
        status={header}
        actions={(
          <AsyncButton variant="outline" size="sm" icon={<IconRefresh />} pending={refreshing}
            disabled={status === 'loading'} onClick={refresh}>
            {t('admin:nodeMetrics.refresh')}
          </AsyncButton>
        )}
        className="data-[side=right]:sm:w-[min(60rem,96vw)] data-[side=right]:sm:max-w-[min(60rem,96vw)]"
      >
        {/* A thin bar while a fetch runs over data that is already on screen, so a range change never goes silent. */}
        {status === 'loading' && current && <LoadingBar label={t('common:ui.feedback.loading')} />}
        <Tabs value={tab} onValueChange={value => setTab(value as TabKey)} className="gap-4">
          <TabsList aria-label={t('admin:nodeMetrics.heading')} className="max-w-full overflow-x-auto">
            {TABS.map(key => <TabsTrigger key={key} value={key}>{t(`admin:nodeMetrics.${TAB_LABEL[key]}`)}</TabsTrigger>)}
          </TabsList>

          {status === 'error' && (current
            ? <Notice tone="danger" role="alert" action={<Button size="sm" variant="outline" onClick={() => void load()}>{t('common:actions.retry')}</Button>}>{t('admin:nodeMetrics.loadFailed')}</Notice>
            : <ErrorState compact title={t('admin:nodeMetrics.loadFailed')} retry={() => load()} />)}
          {notice && <Notice>{t(`admin:nodeMetrics.${notice}`)}</Notice>}
          {status === 'loading' && !current && <CardSkeleton lines={5} />}
          {current && !current.available && <Notice tone="info">{t('admin:nodeMetrics.unsupported')}</Notice>}

          {current?.available && (
            <>
              {current.freshness === 'stale' && <Notice>{t('admin:nodeMetrics.staleHint')}</Notice>}
              {current.freshness === 'missing' && <Notice tone="info">{t('admin:nodeMetrics.waitingForSample')}</Notice>}
              <div className="flex flex-col justify-between gap-1 text-sm text-muted-foreground sm:flex-row">
                <span>{t('admin:nodeMetrics.collected')}: {current.collected_at ? dateFormat.format(new Date(current.collected_at)) : '—'}</span>
                <span className="text-xs">{t('admin:nodeMetrics.timezone', { value: timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone })}</span>
              </div>

              {historyTab && (
                <div className="flex flex-col gap-2 rounded-lg bg-muted/50 p-3">
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                    <div>
                      <div className="text-sm font-medium">{t('admin:nodeMetrics.historyRange')}</div>
                      <div className="text-xs text-muted-foreground">{t(`admin:nodeMetrics.range.${range.key}`)}</div>
                    </div>
                    <SegmentedControl
                      aria-label={t('admin:nodeMetrics.historyRange')}
                      value={range.key}
                      onChange={key => setRange(RANGES.find(item => item.key === key)!)}
                      options={RANGES.map(item => ({ value: item.key, label: item.key }))}
                      disabled={status === 'loading' || refreshing}
                      size="sm"
                    />
                  </div>
                  {history?.from && history.to && (
                    <div className="text-xs text-muted-foreground">
                      {dateFormat.format(new Date(history.from))} — {dateFormat.format(new Date(history.to))}
                      {' · '}{t(`admin:nodeMetrics.resolution.${history.resolution}`)}
                      {status === 'loading' && ` · ${t('admin:nodeMetrics.previousRange')}`}
                    </div>
                  )}
                </div>
              )}

              <TabsContent value="overview" className="flex flex-col gap-4">
                <KpiGrid className="my-0 md:grid-cols-3">
                  <KpiTile label={t('admin:nodeMetrics.cpu')} value={show(current.summary.cpu_percent, percentText)}
                    caption={current.summary.cpu_scope ? t(`admin:nodeMetrics.cpuScope.${current.summary.cpu_scope}`) : undefined} />
                  <KpiTile label={t('admin:nodeMetrics.memory')} value={show(current.summary.memory_used_percent, percentText)}
                    caption={current.summary.memory_scope ? t(`admin:nodeMetrics.memoryScope.${current.summary.memory_scope}`) : undefined} />
                  <KpiTile label={t('admin:nodeMetrics.disk')} value={show(current.summary.disk_used_percent, percentText)} />
                  <KpiTile label={t('admin:nodeMetrics.rxBps')} value={show(current.summary.rx_bps, formatBits)} />
                  <KpiTile label={t('admin:nodeMetrics.txBps')} value={show(current.summary.tx_bps, formatBits)} />
                  <KpiTile label={t('admin:nodeMetrics.coreRSS')} value={show(current.summary.core_rss_bytes, formatBytes)} />
                </KpiGrid>
                <Findings findings={findings} />
                <ScopeNote current={current} />
              </TabsContent>

              <TabsContent value="performance" className="flex flex-col gap-4">
                <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
                  <MetricPlot title={t('admin:nodeMetrics.chartCPU')} value={show(current.summary.cpu_percent, percentText)} {...chartProps}
                    series={plotSeries(history, [['system_cpu_percent', t('admin:nodeMetrics.systemCPU'), 'percent'], ['cgroup_cpu_capacity_percent', t('admin:nodeMetrics.cgroupCapacity'), 'percent']])} />
                  <MetricPlot title={t('admin:nodeMetrics.chartMemory')} {...chartProps}
                    series={plotSeries(history, [['memory_used_percent', t('admin:nodeMetrics.memory'), 'percent']])} />
                  <MetricPlot title={t('admin:nodeMetrics.chartDisk')} {...chartProps}
                    series={plotSeries(history, [['disk_available_bytes', t('admin:nodeMetrics.diskAvailable'), 'bytes']])} />
                  <div className="rounded-lg border p-4"><ScopeNote current={current} /></div>
                </div>
              </TabsContent>

              <TabsContent value="network" className="flex flex-col gap-4">
                <KpiGrid className="my-0 md:grid-cols-3">
                  <KpiTile label={t('admin:nodeMetrics.rxBps')} value={show(current.summary.rx_bps, formatBits)} />
                  <KpiTile label={t('admin:nodeMetrics.txBps')} value={show(current.summary.tx_bps, formatBits)} />
                  <KpiTile label={t('admin:nodeMetrics.retrans')} value={show(current.summary.tcp_retrans_percent, percentText)} />
                </KpiGrid>
                <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
                  <MetricPlot title={t('admin:nodeMetrics.network')} {...chartProps}
                    series={plotSeries(history, [['rx_bps', t('admin:nodeMetrics.rxBps'), 'bps'], ['tx_bps', t('admin:nodeMetrics.txBps'), 'bps']])} />
                  <MetricPlot title={t('admin:nodeMetrics.retrans')} {...chartProps}
                    series={plotSeries(history, [['tcp_retrans_percent', t('admin:nodeMetrics.retrans'), 'percent']])} />
                </div>
                <Notice tone="info">{t('admin:nodeMetrics.networkScopeHint')}</Notice>
              </TabsContent>

              <TabsContent value="diagnostics" className="flex flex-col gap-5">
                <Findings findings={findings} />
                <section className="flex flex-col gap-2">
                  <h3 className="text-sm font-medium">{t('admin:nodeMetrics.coreLifecycle')}</h3>
                  <KpiGrid className="my-0 md:grid-cols-3">
                    <KpiTile label={t('admin:nodeMetrics.totalRestarts')} value={current.core?.restarts ?? '—'} caption={t('admin:nodeMetrics.totalRestartsHint')} />
                    <KpiTile label={t('admin:nodeMetrics.unexpectedExits')} value={current.core?.unexpected_exits ?? '—'} caption={t('admin:nodeMetrics.sinceAgentStart')} />
                    <KpiTile label={t('admin:nodeMetrics.lastUnexpectedExit')}
                      value={current.core?.last_unexpected_exit_at ? dateFormat.format(new Date(current.core.last_unexpected_exit_at)) : '—'} />
                  </KpiGrid>
                </section>
                {current.core?.unexpected_exits == null && <Notice tone="info">{t('admin:nodeMetrics.restartReasonUnavailable')}</Notice>}
                <Notice tone="info">{t('admin:nodeMetrics.restartPolicy')}</Notice>
                <section className="flex flex-col gap-2 rounded-lg border p-4">
                  <h3 className="text-sm font-medium">{t('admin:nodeMetrics.investigate')}</h3>
                  <p className="text-sm text-muted-foreground">{t('admin:nodeMetrics.investigateHint')}</p>
                  <div><Button variant="outline" onClick={() => setDiagnostics(true)}>{t('admin:nodeMetrics.openDiagnostics')}</Button></div>
                  <p className="text-sm">{t('admin:nodeMetrics.doctorHint', { command: 'passwall-node doctor --json --data-dir /opt/passwall-node/data' })}</p>
                </section>
              </TabsContent>
            </>
          )}
        </Tabs>
      </DetailSheet>
      {diagnostics && <NodeDiagnosticsSheet server={server} open={open} onClose={() => setDiagnostics(false)} />}
    </>
  )
}

function LoadingBar({ label }: { label: string }) {
  return (
    <div role="progressbar" aria-label={label} aria-busy="true" className="mb-3 h-1 overflow-hidden rounded-full bg-muted">
      <div className="h-full w-1/3 animate-pulse rounded-full bg-brand" />
    </div>
  )
}

function Health({ word }: { word: string }) {
  const t = useAdminT()
  return <StatusBadge tone={HEALTH_TONE[word] ?? 'info'} label={t(`admin:nodeMetrics.freshness.${word}`, word)} />
}

function ScopeNote({ current }: { current: NodeMetricsCurrent }) {
  const t = useAdminT()
  if (!current.scope) return null
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">{t('admin:nodeMetrics.measurementScope')}</h3>
      <dl className="flex flex-wrap gap-x-6 gap-y-1 text-sm">
        <div className="flex gap-1.5"><dt className="text-muted-foreground">{t('admin:nodeMetrics.deployment')}</dt><dd>{t(`admin:nodeMetrics.deploymentValue.${current.scope.deployment}`, current.scope.deployment)}</dd></div>
        <div className="flex gap-1.5"><dt className="text-muted-foreground">{t('admin:nodeMetrics.scope')}</dt><dd>{t(`admin:nodeMetrics.scopeValue.${current.scope.resource_scope}`, current.scope.resource_scope)}</dd></div>
        {current.scope.cgroup_version > 0 && <div className="flex gap-1.5"><dd>cgroup v{current.scope.cgroup_version}</dd></div>}
      </dl>
      {current.scope.resource_scope === 'mixed' && <p className="text-sm text-muted-foreground">{t('admin:nodeMetrics.scopeNote')}</p>}
    </section>
  )
}

function show(value: number | null | undefined, format: (value: number) => string): string { return value == null ? '—' : format(value) }
function percentText(value: number): string { return `${value.toFixed(1)}%` }
function plotSeries(history: NodeMetricsHistory | null, fields: [keyof NodeMetricsSeriesPoint, string, MetricsSeries['unit']][]): MetricsSeries[] {
  return fields.map(([key, name, unit]) => ({ name, unit, values: history?.series.map(point => typeof point[key] === 'number' ? point[key] as number : null) ?? [] }))
}

function MetricPlot({ title, value, ...props }: React.ComponentProps<typeof NodeMetricsChart> & { title: string; value?: string }) {
  const t = useAdminT()
  return (
    <div className="min-w-0 rounded-lg border p-4">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="text-sm font-medium">{title}</h3>
        {value && <span className="text-xs text-muted-foreground">{t('admin:nodeMetrics.latestReading')} <span className="text-sm text-foreground tabular-nums">{value}</span></span>}
      </div>
      <NodeMetricsChart {...props} label={title} />
    </div>
  )
}

function Findings({ findings }: { findings: NodeHealthFinding[] }) {
  const t = useAdminT()
  const { i18n } = useTranslation()
  const timeZone = useSiteStore(state => state.timezone)
  const dateFormat = metricsTimeFormatter(i18n?.resolvedLanguage, timeZone, true)
  const value = (finding: NodeHealthFinding, number: number) => {
    if (finding.unit === 'percent' || finding.unit === 'ratio' || finding.unit === 'bytes' || finding.unit === 'milliseconds') return formatValue(number, finding.unit)
    return `${number.toLocaleString()} ${t(`admin:nodeMetrics.unit.${finding.unit}`, finding.unit)}`
  }
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">{t('admin:nodeMetrics.activeFindings')}</h3>
      {findings.length === 0 && <Notice tone="info">{t('admin:nodeMetrics.noFindings')}</Notice>}
      {findings.map(finding => (
        <Notice key={`${finding.code}:${finding.interface_name ?? ''}:${finding.started_at}`} tone={finding.severity === 'critical' ? 'danger' : 'warning'}>
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{t(`admin:nodeMetrics.finding.${finding.code}`, finding.code)}</span>
            {/* The severity in words, so the tint is never the only thing that says it. */}
            <span className="text-xs text-muted-foreground">{t(`admin:nodeMetrics.freshness.${finding.severity === 'critical' ? 'critical' : 'warning'}`)}</span>
          </div>
          <div className="mt-1 flex flex-wrap gap-x-5 gap-y-1">
            <Evidence label={t('admin:nodeMetrics.observedValue')}>{value(finding, finding.current_value)}</Evidence>
            <Evidence label={t('admin:nodeMetrics.threshold')}>{value(finding, finding.threshold)}</Evidence>
            <Evidence label={t('admin:nodeMetrics.startedAt')}>{dateFormat.format(new Date(finding.started_at))}</Evidence>
            {finding.last_seen_at && <Evidence label={t('admin:nodeMetrics.lastSeenAt')}>{dateFormat.format(new Date(finding.last_seen_at))}</Evidence>}
            {finding.interface_name && <Evidence label={t('admin:nodeMetrics.interface')}>{finding.interface_name}</Evidence>}
          </div>
          {finding.code === 'node_core_restart_loop' && <p className="mt-1 text-muted-foreground">{t('admin:nodeMetrics.restartEvidence', {
            window: (finding.window_seconds ?? 600) / 60, recovery: (finding.recovery_seconds ?? 1800) / 60,
          })}</p>}
        </Notice>
      ))}
    </section>
  )
}

function Evidence({ label, children }: { label: string; children: ReactNode }) {
  return <span><span className="text-muted-foreground">{label}: </span><strong className="font-medium tabular-nums">{children}</strong></span>
}

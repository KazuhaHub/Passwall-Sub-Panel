import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Alert, Box, Button, Chip, CircularProgress, Dialog, DialogContent, DialogTitle,
  Divider, Stack, Tab, Tabs, Typography,
} from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'

import NodeMetricsChart, { formatBits, formatBytes, type MetricsSeries } from '@/components/NodeMetricsChart'
import {
  getNodeHealth, getNodeMetricsCurrent, getNodeMetricsHistory, requestNodeMetricsRefresh,
  type HistoryResolution, type NodeHealthDetail, type NodeMetricsCurrent, type NodeMetricsHistory,
  type NodeMetricsSeriesPoint,
} from '@/api/nodeMetrics'
import type { Server } from '@/api/servers'

/**
 * Node resource telemetry.
 *
 * EVERY STATE HAS ITS OWN WORDS, and that is the point of the component rather
 * than a detail of it: unsupported (an older node), unavailable (nothing readable
 * yet), stale (it stopped reporting) and healthy are four different situations,
 * and a single empty chart would say the same thing about all four.
 */

const RANGES: { key: string; hours: number; resolution: HistoryResolution }[] = [
  { key: '1h', hours: 1, resolution: 'minute' },
  { key: '24h', hours: 24, resolution: 'auto' },
  { key: '7d', hours: 24 * 7, resolution: 'hour' },
  { key: '30d', hours: 24 * 30, resolution: 'hour' },
  { key: '90d', hours: 24 * 90, resolution: 'hour' },
]

type Status = 'loading' | 'ready' | 'error'

export default function NodeMetricsDialog({ server, open, onClose }: {
  server: Server | null
  open: boolean
  onClose: () => void
}) {
  const { t } = useTranslation(['admin', 'common'])
  const [tab, setTab] = useState(0)
  const [range, setRange] = useState(RANGES[0])
  const [current, setCurrent] = useState<NodeMetricsCurrent | null>(null)
  const [history, setHistory] = useState<NodeMetricsHistory | null>(null)
  const [status, setStatus] = useState<Status>('loading')
  const [refreshing, setRefreshing] = useState(false)

  const serverID = server?.id ?? 0

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
      setCurrent(currentBody)
      setHistory(historyBody)
      setStatus('ready')
    } catch {
      if (signal?.aborted) return
      setStatus('error')
    }
  }, [serverID, range])

  useEffect(() => {
    if (!open || !serverID) return
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [open, serverID, load])

  /**
   * The refresh opens a window on the panel; the sample arrives on the node's
   * NEXT sync, so the poll waits for the sample id to change rather than for a
   * fixed delay.
   */
  async function refresh() {
    if (!serverID) return
    setRefreshing(true)
    const baseline = current?.received_at
    try {
      await requestNodeMetricsRefresh(serverID)
      const deadline = Date.now() + 30_000
      while (Date.now() < deadline) {
        await new Promise(resolve => setTimeout(resolve, 2000))
        const body = await getNodeMetricsCurrent(serverID)
        if (body.received_at && body.received_at !== baseline) {
          setCurrent(body)
          break
        }
      }
    } finally {
      setRefreshing(false)
    }
  }

  const times = useMemo(() => history?.series.map(p => p.at) ?? [], [history])
  const coverage = useMemo(() => history?.series.map(p => p.coverage_seconds) ?? [], [history])
  const hasPoints = (history?.series.length ?? 0) > 0

  return (
    <Dialog open={open} onClose={onClose} fullWidth maxWidth="lg">
      <DialogTitle>
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
          <span>{t('admin:nodeMetrics.title', { name: server?.name ?? '' })}</span>
          {current && <HealthChip health={current.available ? current.freshness : 'unsupported'} />}
          <Box sx={{ flexGrow: 1 }} />
          <Button size="small" startIcon={<RefreshIcon />} onClick={refresh} disabled={refreshing}>
            {t('admin:nodeMetrics.refresh')}
          </Button>
        </Stack>
      </DialogTitle>
      <DialogContent dividers>
        {status === 'error' && <Alert severity="error">{t('admin:nodeMetrics.loadFailed')}</Alert>}
        {status === 'loading' && !current && (
          <Box sx={{ display: 'flex', justifyContent: 'center', py: 6 }}><CircularProgress /></Box>
        )}
        {current && !current.available && (
          <Alert severity="info">{t('admin:nodeMetrics.unsupported')}</Alert>
        )}
        {current && current.available && (
          <>
            <ScopeNote current={current} />
            <Tabs value={tab} onChange={(_, next) => setTab(next)} sx={{ mb: 2 }}>
              <Tab label={t('admin:nodeMetrics.tabOverview')} />
              <Tab label={t('admin:nodeMetrics.tabPerformance')} />
              <Tab label={t('admin:nodeMetrics.tabNetwork')} />
              <Tab label={t('admin:nodeMetrics.tabDiagnostics')} />
            </Tabs>
            <Stack direction="row" spacing={1} sx={{ mb: 2, flexWrap: 'wrap' }}>
              {RANGES.map(item => (
                <Chip
                  key={item.key}
                  label={item.key}
                  size="small"
                  color={item.key === range.key ? 'primary' : 'default'}
                  onClick={() => setRange(item)}
                />
              ))}
            </Stack>
            {tab === 0 && <OverviewTab current={current} />}
            {tab === 1 && (
              <PerformanceTab
                current={current} times={times} coverage={coverage} points={history?.series ?? []}
                empty={!hasPoints}
              />
            )}
            {tab === 2 && <NetworkTab current={current} />}
            {tab === 3 && <DiagnosticsTab serverID={serverID} current={current} />}
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}

function HealthChip({ health }: { health: string }) {
  const { t } = useTranslation(['admin'])
  const color = health === 'critical' ? 'error'
    : health === 'warning' || health === 'stale' ? 'warning'
      : health === 'healthy' ? 'success' : 'default'
  return <Chip size="small" color={color} label={t(`admin:nodeMetrics.freshness.${health}`)} />
}

/**
 * The scope is shown, never folded away. A container reading the host's /proc and
 * a container reading its own cgroup produce the same field names, so "mixed"
 * must not be rendered as "host" — that is how a 512 MiB container gets drawn
 * against the host's RAM.
 */
function ScopeNote({ current }: { current: NodeMetricsCurrent }) {
  const { t } = useTranslation(['admin'])
  if (!current.scope) return null
  return (
    <Stack direction="row" spacing={1} sx={{ mb: 1, flexWrap: 'wrap' }}>
      <Chip size="small" variant="outlined" label={t('admin:nodeMetrics.deployment') + ': ' + current.scope.deployment} />
      <Chip
        size="small"
        variant="outlined"
        color={current.scope.resource_scope === 'host' ? 'default' : 'info'}
        label={t('admin:nodeMetrics.scope') + ': ' + t(`admin:nodeMetrics.scopeValue.${current.scope.resource_scope}`, current.scope.resource_scope)}
      />
      {current.scope.cgroup_version > 0 && (
        <Chip size="small" variant="outlined" label={`cgroup v${current.scope.cgroup_version}`} />
      )}
      {current.collected_at && (
        <Chip size="small" variant="outlined" label={`${t('admin:nodeMetrics.collected')}: ${new Date(current.collected_at).toLocaleString()}`} />
      )}
    </Stack>
  )
}

function MetricCard({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <Box sx={{ minWidth: 160, flex: '1 1 160px' }}>
      <Typography variant="caption" color="text.secondary">{label}</Typography>
      <Typography variant="h6">{value}</Typography>
      {hint && <Typography variant="caption" color="text.secondary">{hint}</Typography>}
    </Box>
  )
}

/** A missing value is an em dash, never a zero. */
function show(value: number | null | undefined, format: (v: number) => string): string {
  return value === null || value === undefined ? '—' : format(value)
}

function percentText(value: number): string { return `${value.toFixed(1)}%` }

function OverviewTab({ current }: { current: NodeMetricsCurrent }) {
  const { t } = useTranslation(['admin'])
  const summary = current.summary
  return (
    <Stack spacing={2}>
      <Stack direction="row" spacing={3} sx={{ flexWrap: 'wrap' }}>
        <MetricCard
          label={t('admin:nodeMetrics.cpu')}
          value={show(summary.cpu_percent, percentText)}
          hint={summary.cpu_scope ? t(`admin:nodeMetrics.cpuScope.${summary.cpu_scope}`, summary.cpu_scope) : undefined}
        />
        <MetricCard
          label={t('admin:nodeMetrics.memory')}
          value={show(summary.memory_used_percent, percentText)}
          hint={summary.memory_scope ? t(`admin:nodeMetrics.memoryScope.${summary.memory_scope}`, summary.memory_scope) : undefined}
        />
        <MetricCard label={t('admin:nodeMetrics.disk')} value={show(summary.disk_used_percent, percentText)} />
        <MetricCard
          label={t('admin:nodeMetrics.network')}
          value={show(summary.tx_bps, formatBits)}
          hint={t('admin:nodeMetrics.networkHint')}
        />
        <MetricCard label={t('admin:nodeMetrics.coreRSS')} value={show(summary.core_rss_bytes, formatBytes)} />
      </Stack>
      {current.health && current.health.findings.length > 0 && (
        <>
          <Divider />
          <Typography variant="subtitle2">{t('admin:nodeMetrics.activeFindings')}</Typography>
          <Stack spacing={1}>
            {current.health.findings.map(finding => (
              <Alert key={finding.code} severity={finding.severity === 'critical' ? 'error' : 'warning'}>
                {t(`admin:nodeMetrics.finding.${finding.code}`, finding.code)}
              </Alert>
            ))}
          </Stack>
        </>
      )}
    </Stack>
  )
}

/**
 * THE CHART BREAKS ITS LINE AT A GAP. A null in the series is a sample that could
 * not be derived — a reboot, an outage, a counter reset — and interpolating
 * across one would draw a confident slope through the event an operator is
 * looking for.
 */
function PerformanceTab({ current, times, coverage, points, empty }: {
  current: NodeMetricsCurrent
  times: string[]
  coverage: number[]
  points: NodeMetricsSeriesPoint[]
  empty: boolean
}) {
  const { t } = useTranslation(['admin'])
  const series = (pick: (p: NodeMetricsSeriesPoint) => number | null | undefined, name: string, unit: MetricsSeries['unit']): MetricsSeries => ({
    name,
    unit,
    values: points.map(p => {
      const value = pick(p)
      return value === undefined ? null : value
    }),
  })
  return (
    <Stack spacing={3}>
      {empty && <Alert severity="info">{t('admin:nodeMetrics.noHistory')}</Alert>}
      <ChartBlock title={t('admin:nodeMetrics.chartCPU')}>
        <NodeMetricsChart
          times={times} coverage={coverage}
          series={[
            series(p => p.system_cpu_percent, t('admin:nodeMetrics.systemCPU'), 'percent'),
            series(p => p.cgroup_cpu_capacity_percent, t('admin:nodeMetrics.cgroupCapacity'), 'percent'),
          ]}
        />
      </ChartBlock>
      <ChartBlock title={t('admin:nodeMetrics.chartMemory')}>
        <NodeMetricsChart times={times} coverage={coverage}
          series={[series(p => p.memory_used_percent, t('admin:nodeMetrics.memory'), 'percent')]} />
      </ChartBlock>
      <ChartBlock title={t('admin:nodeMetrics.chartDisk')}>
        <NodeMetricsChart times={times} coverage={coverage}
          series={[series(p => p.disk_available_bytes, t('admin:nodeMetrics.diskAvailable'), 'bytes')]} />
      </ChartBlock>
      <Typography variant="caption" color="text.secondary">
        {t('admin:nodeMetrics.currentNow', { value: show(current.summary.cpu_percent, percentText) })}
      </Typography>
    </Stack>
  )
}

function ChartBlock({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <Box>
      <Typography variant="subtitle2" sx={{ mb: 1 }}>{title}</Typography>
      {children}
    </Box>
  )
}

function NetworkTab({ current }: { current: NodeMetricsCurrent }) {
  const { t } = useTranslation(['admin'])
  const summary = current.summary
  return (
    <Stack spacing={2}>
      <Stack direction="row" spacing={3} sx={{ flexWrap: 'wrap' }}>
        <MetricCard label={t('admin:nodeMetrics.rxBps')} value={show(summary.rx_bps, formatBits)} />
        <MetricCard label={t('admin:nodeMetrics.txBps')} value={show(summary.tx_bps, formatBits)} />
        <MetricCard label={t('admin:nodeMetrics.retrans')} value={show(summary.tcp_retrans_percent, percentText)} />
      </Stack>
      {/* READ-ONLY, AND THE COPY SAYS SO. An operator who reads "congestion
          control" without being told will reasonably ask whether PSP can change
          it, and the answer is that it never will. */}
      <Alert severity="info">{t('admin:nodeMetrics.bbrReadOnly')}</Alert>
    </Stack>
  )
}

function DiagnosticsTab({ serverID, current }: { serverID: number; current: NodeMetricsCurrent }) {
  const { t } = useTranslation(['admin'])
  // The detailed findings come from their own endpoint, so the overview never
  // loads a snapshot to render a badge and this tab never loads a chart to list
  // a condition.
  const [health, setHealth] = useState<NodeHealthDetail | null>(null)
  useEffect(() => {
    if (!current.available || !serverID) return
    const controller = new AbortController()
    void getNodeHealth(serverID, controller.signal).then(setHealth).catch(() => undefined)
    return () => controller.abort()
  }, [current.available, serverID])
  return (
    <Stack spacing={2}>
      <Stack direction="row" spacing={3} sx={{ flexWrap: 'wrap' }}>
        <MetricCard label={t('admin:nodeMetrics.freshnessLabel')} value={health?.freshness ?? current.freshness} />
        <MetricCard label={t('admin:nodeMetrics.resourceHealth')} value={health?.resource_health ?? '—'} />
      </Stack>
      {health && health.findings.length > 0 && (
        <Stack spacing={1}>
          {health.findings.map(finding => (
            <Alert key={`${finding.code}:${finding.started_at}`} severity={finding.severity === 'critical' ? 'error' : 'warning'}>
              {t(`admin:nodeMetrics.finding.${finding.code}`, finding.code)}
            </Alert>
          ))}
        </Stack>
      )}
      <Typography variant="body2">
        {t('admin:nodeMetrics.doctorHint', { command: 'passwall-node doctor --json' })}
      </Typography>
      <Alert severity="info">{t('admin:nodeMetrics.diagnosticsPhaseTwo')}</Alert>
    </Stack>
  )
}

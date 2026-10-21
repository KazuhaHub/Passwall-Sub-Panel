import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Alert, AlertTitle, Box, Button, Chip, CircularProgress, Dialog, DialogContent, DialogTitle,
  IconButton, LinearProgress, Stack, Tab, Tabs, ToggleButton, ToggleButtonGroup, Typography,
  useMediaQuery, useTheme,
} from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'
import CloseIcon from '@mui/icons-material/Close'
import CheckIcon from '@mui/icons-material/Check'
import { AsyncButton } from '@/components/AsyncButton'
import NodeMetricsChart, { formatBits, formatBytes, formatValue, metricsTimeFormatter, type MetricsSeries } from '@/components/NodeMetricsChart'
import NodeDiagnosticsDialog from './NodeDiagnosticsDialog'
import {
  getNodeMetricsCurrent, getNodeMetricsHistory, requestNodeMetricsRefresh,
  type HistoryResolution, type NodeHealthFinding, type NodeMetricsCurrent, type NodeMetricsHistory,
  type NodeMetricsSeriesPoint,
} from '@/api/nodeMetrics'
import type { Server } from '@/api/servers'
import { useSiteStore } from '@/stores/site'

const RANGES: { key: string; hours: number; resolution: HistoryResolution }[] = [
  { key: '1h', hours: 1, resolution: 'minute' },
  { key: '24h', hours: 24, resolution: 'auto' },
  { key: '7d', hours: 24 * 7, resolution: 'hour' },
  { key: '30d', hours: 24 * 30, resolution: 'hour' },
  { key: '90d', hours: 24 * 90, resolution: 'hour' },
]
const cards = { display: 'grid', gridTemplateColumns: { xs: '1fr 1fr', md: 'repeat(3, minmax(0, 1fr))' }, gap: 2 }
const charts = { display: 'grid', gridTemplateColumns: { xs: 'minmax(0, 1fr)', md: 'repeat(2, minmax(0, 1fr))' }, gap: 2 }
type Status = 'loading' | 'ready' | 'error'

export default function NodeMetricsDialog({ server, open, onClose }: {
  server: Server | null; open: boolean; onClose: () => void
}) {
  const { t, i18n } = useTranslation(['admin', 'common'])
  const theme = useTheme()
  const mobile = useMediaQuery(theme.breakpoints.down('sm'))
  const timeZone = useSiteStore(state => state.timezone)
  const [tab, setTab] = useState(0)
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

  return <>
    <Dialog open={open} onClose={onClose} fullWidth fullScreen={mobile} maxWidth="xl"
      aria-labelledby="node-metrics-title"
      slotProps={{ paper: { sx: { maxWidth: 1280, height: { sm: 'min(900px, calc(100dvh - 64px))' }, minWidth: 0 } } }}>
      <DialogTitle id="node-metrics-title" component="div" sx={{ pb: 2, px: { xs: 2, sm: 4 } }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: 'minmax(0, 1fr) auto', sm: 'minmax(0, 1fr) auto auto' },
          gridTemplateAreas: { xs: '"title close" "status refresh"', sm: '"title refresh close" "status refresh close"' }, gap: 1.5, alignItems: 'center' }}>
          <Box sx={{ gridArea: 'title', minWidth: 0 }}>
            <Typography variant="overline" color="text.secondary">{t('admin:nodeMetrics.heading')}</Typography>
            <Typography component="h2" variant="h6" sx={{ overflowWrap: 'anywhere' }}>{server?.name}</Typography>
          </Box>
          {current && <Stack direction="row" sx={{ gridArea: 'status', gap: 1, flexWrap: 'wrap' }}>
            {current.available && <HealthChip health={current.health?.status ?? 'unavailable'} />}
            <HealthChip health={current.freshness} />
          </Stack>}
          <AsyncButton size="small" startIcon={<RefreshIcon />} pending={refreshing}
            disabled={status === 'loading'} onClick={refresh} sx={{ gridArea: 'refresh', justifySelf: 'end' }}>
            {t('admin:nodeMetrics.refresh')}
          </AsyncButton>
          <IconButton onClick={onClose} aria-label={t('common:actions.close')} sx={{ gridArea: 'close', alignSelf: 'start' }}><CloseIcon /></IconButton>
        </Box>
      </DialogTitle>
      <Tabs value={tab} onChange={(_, next) => setTab(next)} variant={mobile ? 'fullWidth' : 'scrollable'} scrollButtons="auto"
        aria-label={t('admin:nodeMetrics.heading')} sx={{ px: { xs: 2, sm: 3 }, borderBottom: 1, borderColor: 'divider', flexShrink: 0, '& .MuiTab-root': { minWidth: { xs: 0, sm: 90 }, px: { xs: 1, sm: 2 }, fontSize: { xs: 12, sm: 14 } } }}>
        {['tabOverview', 'tabPerformance', 'tabNetwork', 'tabDiagnostics'].map((key, index) =>
          <Tab key={key} id={`node-metrics-tab-${index}`} aria-controls="node-metrics-panel" label={t(`admin:nodeMetrics.${key}`)} />)}
      </Tabs>
      <DialogContent sx={{ p: { xs: 2, sm: 3 }, minWidth: 0 }}>
        <Stack spacing={2.5}>
          {status === 'error' && <Alert severity="error" action={<Button onClick={() => void load()}>{t('common:actions.retry')}</Button>}>{t('admin:nodeMetrics.loadFailed')}</Alert>}
          {notice && <Alert severity="warning">{t(`admin:nodeMetrics.${notice}`)}</Alert>}
          {status === 'loading' && (current ? <LinearProgress /> : <Box sx={{ py: 6, textAlign: 'center' }}><CircularProgress /></Box>)}
          {current && !current.available && <Alert severity="info">{t('admin:nodeMetrics.unsupported')}</Alert>}
          {current?.available && <>
            {current.freshness === 'stale' && <Alert severity="warning">{t('admin:nodeMetrics.staleHint')}</Alert>}
            {current.freshness === 'missing' && <Alert severity="info">{t('admin:nodeMetrics.waitingForSample')}</Alert>}
            <Stack direction={{ xs: 'column', sm: 'row' }} sx={{ justifyContent: 'space-between', gap: 1 }}>
              <Typography variant="body2" color="text.secondary">
                {t('admin:nodeMetrics.collected')}: {current.collected_at ? dateFormat.format(new Date(current.collected_at)) : '—'}
              </Typography>
              <Typography variant="caption" color="text.secondary">{t('admin:nodeMetrics.timezone', { value: timeZone || Intl.DateTimeFormat().resolvedOptions().timeZone })}</Typography>
            </Stack>
            {(tab === 1 || tab === 2) && <Box sx={{ p: 2, borderRadius: 2, bgcolor: 'action.hover' }}>
              <Stack direction={{ xs: 'column', sm: 'row' }} sx={{ gap: 2, justifyContent: 'space-between', alignItems: { sm: 'center' } }}>
                <Box>
                  <Typography variant="subtitle2">{t('admin:nodeMetrics.historyRange')}</Typography>
                  <Typography variant="caption" color="text.secondary">{t(`admin:nodeMetrics.range.${range.key}`)}</Typography>
                </Box>
                <ToggleButtonGroup exclusive value={range.key} aria-label={t('admin:nodeMetrics.historyRange')}
                  onChange={(_, key: string | null) => { if (key) setRange(RANGES.find(item => item.key === key)!) }}
                  size="small" disabled={status === 'loading' || refreshing}
                  sx={{ alignSelf: { xs: 'flex-start', sm: 'auto' }, '& .MuiToggleButton-root': { px: { xs: 1, sm: 1.5 }, minWidth: { xs: 49, sm: 62 }, gap: 0.5 },
                    '& .MuiToggleButton-root.Mui-selected': { bgcolor: theme.palette.md.primaryContainer, color: theme.palette.md.onPrimaryContainer,
                      borderColor: theme.palette.md.primary, fontWeight: 700, '&:hover': { bgcolor: theme.palette.md.primaryContainer } } }}>
                  {RANGES.map(item => <ToggleButton key={item.key} value={item.key} aria-label={item.key}>
                    {range.key === item.key && <CheckIcon sx={{ fontSize: 15 }} />}{item.key}
                  </ToggleButton>)}
                </ToggleButtonGroup>
              </Stack>
              {history?.from && history.to && <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 1 }}>
                {dateFormat.format(new Date(history.from))} — {dateFormat.format(new Date(history.to))}
                {' · '}{t(`admin:nodeMetrics.resolution.${history.resolution}`)}
                {status === 'loading' && ` · ${t('admin:nodeMetrics.previousRange')}`}
              </Typography>}
            </Box>}
            <Box id="node-metrics-panel" role="tabpanel" aria-labelledby={`node-metrics-tab-${tab}`}>
              {tab === 0 && <Stack spacing={3}>
                <Box sx={cards}>
                  <MetricCard label={t('admin:nodeMetrics.cpu')} value={show(current.summary.cpu_percent, percentText)}
                    hint={current.summary.cpu_scope ? t(`admin:nodeMetrics.cpuScope.${current.summary.cpu_scope}`) : undefined} />
                  <MetricCard label={t('admin:nodeMetrics.memory')} value={show(current.summary.memory_used_percent, percentText)}
                    hint={current.summary.memory_scope ? t(`admin:nodeMetrics.memoryScope.${current.summary.memory_scope}`) : undefined} />
                  <MetricCard label={t('admin:nodeMetrics.disk')} value={show(current.summary.disk_used_percent, percentText)} />
                  <MetricCard label={t('admin:nodeMetrics.rxBps')} value={show(current.summary.rx_bps, formatBits)} />
                  <MetricCard label={t('admin:nodeMetrics.txBps')} value={show(current.summary.tx_bps, formatBits)} />
                  <MetricCard label={t('admin:nodeMetrics.coreRSS')} value={show(current.summary.core_rss_bytes, formatBytes)} />
                </Box>
                <Findings findings={findings} />
                <ScopeNote current={current} />
              </Stack>}
              {tab === 1 && <Box sx={charts}>
                <MetricPlot title={t('admin:nodeMetrics.chartCPU')} value={show(current.summary.cpu_percent, percentText)} {...chartProps}
                  series={plotSeries(history, [ ['system_cpu_percent', t('admin:nodeMetrics.systemCPU'), 'percent'], ['cgroup_cpu_capacity_percent', t('admin:nodeMetrics.cgroupCapacity'), 'percent'] ])} />
                <MetricPlot title={t('admin:nodeMetrics.chartMemory')} {...chartProps}
                  series={plotSeries(history, [['memory_used_percent', t('admin:nodeMetrics.memory'), 'percent']])} />
                <MetricPlot title={t('admin:nodeMetrics.chartDisk')} {...chartProps}
                  series={plotSeries(history, [['disk_available_bytes', t('admin:nodeMetrics.diskAvailable'), 'bytes']])} />
                <Box sx={{ p: 2.5, border: 1, borderColor: 'divider', borderRadius: 2 }}><ScopeNote current={current} /></Box>
              </Box>}
              {tab === 2 && <Stack spacing={2}>
                <Box sx={cards}>
                  <MetricCard label={t('admin:nodeMetrics.rxBps')} value={show(current.summary.rx_bps, formatBits)} />
                  <MetricCard label={t('admin:nodeMetrics.txBps')} value={show(current.summary.tx_bps, formatBits)} />
                  <MetricCard label={t('admin:nodeMetrics.retrans')} value={show(current.summary.tcp_retrans_percent, percentText)} />
                </Box>
                <Box sx={charts}>
                  <MetricPlot title={t('admin:nodeMetrics.network')} {...chartProps}
                    series={plotSeries(history, [['rx_bps', t('admin:nodeMetrics.rxBps'), 'bps'], ['tx_bps', t('admin:nodeMetrics.txBps'), 'bps']])} />
                  <MetricPlot title={t('admin:nodeMetrics.retrans')} {...chartProps}
                    series={plotSeries(history, [['tcp_retrans_percent', t('admin:nodeMetrics.retrans'), 'percent']])} />
                </Box>
                <Alert severity="info">{t('admin:nodeMetrics.networkScopeHint')}</Alert>
              </Stack>}
              {tab === 3 && <Stack spacing={3}>
                <Findings findings={findings} />
                <Box>
                  <Typography variant="subtitle1" sx={{ mb: 1.5 }}>{t('admin:nodeMetrics.coreLifecycle')}</Typography>
                  <Box sx={cards}>
                    <MetricCard label={t('admin:nodeMetrics.totalRestarts')} value={current.core?.restarts ?? '—'} hint={t('admin:nodeMetrics.totalRestartsHint')} />
                    <MetricCard label={t('admin:nodeMetrics.unexpectedExits')} value={current.core?.unexpected_exits ?? '—'} hint={t('admin:nodeMetrics.sinceAgentStart')} />
                    <MetricCard label={t('admin:nodeMetrics.lastUnexpectedExit')}
                      value={current.core?.last_unexpected_exit_at ? dateFormat.format(new Date(current.core.last_unexpected_exit_at)) : '—'} />
                  </Box>
                </Box>
                {current.core?.unexpected_exits == null && <Alert severity="info">{t('admin:nodeMetrics.restartReasonUnavailable')}</Alert>}
                <Alert severity="info">{t('admin:nodeMetrics.restartPolicy')}</Alert>
                <Box sx={{ p: 2.5, border: 1, borderColor: 'divider', borderRadius: 2 }}>
                  <Typography variant="subtitle1">{t('admin:nodeMetrics.investigate')}</Typography>
                  <Typography variant="body2" color="text.secondary" sx={{ mt: 1, mb: 2 }}>{t('admin:nodeMetrics.investigateHint')}</Typography>
                  <Button variant="outlined" onClick={() => setDiagnostics(true)}>{t('admin:nodeMetrics.openDiagnostics')}</Button>
                  <Typography variant="body2" sx={{ mt: 2 }}>{t('admin:nodeMetrics.doctorHint', { command: 'passwall-node doctor --json --data-dir /opt/passwall-node/data' })}</Typography>
                </Box>
              </Stack>}
            </Box>
          </>}
        </Stack>
      </DialogContent>
    </Dialog>
    {diagnostics && <NodeDiagnosticsDialog server={server} open={open} onClose={() => setDiagnostics(false)} />}
  </>
}

function HealthChip({ health }: { health: string }) {
  const { t } = useTranslation('admin')
  const theme = useTheme()
  const color = health === 'critical' ? theme.palette.error : health === 'warning' || health === 'stale' ? theme.palette.warning
    : health === 'healthy' ? theme.palette.success : theme.palette.info
  return <Chip size="small" variant="outlined" label={t(`admin:nodeMetrics.freshness.${health}`, health)}
    sx={{ color: color.main, borderColor: color.main, bgcolor: 'transparent' }} />
}

function ScopeNote({ current }: { current: NodeMetricsCurrent }) {
  const { t } = useTranslation('admin')
  if (!current.scope) return null
  return <Box>
    <Typography variant="subtitle2" sx={{ mb: 1 }}>{t('admin:nodeMetrics.measurementScope')}</Typography>
    <Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap' }}>
      <Chip size="small" variant="outlined" label={`${t('admin:nodeMetrics.deployment')}: ${t(`admin:nodeMetrics.deploymentValue.${current.scope.deployment}`, current.scope.deployment)}`} />
      <Chip size="small" variant="outlined" label={`${t('admin:nodeMetrics.scope')}: ${t(`admin:nodeMetrics.scopeValue.${current.scope.resource_scope}`, current.scope.resource_scope)}`}
        sx={{ height: 'auto', minHeight: 32, maxWidth: '100%', '& .MuiChip-label': { py: 0.5, whiteSpace: 'normal' } }} />
      {current.scope.cgroup_version > 0 && <Chip size="small" variant="outlined" label={`cgroup v${current.scope.cgroup_version}`} />}
    </Stack>
    {current.scope.resource_scope === 'mixed' && <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>{t('admin:nodeMetrics.scopeNote')}</Typography>}
  </Box>
}

function MetricCard({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return <Box sx={{ p: { xs: 1.5, sm: 2 }, border: 1, borderColor: 'divider', borderRadius: 2, minWidth: 0 }}>
    <Typography variant="body2" color="text.secondary">{label}</Typography>
    <Typography variant="h5" sx={{ mt: 0.75, fontVariantNumeric: 'tabular-nums', overflowWrap: 'anywhere' }}>{value}</Typography>
    {hint && <Typography variant="caption" color="text.secondary" sx={{ display: 'block', mt: 0.5 }}>{hint}</Typography>}
  </Box>
}
function show(value: number | null | undefined, format: (value: number) => string): string { return value == null ? '—' : format(value) }
function percentText(value: number): string { return `${value.toFixed(1)}%` }
function plotSeries(history: NodeMetricsHistory | null, fields: [keyof NodeMetricsSeriesPoint, string, MetricsSeries['unit']][]): MetricsSeries[] {
  return fields.map(([key, name, unit]) => ({ name, unit, values: history?.series.map(point => typeof point[key] === 'number' ? point[key] as number : null) ?? [] }))
}
function MetricPlot({ title, value, ...props }: React.ComponentProps<typeof NodeMetricsChart> & { title: string; value?: string }) {
  const { t } = useTranslation('admin')
  return <Box sx={{ p: { xs: 1.5, sm: 2.5 }, minWidth: 0, border: 1, borderColor: 'divider', borderRadius: 2 }}>
    <Stack direction="row" sx={{ mb: 2, gap: 1, alignItems: 'baseline', justifyContent: 'space-between', flexWrap: 'wrap' }}>
      <Typography variant="subtitle1">{title}</Typography>
      {value && <Stack direction="row" sx={{ gap: 1, alignItems: 'baseline' }}>
        <Typography variant="caption" color="text.secondary">{t('admin:nodeMetrics.latestReading')}</Typography>
        <Typography variant="body2">{value}</Typography>
      </Stack>}
    </Stack>
    <NodeMetricsChart {...props} label={title} />
  </Box>
}
function Findings({ findings }: { findings: NodeHealthFinding[] }) {
  const { t, i18n } = useTranslation('admin')
  const timeZone = useSiteStore(state => state.timezone)
  const dateFormat = metricsTimeFormatter(i18n?.resolvedLanguage, timeZone, true)
  const value = (finding: NodeHealthFinding, number: number) => {
    if (finding.unit === 'percent' || finding.unit === 'ratio' || finding.unit === 'bytes' || finding.unit === 'milliseconds') return formatValue(number, finding.unit)
    return `${number.toLocaleString()} ${t(`admin:nodeMetrics.unit.${finding.unit}`, finding.unit)}`
  }
  return <Stack spacing={1.5}>
    <Typography variant="subtitle1">{t('admin:nodeMetrics.activeFindings')}</Typography>
    {findings.length === 0 && <Alert severity="info">{t('admin:nodeMetrics.noFindings')}</Alert>}
    {findings.map(finding => <Alert key={`${finding.code}:${finding.interface_name ?? ''}:${finding.started_at}`} severity={finding.severity === 'critical' ? 'error' : 'warning'}
      sx={{ '& .MuiAlert-message': { minWidth: 0, width: '100%' } }}>
      <AlertTitle>{t(`admin:nodeMetrics.finding.${finding.code}`, finding.code)}</AlertTitle>
      <Stack direction="row" sx={{ gap: 1, columnGap: 3, flexWrap: 'wrap' }}>
        <Evidence label={t('admin:nodeMetrics.observedValue')}>{value(finding, finding.current_value)}</Evidence>
        <Evidence label={t('admin:nodeMetrics.threshold')}>{value(finding, finding.threshold)}</Evidence>
        <Evidence label={t('admin:nodeMetrics.startedAt')}>{dateFormat.format(new Date(finding.started_at))}</Evidence>
        {finding.last_seen_at && <Evidence label={t('admin:nodeMetrics.lastSeenAt')}>{dateFormat.format(new Date(finding.last_seen_at))}</Evidence>}
        {finding.interface_name && <Evidence label={t('admin:nodeMetrics.interface')}>{finding.interface_name}</Evidence>}
      </Stack>
      {finding.code === 'node_core_restart_loop' && <Typography variant="body2" sx={{ mt: 1 }}>{t('admin:nodeMetrics.restartEvidence', {
        window: (finding.window_seconds ?? 600) / 60, recovery: (finding.recovery_seconds ?? 1800) / 60,
      })}</Typography>}
    </Alert>)}
  </Stack>
}
function Evidence({ label, children }: { label: string; children: ReactNode }) {
  return <Typography variant="body2" component="span"><Box component="span" sx={{ opacity: 0.8 }}>{label}: </Box><strong>{children}</strong></Typography>
}

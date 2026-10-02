import { useMemo, useRef, useState } from 'react'
import { Box, Button, CircularProgress, Typography, useTheme } from '@mui/material'
import DownloadIcon from '@mui/icons-material/Download'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import RefreshIcon from '@mui/icons-material/Refresh'
import { useQueryClient } from '@tanstack/react-query'
import { Navigate } from 'react-router'
import { resetDiagnostics } from '@/api/diagnostics'
import type { ServerListParams } from '@/api/servers'
import { AsyncButton } from '@/components/AsyncButton'
import { confirm } from '@/components/ConfirmHost'
import HelpTip from '@/components/HelpTip'
import PageHeader from '@/components/PageHeader'
import { pushSnack } from '@/components/SnackbarHost'
import { useDiagnostics } from '@/query/diagnostics'
import { diagnosticsKeys } from '@/query/keys'
import { useServersList } from '@/query/servers'
import { useUISettings } from '@/query/settings'
import { useQueryScope } from '@/query/useQueryScope'
import { useSiteStore } from '@/stores/site'
import { copyToClipboard } from '@/utils/clipboard'
import { useCan } from '@/utils/permissions'
import {
  UNKNOWN_FACTS,
  deriveCards,
  deriveFindings,
  deriveSelfChecks,
  effectivePollIntervalMs,
  expectedPollsMin,
  nextSessionBase,
  pageVerdict,
  panelFacts,
  rawFamilies,
  windowMode,
  type SessionBase,
} from '@/utils/diagnostics'
import type { CardId } from '@/utils/diagnosticsCatalog'
import AreaCards from './AreaCards'
import ExportMenu from './ExportMenu'
import { buildExport, buildPreviousExport, downloadJson, exportFileName, type PageReading } from './exportData'
import FindingList from './FindingList'
import RawMetrics, { type ClosedWindow } from './RawMetrics'
import StatusLine, { panelTime } from './StatusLine'
import { useDiagFormat } from './useDiagFormat'

// THE DIAGNOSTICS PAGE, IN FOUR LAYERS: one status line coloured by the worst
// finding; the problems, each with its denominator, its impact, what to do
// and where; seven area cards in a fixed order; and every raw series the
// registry returned, one click away and exportable. The rules that decide
// what an operator is told live in utils/diagnostics.ts, pure and tested;
// this file only reads, derives once per reading, and lays the result out.

/** One page of the server list, the server's own cap. A list longer than this
 *  is incomplete, and an incomplete list is never used to prove anything. */
const SERVER_LIST: ServerListParams = { page: 1, page_size: 200 }

/**
 * The capability check comes before the page mounts, not after its hooks:
 * the page's queries fire on mount, so a check inside it would already have
 * asked an adminGroup endpoint for an answer that can only be 403. The route
 * is admin-only too (ADMIN_ONLY_ROUTES); this is the page's own check.
 */
export default function DiagnosticsView() {
  const canView = useCan('diagnostics.view')
  if (!canView) return <Navigate to="/admin/dashboard" replace />
  return <DiagnosticsPage />
}

function DiagnosticsPage() {
  const fmt = useDiagFormat()
  const { t } = fmt
  const md = useTheme().palette.md
  const scope = useQueryScope()
  const qc = useQueryClient()
  const panelTz = useSiteStore(s => s.timezone)

  const diag = useDiagnostics(scope)
  // The settings row is only the fallback for the poll interval: the loop's
  // own gauge wins whenever the server publishes it (effectivePollIntervalMs).
  const settings = useUISettings(scope)
  const servers = useServersList(scope, SERVER_LIST)
  const settingsIntervalMs = (settings.data?.cron_traffic_pull_minutes ?? 0) * 60_000
  const facts = useMemo(
    () => servers.data ? panelFacts(servers.data.items, servers.data.total) : UNKNOWN_FACTS,
    [servers.data],
  )

  const [previous, setPrevious] = useState<ClosedWindow | null>(null)
  const [resetting, setResetting] = useState(false)
  const resettingRef = useRef(false)
  const [expanded, setExpanded] = useState<ReadonlySet<CardId>>(new Set())

  const snap = diag.data

  // The first reading this page took is the baseline for "since you opened
  // this page". It lives in component state only, and is retaken whenever the
  // window was reopened (a restart, or a clear by anyone), marked as such so
  // the line says it counts from the restart (nextSessionBase). Adjusted
  // during render, so the stale baseline is never drawn.
  const [base, setBase] = useState<SessionBase>()

  const derived = useMemo(() => {
    if (!snap) return null
    const m = snap.metrics
    // What the loop runs on beats what the settings row asks for: they differ
    // from the moment an admin saves until the loop's next tick, and every
    // gate on this page is judged against this number.
    const interval = effectivePollIntervalMs(m, settingsIntervalMs)
    const mode = windowMode(m.window_ms, interval)
    const findings = deriveFindings(snap, interval, facts)
    const cards = deriveCards(snap, interval, findings, facts)
    return {
      interval,
      mode,
      findings,
      cards,
      verdict: pageVerdict(findings, cards, m.window_ms, interval),
      selfChecks: deriveSelfChecks(m),
      expected: expectedPollsMin(snap),
      families: rawFamilies(m),
    }
  }, [snap, settingsIntervalMs, facts])

  if (snap && derived) {
    const next = nextSessionBase(base, snap.metrics, derived.findings.map(f => f.series))
    if (next !== base) setBase(next)
  }

  if (diag.isPending) {
    return <Box sx={{ p: 3, display: 'flex', justifyContent: 'center' }}><CircularProgress /></Box>
  }
  if (!snap || !derived) {
    return (
      <Box sx={{ p: { xs: 2, sm: 3 } }}>
        <PageHeader title={t('admin:diagnostics.title')} />
        <Box role="alert" sx={{ p: 2, borderRadius: 3, bgcolor: md.errorContainer, color: md.onErrorContainer }}>
          {t('admin:diagnostics.load_failed')}
        </Box>
      </Box>
    )
  }

  const m = snap.metrics
  const { interval, mode, findings, cards, verdict, selfChecks, expected, families } = derived

  const reading = (): PageReading => ({
    read_at: new Date(diag.dataUpdatedAt).toISOString(),
    settings_interval_ms: settingsIntervalMs,
    effective_interval_ms: interval,
    window_mode: mode,
    verdict: verdict.key,
    expected_polls_min: expected,
    findings: findings.map(({ id, severity, values }) => ({ id, severity, values })),
    self_checks: selfChecks.map(({ id, pass, values }) => ({ id, pass, values })),
    cards: cards.map(({ id, state }) => ({ id, state })),
    panel_types: { complete: facts.complete, types: [...facts.types] },
  })
  const onCopy = () => void copyToClipboard(JSON.stringify(buildExport(snap, reading(), Date.now()), null, 2))
  const onDownload = () => {
    const now = Date.now()
    downloadJson(exportFileName('psp-diagnostics', snap.version, now, panelTz), buildExport(snap, reading(), now))
  }
  const onDownloadPrevious = () => {
    if (!previous) return
    downloadJson(
      exportFileName('psp-diagnostics-pre-reset', snap.version, previous.closedAt, panelTz),
      buildPreviousExport(previous.metrics, previous.closedAt, snap, Date.now()),
    )
  }

  const onReset = async () => {
    // A ref as well as state: the item is disabled while a clear runs, but a
    // click can land in the frame before that renders.
    if (resettingRef.current) return
    resettingRef.current = true
    try {
      const ok = await confirm({
        title: t('admin:diagnostics.reset.title'),
        message: t('admin:diagnostics.reset.message', { window: fmt.duration(m.window_ms) }),
        confirmText: t('admin:diagnostics.reset.confirm'),
        destructive: true,
      })
      if (!ok) return
      setResetting(true)
      const res = await resetDiagnostics()
      // The window the clear closed is kept for this visit, so clearing never
      // throws away the measurement it ends.
      const closedAt = res.previous.since_unix_ms + res.previous.window_ms
      setPrevious({ metrics: res.previous, closedAt })
      pushSnack(t('admin:diagnostics.reset.done', { time: panelTime(closedAt, panelTz, fmt.lang) }), 'success')
      await qc.invalidateQueries({ queryKey: diagnosticsKeys.metrics(scope) })
    } catch {
      /* the shared client toasted the failure */
    } finally {
      resettingRef.current = false
      setResetting(false)
    }
  }

  // Clearing an empty window does nothing; clearing one shorter than an
  // interval throws away the measurement the operator is waiting for. Judged
  // on time alone, like the blackout itself: a poll may already have finished
  // inside it, so the reason given never claims one has not.
  const resetDisabledReason = m.window_ms < 1000
    ? t('admin:diagnostics.actions.reset_disabled_empty')
    : mode === 'blackout' ? t('admin:diagnostics.actions.reset_disabled_blackout') : undefined

  const toggleCard = (id: CardId) => setExpanded(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })
  const showData = (id: CardId) => {
    setExpanded(prev => new Set(prev).add(id))
    document.getElementById(`diag-card-${id}`)?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
  }

  return (
    <Box sx={{ p: { xs: 2, sm: 3 }, minWidth: 0 }}>
      <PageHeader
        title={t('admin:diagnostics.title')}
        subtitle={t('admin:diagnostics.subtitle')}
        actions={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 0.5 }}>
            <HelpTip textKey="admin:diagnostics.help" labelKey="admin:diagnostics.help_label" />
            <AsyncButton startIcon={<RefreshIcon />} onClick={() => diag.refetch()}>
              {t('admin:diagnostics.actions.refresh')}
            </AsyncButton>
            <ExportMenu fmt={fmt} onCopy={onCopy} onDownload={onDownload} onReset={() => void onReset()}
              resetting={resetting} resetDisabledReason={resetDisabledReason} />
          </Box>
        }
      />

      <StatusLine verdict={verdict} snap={snap} intervalMs={interval} readAt={diag.dataUpdatedAt} panelTz={panelTz} fmt={fmt} />

      {previous && (
        <Box data-testid="reset-kept" sx={{
          display: 'flex', alignItems: 'center', gap: 1.5, flexWrap: 'wrap', p: 1.5, mb: 2, borderRadius: 3,
          bgcolor: md.surfaceContainerHigh,
        }}>
          <InfoOutlinedIcon aria-hidden sx={{ color: md.onSurfaceVariant }} />
          <Typography variant="body2" sx={{ flex: '1 1 260px', minWidth: 0 }}>
            {t('admin:diagnostics.reset.kept', { window: fmt.duration(previous.metrics.window_ms) })}
          </Typography>
          <Button size="small" variant="outlined" startIcon={<DownloadIcon />} onClick={onDownloadPrevious}>
            {t('admin:diagnostics.reset.download_previous')}
          </Button>
        </Box>
      )}

      <FindingList findings={findings} fmt={fmt} base={base} cur={m} onShowData={showData} />

      <AreaCards snap={snap} cards={cards} mode={mode} intervalMs={interval} settingsIntervalMs={settingsIntervalMs}
        expected={expected} facts={facts} families={families} fmt={fmt} panelTz={panelTz}
        expanded={expanded} onToggle={toggleCard} />

      <RawMetrics current={m} previous={previous} mode={mode} panelTz={panelTz} fmt={fmt} />
    </Box>
  )
}

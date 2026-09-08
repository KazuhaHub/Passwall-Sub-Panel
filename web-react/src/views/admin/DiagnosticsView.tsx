import { useCallback, useEffect, useState } from 'react'
import {
  Alert,
  Box,
  Button,
  Card,
  Chip,
  CircularProgress,
  Divider,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  Tooltip,
  Typography,
} from '@mui/material'
import RefreshIcon from '@mui/icons-material/Refresh'
import RestartAltIcon from '@mui/icons-material/RestartAlt'
import { useTranslation } from 'react-i18next'

import {
  getDiagnostics,
  resetDiagnostics,
  type DiagnosticsSnapshot,
  type HistogramSnapshot,
} from '@/api/diagnostics'
import { getUISettings } from '@/api/settings'
import {
  counter,
  counterFamilyTotal,
  deriveFindings,
  derivePreconditions,
  effectivePollIntervalMs,
  gauge,
  histogram,
  quantileUsable,
  verdict,
  wasReset,
  windowMode,
  type Finding,
  type Severity,
} from '@/utils/diagnostics'
import { confirm } from '@/components/ConfirmHost'
import { pushSnack } from '@/components/SnackbarHost'
import PageHeader from '@/components/PageHeader'

const SEVERITY_COLOR: Record<Severity, 'error' | 'warning'> = {
  critical: 'error',
  error: 'error',
  warn: 'warning',
}

function ms(v: number): string {
  return v >= 1000 ? `${(v / 1000).toFixed(2)} s` : `${Math.round(v)} ms`
}

function duration(v: number): string {
  const mins = Math.round(v / 60_000)
  if (mins < 60) return `${mins} min`
  const h = Math.floor(mins / 60)
  return `${h} h ${mins % 60} min`
}

export default function DiagnosticsView() {
  const { t } = useTranslation(['admin', 'common'])
  const [snap, setSnap] = useState<DiagnosticsSnapshot | null>(null)
  const [intervalMs, setIntervalMs] = useState(0)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      // The poll interval is what makes a window long or short, so a reading
      // without it cannot be judged — windowMode refuses to settle on 0.
      const [d, s] = await Promise.all([
        getDiagnostics(),
        getUISettings().catch(() => null),
      ])
      setSnap(d)
      setIntervalMs((s?.cron_traffic_pull_minutes ?? 0) * 60_000)
    } catch {
      /* axios interceptor toasted */
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  const onReset = async () => {
    if (!snap) return
    const ok = await confirm({
      title: t('admin:diagnostics.reset.title'),
      message: t('admin:diagnostics.reset.message', { window: duration(snap.metrics.window_ms) }),
    })
    if (!ok) return
    setBusy(true)
    try {
      await resetDiagnostics()
      pushSnack(t('admin:diagnostics.reset.done'), 'success')
      await load()
    } catch {
      /* toast */
    } finally {
      setBusy(false)
    }
  }

  if (loading && !snap) {
    return <Box sx={{ p: 3, display: 'flex', justifyContent: 'center' }}><CircularProgress /></Box>
  }
  if (!snap) {
    return (
      <Box sx={{ p: 3 }}>
        <PageHeader title={t('admin:diagnostics.title')} />
        <Alert severity="error">{t('admin:diagnostics.load_failed')}</Alert>
      </Box>
    )
  }

  const m = snap.metrics
  // What the loop is running on beats what the settings row asks for: they
  // differ from the moment an admin saves until the loop's next tick, and
  // every gate below is judged against this number.
  const interval = effectivePollIntervalMs(m, intervalMs)
  const mode = windowMode(m.window_ms, interval)
  const findings = deriveFindings(snap, interval)
  const preconditions = derivePreconditions(snap, interval)
  const v = verdict(findings, mode)
  const reset = wasReset(snap)

  const banner = {
    problems: { severity: 'error' as const, key: 'problems' },
    blackout: { severity: 'info' as const, key: 'blackout' },
    measuring: { severity: 'info' as const, key: 'measuring' },
    ok: { severity: 'success' as const, key: 'ok' },
  }[v]

  return (
    <Box sx={{ p: 3 }}>
      <PageHeader
        title={t('admin:diagnostics.title')}
        subtitle={t('admin:diagnostics.subtitle')}
        actions={
          <>
            <Button startIcon={<RefreshIcon />} onClick={() => void load()} disabled={loading}>
              {t('admin:diagnostics.refresh')}
            </Button>
            <Tooltip title={m.window_ms < 1000 ? t('admin:diagnostics.reset.nothing') : ''}>
              <span>
                <Button
                  startIcon={<RestartAltIcon />}
                  onClick={() => void onReset()}
                  // Resetting during the blackout throws away the window that is
                  // still being measured, which is the one thing the operator is
                  // waiting for.
                  disabled={busy || mode === 'blackout' || m.window_ms < 1000}
                >
                  {t('admin:diagnostics.reset.action')}
                </Button>
              </span>
            </Tooltip>
          </>
        }
      />

      <Alert severity={banner.severity} sx={{ mb: 2 }}>
        <Typography variant="subtitle2">
          {t(`admin:diagnostics.verdict.${banner.key}`, { count: findings.length })}
        </Typography>
        <Typography variant="body2" sx={{ mt: 0.5 }}>
          {t('admin:diagnostics.window', {
            window: duration(m.window_ms),
            uptime: duration(snap.uptime_ms),
          })}
          {reset ? ` · ${t('admin:diagnostics.was_reset')}` : ''}
        </Typography>
      </Alert>

      {/* Answer once at the top: does each downstream area have any input at
          all. Without this, "everything is zero" reads as N separate faults
          instead of one upstream cause. */}
      <Stack direction="row" spacing={1} sx={{ mb: 2, flexWrap: 'wrap', gap: 1 }}>
        {preconditions.map(p => (
          <Tooltip key={p.id} title={t(`admin:diagnostics.precondition.${p.id}.help`)}>
            <Chip
              size="small"
              label={t(`admin:diagnostics.precondition.${p.id}.label`)}
              color={p.state === 'ok' ? 'success' : p.state === 'broken' ? 'error' : 'default'}
              variant={p.state === 'ok' ? 'filled' : 'outlined'}
            />
          </Tooltip>
        ))}
      </Stack>

      {findings.length > 0 && (
        <Card sx={{ mb: 2, p: 2 }}>
          <Typography variant="subtitle2" sx={{ mb: 1 }}>
            {t('admin:diagnostics.findings.title', { count: findings.length })}
          </Typography>
          <Stack spacing={1.5}>
            {findings.map((f: Finding) => (
              <Alert key={f.id} severity={SEVERITY_COLOR[f.severity]} variant="outlined">
                <Typography variant="body2" sx={{ fontWeight: 600 }}>
                  {t(`admin:diagnostics.findings.${f.key}.title`, f.values)}
                </Typography>
                <Typography variant="body2" sx={{ mt: 0.5 }}>
                  {t(`admin:diagnostics.findings.${f.key}.detail`, f.values)}
                </Typography>
              </Alert>
            ))}
          </Stack>
        </Card>
      )}

      {mode === 'blackout' ? (
        // Numbers are withheld rather than greyed: a greyed zero is still a
        // zero on the screen, and it gets read as one.
        <Alert severity="info">
          <Typography variant="body2">
            {t('admin:diagnostics.blackout', { interval: duration(interval || 300_000) })}
          </Typography>
        </Alert>
      ) : (
        <Card>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>{t('admin:diagnostics.table.metric')}</TableCell>
                <TableCell align="right">{t('admin:diagnostics.table.value')}</TableCell>
                <TableCell>{t('admin:diagnostics.table.means')}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              <CounterRow snap={snap} name="psp_poll_total" />
              <CounterRow snap={snap} name="psp_poll_error_total" />
              <HistRow h={histogram(m, 'psp_poll_ms')} name="psp_poll_ms" />
              <HistRow h={histogram(m, 'psp_poll_stage_ms{stage=panel_fetch}')} name="panel_fetch" />
              <CounterRow snap={snap} name="psp_push_client_config_total" />
              <CounterRow snap={snap} name="psp_push_client_config_error_total" />
              <CounterRow snap={snap} name="psp_push_suppressed_total" />
              <CounterRow snap={snap} name="psp_push_sem_carryover_total" />
              <HistRow h={histogram(m, 'psp_push_sem_wait_ms')} name="psp_push_sem_wait_ms" />
              <CounterRow snap={snap} name="psp_lifecycle_sync_total" />
              <CounterRow snap={snap} name="psp_lifecycle_sync_write_total" />
              <CounterRow snap={snap} name="psp_lifecycle_sync_error_total" />
              <FamilyRow snap={snap} name="psp_capability_gap_total" />
            </TableBody>
          </Table>
          <Divider />
          <Box sx={{ p: 1.5 }}>
            <Typography variant="caption" color="text.secondary">
              {t('admin:diagnostics.footer', {
                version: snap.version,
                commit: snap.commit || '—',
                goroutines: snap.goroutines,
                capacity: gauge(m, 'psp_push_sem_capacity')?.value ?? 0,
              })}
            </Typography>
          </Box>
        </Card>
      )}
    </Box>
  )
}

function Row({ name, value, means }: { name: string; value: string; means: string }) {
  return (
    <TableRow>
      <TableCell sx={{ fontFamily: 'monospace', fontSize: 12 }}>{name}</TableCell>
      <TableCell align="right" sx={{ fontVariantNumeric: 'tabular-nums' }}>{value}</TableCell>
      <TableCell><Typography variant="caption" color="text.secondary">{means}</Typography></TableCell>
    </TableRow>
  )
}

function CounterRow({ snap, name }: { snap: DiagnosticsSnapshot; name: string }) {
  const { t } = useTranslation('admin')
  const c = counter(snap.metrics, name)
  // Absent is not zero: a metric that was never registered is a different fact
  // from one that has not been incremented.
  const value = c === undefined ? '—' : String(c.value)
  return <Row name={name} value={value} means={t(`diagnostics.metric.${name}`, { defaultValue: c?.help ?? '' })} />
}

function FamilyRow({ snap, name }: { snap: DiagnosticsSnapshot; name: string }) {
  const { t } = useTranslation('admin')
  const total = counterFamilyTotal(snap.metrics, name)
  return <Row name={name} value={String(total)} means={t(`diagnostics.metric.${name}`, { defaultValue: '' })} />
}

function HistRow({ h, name }: { h: HistogramSnapshot | undefined; name: string }) {
  const { t } = useTranslation('admin')
  if (!h || h.count === 0) {
    return <Row name={name} value="—" means={t('diagnostics.never_observed')} />
  }
  // Under the sample floor a quantile is the max wearing a percentile's name,
  // so report what was actually seen instead of dressing it up.
  const value = quantileUsable(h)
    ? `p50 ${ms(h.p50)} · p95 ${ms(h.p95)}`
    : t('diagnostics.too_few_samples', { count: h.count, max: ms(h.max) })
  return <Row name={name} value={value} means={t(`diagnostics.metric.${name}`, { defaultValue: h.help })} />
}

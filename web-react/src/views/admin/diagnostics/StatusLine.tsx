import { Box, Tooltip, useTheme } from '@mui/material'
import SharedStatusLine, { type StatusLineTone } from '@/components/StatusLine'
import type { DiagnosticsSnapshot } from '@/api/diagnostics'
import { formatMsDualTz } from '@/utils/datetime'
import { wasReset, type PageVerdict } from '@/utils/diagnostics'
import type { DiagFormat } from './useDiagFormat'

/** A panel-time instant, as the rest of the panel writes one. */
export function panelTime(ms: number, tz: string, lang: string, timeOnly = false): string {
  const d = new Date(ms)
  const opts: Intl.DateTimeFormatOptions = timeOnly
    ? { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }
    : { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
  try {
    return d.toLocaleString(lang, { ...opts, timeZone: tz || undefined })
  } catch {
    return d.toLocaleString(lang, opts)
  }
}

export default function StatusLine({
  verdict, snap, intervalMs, readAt, panelTz, fmt,
}: {
  verdict: PageVerdict
  snap: DiagnosticsSnapshot
  intervalMs: number
  readAt: number
  panelTz: string
  fmt: DiagFormat
}) {
  const theme = useTheme()
  const { t } = fmt
  const m = snap.metrics
  const cards = verdict.cards.map(id => t(`admin:diagnostics.cards.${id}.title`)).join(t('admin:diagnostics.status.list_separator'))
  const headline = t(`admin:diagnostics.verdict.${verdict.key}`, {
    cards,
    count: fmt.count(verdict.notices),
    remaining: fmt.duration(verdict.remainingMs ?? 0),
    interval: fmt.duration(intervalMs),
  })
  const since = panelTime(m.since_unix_ms, panelTz, fmt.lang)
  // After a manual clear the window is shorter than the uptime, so the line
  // says so once instead of printing one span twice.
  const window = wasReset(snap)
    ? t('admin:diagnostics.status.window_after_reset', { since, window: fmt.duration(m.window_ms), uptime: fmt.duration(snap.uptime_ms) })
    : t('admin:diagnostics.status.window', { since, window: fmt.duration(m.window_ms) })
  const interval = intervalMs > 0
    ? t('admin:diagnostics.status.interval', { interval: fmt.duration(intervalMs) })
    : t('admin:diagnostics.status.interval_unknown')

  const tones: Record<PageVerdict['tone'], StatusLineTone> = {
    critical: 'failing', action: 'failing', attention: 'attention', blackout: 'measuring', measuring: 'measuring', ok: 'ok',
  }
  return <SharedStatusLine tone={tones[verdict.tone]} testId="diag-status" dataTone={verdict.tone}
    title={verdict.tone === 'ok' ? <Box component="span" sx={{ color: theme.palette.success.main }}>{headline}</Box> : headline}
    detail={<Tooltip title={formatMsDualTz(m.since_unix_ms, panelTz)}><span>{`${window} · ${interval}`}</span></Tooltip>}
    meta={t('admin:diagnostics.status.read_at', { time: panelTime(readAt, panelTz, fmt.lang, true) })} />
}

import { Box, Tooltip, Typography, useTheme, type Theme } from '@mui/material'
import CheckCircleOutlineIcon from '@mui/icons-material/CheckCircleOutlineOutlined'
import ErrorOutlineIcon from '@mui/icons-material/ErrorOutlineOutlined'
import HourglassEmptyIcon from '@mui/icons-material/HourglassEmpty'
import WarningAmberIcon from '@mui/icons-material/WarningAmber'
import type { DiagnosticsSnapshot } from '@/api/diagnostics'
import { formatMsDualTz } from '@/utils/datetime'
import { wasReset, type PageVerdict, type VerdictTone } from '@/utils/diagnostics'
import { amber, type Tone } from './StatusBadge'
import type { DiagFormat } from './useDiagFormat'

// The one summary on the page. Drawn from the same tokens as the badges,
// not an <Alert severity>, for the same reason the badges are not chips: the
// theme's overrides would decide the colour, and the colour has to come from
// the worst finding.

function verdictTone(theme: Theme, tone: VerdictTone): Tone & { text: string } {
  const md = theme.palette.md
  switch (tone) {
    case 'critical':
    case 'action':
      return { bg: md.errorContainer, fg: md.onErrorContainer, text: md.onErrorContainer, Icon: ErrorOutlineIcon }
    case 'attention': {
      const a = amber(theme)
      return { ...a, text: md.onSurface, Icon: WarningAmberIcon }
    }
    case 'blackout':
    case 'measuring':
      return { bg: md.secondaryContainer, fg: md.onSecondaryContainer, text: md.onSecondaryContainer, Icon: HourglassEmptyIcon }
    case 'ok':
      return { bg: md.surfaceContainerHigh, fg: theme.palette.success.main, text: md.onSurface, Icon: CheckCircleOutlineIcon }
  }
}

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
  const tone = verdictTone(theme, verdict.tone)
  const { Icon } = tone
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

  return (
    <Box data-testid="diag-status" data-tone={verdict.tone} role="status"
      sx={{ display: 'flex', gap: 1.5, alignItems: 'flex-start', p: 2, mb: 2, borderRadius: 3, bgcolor: tone.bg }}>
      <Icon aria-hidden sx={{ color: tone.fg, mt: 0.25 }} />
      <Box sx={{ minWidth: 0 }}>
        <Typography sx={{ fontWeight: 600, color: verdict.tone === 'ok' ? tone.fg : tone.text }}>{headline}</Typography>
        <Tooltip title={formatMsDualTz(m.since_unix_ms, panelTz)}>
          <Typography variant="body2" sx={{ mt: 0.5, color: tone.text }}>{`${window} · ${interval}`}</Typography>
        </Tooltip>
        <Typography variant="caption" sx={{ display: 'block', mt: 0.25, color: tone.text, opacity: 0.8 }}>
          {t('admin:diagnostics.status.read_at', { time: panelTime(readAt, panelTz, fmt.lang, true) })}
        </Typography>
      </Box>
    </Box>
  )
}

import type { ReactNode } from 'react'
import { Box, Card, IconButton, Tooltip, Typography, useTheme } from '@mui/material'
import ContentCopyIcon from '@mui/icons-material/ContentCopy'
import HelpTip from '@/components/HelpTip'
import type { DiagnosticsSnapshot } from '@/api/diagnostics'
import { copyToClipboard } from '@/utils/clipboard'
import { formatMsDualTz } from '@/utils/datetime'
import { gauge } from '@/utils/diagnostics'
import { panelTime } from './StatusLine'
import type { DiagFormat } from './useDiagFormat'

/** The response's own fields, which no card owns: build, clocks, the poll
 *  interval actually in use beside the one asked for, and the goroutines. */
export default function SystemInfo({ snap, settingsIntervalMs, panelTz, fmt }: {
  snap: DiagnosticsSnapshot
  settingsIntervalMs: number
  panelTz: string
  fmt: DiagFormat
}) {
  const md = useTheme().palette.md
  const { t } = fmt
  const m = snap.metrics
  const interval = gauge(m, 'psp_poll_interval_ms')
  const commit = snap.commit ?? ''

  const rows: Array<[string, ReactNode]> = [
    [t('admin:diagnostics.system.version'), snap.version],
    [t('admin:diagnostics.system.build'), commit ? (
      <Box component="span" sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.25 }}>
        <Tooltip title={commit}>
          <Box component="code" sx={{ fontFamily: 'monospace', fontSize: 13 }}>{commit.slice(0, 7)}</Box>
        </Tooltip>
        <IconButton size="small" aria-label={t('admin:diagnostics.system.copy_build')} onClick={() => void copyToClipboard(commit)} sx={{ p: 0.25 }}>
          <ContentCopyIcon sx={{ fontSize: 14 }} />
        </IconButton>
      </Box>
    ) : t('admin:diagnostics.system.build_local')],
    [t('admin:diagnostics.system.uptime'), fmt.duration(snap.uptime_ms)],
    [t('admin:diagnostics.system.since'), (
      <Tooltip title={`${formatMsDualTz(m.since_unix_ms, panelTz)} · ${m.since_unix_ms}`}>
        <span>{panelTime(m.since_unix_ms, panelTz, fmt.lang)}</span>
      </Tooltip>
    )],
    [t('admin:diagnostics.system.window'), fmt.duration(m.window_ms)],
    [t('admin:diagnostics.system.interval'), interval && interval.value > 0 ? (
      <>
        {fmt.duration(interval.value)}
        <Typography component="span" variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant }}>
          {t('admin:diagnostics.system.interval_peak', { peak: fmt.duration(interval.peak) })}
        </Typography>
      </>
    ) : '—'],
    [t('admin:diagnostics.system.interval_setting'), settingsIntervalMs > 0 ? fmt.duration(settingsIntervalMs) : '—'],
    [t('admin:diagnostics.system.goroutines'), (
      <Box component="span" sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.25 }}>
        {fmt.count(snap.goroutines)}
        <HelpTip textKey="admin:diagnostics.system.goroutines_note" labelKey="admin:diagnostics.help_label_card"
          labelValues={{ title: t('admin:diagnostics.system.goroutines') }} />
      </Box>
    )],
  ]

  return (
    <Card data-testid="diag-system" sx={{ p: 2, bgcolor: md.surfaceContainerLow, minWidth: 0 }}>
      <Typography sx={{ fontWeight: 600, mb: 1 }} component="h3">{t('admin:diagnostics.system.title')}</Typography>
      <Box component="dl" sx={{ m: 0, display: 'grid', gridTemplateColumns: 'minmax(0, auto) minmax(0, 1fr)', columnGap: 2, rowGap: 0.75 }}>
        {rows.map(([label, value]) => (
          <Box key={label} sx={{ display: 'contents' }}>
            <Typography component="dt" variant="body2" sx={{ color: md.onSurfaceVariant }}>{label}</Typography>
            <Typography component="dd" variant="body2" sx={{ m: 0, fontVariantNumeric: 'tabular-nums', overflowWrap: 'anywhere', minWidth: 0 }}>
              {value}
            </Typography>
          </Box>
        ))}
      </Box>
    </Card>
  )
}

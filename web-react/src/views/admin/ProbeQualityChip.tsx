import { Box, Tooltip, Typography, alpha, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { ProbeQuality, ProbeState } from '@/api/types'

/**
 * Compact link-quality chip: state, median RTT and loss, plus a marker when the
 * link is withheld from subscriptions. An unknown state shows "no data" and no
 * numbers. Renders nothing without a measurement.
 */
export default function ProbeQualityChip({ probe, withheld, testId = 'probe-quality' }: {
  probe?: ProbeQuality | null
  /** The node/line level flag; the probe's own `withheld` also counts. */
  withheld?: boolean
  testId?: string
}) {
  const md = useTheme().palette.md
  const { t } = useTranslation('admin')
  if (!probe) return null

  const colors: Record<ProbeState, string> = {
    ok: md.primary, degraded: md.tertiary, down: md.error, unknown: md.onSurfaceVariant,
  }
  const state: ProbeState = probe.state in colors ? probe.state : 'unknown'
  const color = colors[state]
  const isWithheld = !!(withheld || probe.withheld)
  const known = state !== 'unknown'

  const tip = [
    t('nodes.probe.tip_measured'),
    !known ? t('nodes.probe.tip_unknown') : '',
    probe.reported_at ? t('nodes.probe.tip_reported', { time: new Date(probe.reported_at).toLocaleString() }) : '',
    isWithheld ? t('nodes.probe.withheld_hint') : '',
  ].filter(Boolean).join('\n')

  return (
    <Tooltip title={<span style={{ whiteSpace: 'pre-line' }}>{tip}</span>}>
      <Box data-testid={testId} data-state={state} sx={{
        display: 'inline-flex', alignItems: 'center', gap: 0.75, flexWrap: 'wrap',
        px: 1, py: 0.25, borderRadius: 9999, width: 'fit-content',
        border: `1px solid ${alpha(color, 0.5)}`, bgcolor: alpha(color, 0.08),
      }}>
        <Typography component="span" aria-label={t('nodes.probe.title')} sx={{ fontSize: 12, fontWeight: 600, color }}>
          {t(`nodes.probe.state_${state}`)}
        </Typography>
        {known && (
          <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
            {t('nodes.probe.rtt', { ms: Math.round(probe.median_rtt_ms) })} · {t('nodes.probe.loss', { percent: Math.round(probe.loss_percent) })}
          </Typography>
        )}
        {isWithheld && (
          <Typography component="span" data-testid={`${testId}-withheld`}
            sx={{ fontSize: 11, fontWeight: 600, px: 0.75, borderRadius: 9999, bgcolor: md.errorContainer, color: md.onErrorContainer }}>
            {t('nodes.probe.withheld')}
          </Typography>
        )}
      </Box>
    </Tooltip>
  )
}

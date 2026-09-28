import type { ReactElement } from 'react'
import { Box, Card, CardActionArea, Tooltip, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import type { QueueCounts } from '@/api/riskCenter'
import { useSiteStore } from '@/stores/site'
import { formatDualTz } from '@/utils/datetime'

/** The cards that filter the queue; 在线 is a way to the live tab instead. */
export type MetricCard = 'flagged' | 'suspect' | 'auto' | 'dismissed'

/**
 * The queue's headline numbers, each a click-through filter (D4): 已标记 and
 * 疑似 narrow the open queue to one level, 异地自动暂停 lists every held
 * account whatever its review state (D17), 已忽略 lists the dismissed. Each
 * is a toggle button (aria-pressed) — pressed while its filter is the one in
 * force, and a second click takes the filter away. 在线账号 opens the live
 * tab, so it is a plain button. Two columns on a phone.
 */
export default function QueueMetricCards({ counts, active, onToggle, onOpenLive }: {
  /** Undefined before the first read: every number reads "—". */
  counts: QueueCounts | undefined
  active: MetricCard | null
  onToggle: (card: MetricCard) => void
  onOpenLive: () => void
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  const num = (n: number | null | undefined) => (typeof n === 'number' ? String(n) : '—')

  const body = (label: string, value: string, color: string) => (
    <>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{label}</Typography>
      <Typography sx={{ fontSize: 24, fontWeight: 600, lineHeight: 1.3, color }}>{value}</Typography>
    </>
  )

  const toggle = (card: MetricCard, label: string, value: number | undefined, color: string): ReactElement => {
    const pressed = active === card
    return (
      <Card key={card} variant="outlined"
        sx={{ borderColor: pressed ? md.primary : undefined, bgcolor: pressed ? md.secondaryContainer : undefined }}>
        <CardActionArea aria-pressed={pressed} onClick={() => onToggle(card)} sx={{ p: 1.5, height: '100%' }}>
          {body(label, num(value), color)}
        </CardActionArea>
      </Card>
    )
  }

  // The online count is the live snapshot's, and says whose: its time is in
  // the tooltip, and a stale snapshot outlines the card. describeChild keeps
  // the card's own name ("在线账号 12") as its accessible name.
  const takenAt = counts?.online_taken_at
  const onlineHint = takenAt
    ? t('admin:risk_center.queue.metric_online_hint', { time: formatDualTz(takenAt, panelTz) })
    : t('admin:risk_center.queue.metric_online_none')

  return (
    <Box sx={{
      display: 'grid', gap: 1.5,
      gridTemplateColumns: { xs: 'repeat(2, minmax(0, 1fr))', sm: 'repeat(5, minmax(0, 1fr))' },
    }}>
      <Card variant="outlined" sx={{ borderColor: counts?.online_stale ? 'warning.main' : undefined }}>
        <Tooltip title={onlineHint} describeChild>
          <CardActionArea onClick={onOpenLive} sx={{ p: 1.5, height: '100%' }}>
            {body(t('admin:risk_center.queue.metric_online'), num(counts?.online), md.onSurface)}
          </CardActionArea>
        </Tooltip>
      </Card>
      {toggle('flagged', t('admin:risk_center.queue.metric_flagged'), counts?.flagged, 'error.main')}
      {toggle('suspect', t('admin:risk_center.queue.metric_suspect'), counts?.suspect, 'warning.main')}
      {toggle('auto', t('admin:risk_center.queue.metric_auto'), counts?.auto_suspended, 'error.main')}
      {toggle('dismissed', t('admin:risk_center.queue.metric_dismissed'), counts?.dismissed, md.onSurface)}
    </Box>
  )
}

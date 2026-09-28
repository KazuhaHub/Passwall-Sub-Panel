import { Box, Button, Chip, Tooltip, Typography, useTheme } from '@mui/material'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'

import type { RiskUserSummary, UserDevice } from '@/api/riskCenter'
import { useSiteStore } from '@/stores/site'
import { formatMsDualTz } from '@/utils/datetime'

function Entry({ d }: { d: UserDevice }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const panelTz = useSiteStore(s => s.timezone)
  // The declared device by its label, else its digest's 4-character prefix;
  // a client that declared nothing is named by what it sent.
  const name = d.label || (d.device_id4 ? `#${d.device_id4}` : d.ua || '—')
  // Full UA and the span the fetches cover, first to last.
  const detail = [d.ua, `${formatMsDualTz(d.first_at_ms, panelTz)} – ${formatMsDualTz(d.last_at_ms, panelTz)}`]
    .filter(Boolean).join('\n')
  return (
    <Box data-testid="device-entry" sx={{ py: 1, borderTop: `1px solid ${md.outlineVariant}`, display: 'flex', flexDirection: 'column', gap: 0.25 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
        <Typography sx={{ fontSize: 14, fontWeight: 600, wordBreak: 'break-all' }}>{name}</Typography>
        {d.label && d.device_id4 && (
          <Box component="span" sx={{ fontFamily: 'monospace', fontSize: 11, color: md.onSurfaceVariant }}>{`#${d.device_id4}`}</Box>
        )}
        {d.client_type && <Chip size="small" variant="outlined" label={d.client_type} />}
        <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t('admin:risk_center.drawer.device_fetches', { count: d.fetches })}
        </Typography>
      </Box>
      <Tooltip title={<Box sx={{ whiteSpace: 'pre-line', wordBreak: 'break-all' }}>{detail}</Box>}>
        <Box sx={{ fontSize: 12, color: md.onSurfaceVariant, display: 'flex', flexWrap: 'wrap', columnGap: 0.75 }}>
          <Box component="span" sx={{ fontFamily: 'monospace', wordBreak: 'break-all' }}>
            {d.sources.join(', ') || '—'}
            {d.sources_more > 0 ? ` ${t('admin:risk_center.drawer.sources_more', { count: d.sources_more })}` : ''}
          </Box>
          <span>·</span>
          <span>{t('admin:risk_center.drawer.last_seen', { time: formatMsDualTz(d.last_at_ms, panelTz) })}</span>
        </Box>
      </Tooltip>
    </Box>
  )
}

/**
 * 设备: the clients behind the account's subscription fetches in the device
 * window, one two-line entry each (what it is and how often it fetched;
 * from where and when last). An unreadable sub log says so rather than
 * passing for "no devices", and the full log is one exact link away.
 */
export default function DevicesTab({ summary }: { summary: RiskUserSummary }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const u = summary.user
  const logs = `/admin/logs?tab=sub&user_id=${u.id}&upn=${encodeURIComponent(u.upn)}`
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Typography sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
        {t('admin:risk_center.drawer.devices_intro', { hours: summary.device_window_hours })}
      </Typography>
      {summary.devices_unavailable
        ? <Typography sx={{ fontSize: 13, color: md.error }}>{t('admin:risk_center.drawer.devices_unavailable')}</Typography>
        : summary.devices.length === 0
          ? <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>{t('admin:risk_center.drawer.devices_empty')}</Typography>
          : <Box>{summary.devices.map((d, i) => <Entry key={`${d.device_id4}/${d.ua}/${i}`} d={d} />)}</Box>}
      <Button size="small" component={RouterLink} to={logs} sx={{ alignSelf: 'flex-start', textTransform: 'none' }}>
        {t('admin:risk_center.drawer.open_sub_logs')}
      </Button>
    </Box>
  )
}

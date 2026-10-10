import { Alert, Box, Button, Stack, Typography, useTheme } from '@mui/material'
import { Link as RouterLink, useLocation } from 'react-router'
import type { DestinationRecentHits } from '@/api/accessControl'
import { ToneBadge } from '@/components/ToneBadge'
import { accessTone } from '@/utils/accessControl'
import { useSiteStore } from '@/stores/site'
import { useAccessTranslation } from '../../accessControl/useAccessTranslation'
import { recordsSearch } from '../../accessControl/records/recordsParams'
import HitLossNotice from '../../accessControl/HitLossNotice'
import { hitTime } from '../../accessControl/records/hitTime'

const P = 'admin:access_control.account.', R = 'admin:access_control.records.'
export default function RecentHits({ userId, hits, available, disabled = false, stale = false }: {
  userId: number; hits: DestinationRecentHits; available: boolean; disabled?: boolean; stale?: boolean
}) {
  const { t, number, i18n } = useAccessTranslation(['admin']), theme = useTheme(), location = useLocation()
  const timezone = useSiteStore(s => s.timezone), time = hitTime(i18n.language, timezone)
  const local = location.pathname === '/admin/access-control'
  const records = local ? recordsSearch(new URLSearchParams(location.search), { user_id: userId, panel_id: undefined, source: undefined, action: undefined,
    since: `${hits.days}d`, until: undefined, group_by: 'none', include_trial: false, page: 1, page_size: 50 }, hits.days) : new URLSearchParams({ tab: 'records', rec_user: String(userId), rec_since: `${hits.days}d` })
  // A summary's "all" link clears earlier record filters, while unrelated
  // list state stays available when the administrator returns to that tab.
  records.set('tab', 'records')
  for (const key of ['user', 'sheet', 'list', 'node_state']) records.delete(key)
  const coverage = local ? new URLSearchParams(location.search) : new URLSearchParams()
  for (const key of ['user', 'list', 'node_state', 'q', 'rec_q']) coverage.delete(key)
  coverage.set('sheet', 'nodes')
  const state = { ...location.state }; delete state.drawer; delete state.prefill
  const navState = Object.keys(state).length ? state : null
  const linkSx = { minWidth: 44, minHeight: 44, alignSelf: 'flex-start' }
  return <Stack spacing={1.5}>
    <Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between' }}>
      <Typography variant="subtitle2">{t(`${P}hits_title`, { count: hits.days, days: hits.days })}</Typography>
      <Button component={RouterLink} disabled={disabled} replace={local} state={navState} to={{ pathname: '/admin/access-control', search: `?${records}` }} sx={linkSx}>{t(`${P}hits_all`)}</Button>
    </Stack>
    {stale && <Alert severity="warning">{t(`${P}hits_stale`)}</Alert>}
    {!available && <Alert severity="info"><Typography variant="body2">{t(`${P}hits_unavailable`)}</Typography><Button component={RouterLink} disabled={disabled} replace={local} state={navState} to={{ pathname: '/admin/access-control', search: `?${coverage}` }} sx={linkSx}>{t('admin:access_control.coverage.open')}</Button></Alert>}
    <HitLossNotice losses={hits.losses} />
    {hits.items.length === 0 && available && <Typography variant="body2">{t(`${P}hits_empty`, { count: hits.days, days: hits.days })}</Typography>}
    {hits.items.map(row => {
      const group = row.source.startsWith('g'), kind = group ? 'deny' : row.action
      return <Box key={`${row.source}:${row.action}`} sx={{ p: 1.5, borderRadius: 2, bgcolor: theme.palette.md.surfaceContainerHigh, minWidth: 0 }}>
        <Stack direction="row" sx={{ alignItems: 'flex-start', gap: 1 }}>
          <Box sx={{ flex: 1, minWidth: 0 }}><ToneBadge tone={accessTone(theme, kind)} label={t(`${R}${kind}`)} wrap />
            <Typography sx={{ mt: .5, overflowWrap: 'anywhere', fontStyle: row.source_name === null ? 'italic' : undefined }}>{row.source_name ?? t(`${R}${group ? 'deleted_group' : 'deleted_policy'}`)}</Typography></Box>
          <Typography sx={{ fontVariantNumeric: 'tabular-nums', textAlign: 'right', maxWidth: '45%', overflowWrap: 'anywhere' }}>{number(row.count)}</Typography>
        </Stack>
        <Stack direction="row" sx={{ mt: 1, gap: 1, flexWrap: 'wrap', minWidth: 0 }}>{row.top_dests.map(dest => <Typography key={`${dest.dest}:${dest.port}`} variant="body2" sx={{ fontFamily: 'monospace', overflowWrap: 'anywhere' }}>{dest.dest}{dest.port > 0 ? `:${dest.port}` : ''}</Typography>)}</Stack>
        <Stack direction="row" sx={{ mt: .5, gap: 1, flexWrap: 'wrap', minWidth: 0 }}>{row.panels.map(panel => <Typography key={panel.panel_id} variant="caption" sx={{ overflowWrap: 'anywhere' }}>{panel.name ?? `#${panel.panel_id}`}</Typography>)}</Stack>
        <Typography variant="caption" component="div" sx={{ mt: .5 }}>{t(`${R}recent`)} · {time.full(row.last_at)}</Typography>
      </Box>
    })}
  </Stack>
}

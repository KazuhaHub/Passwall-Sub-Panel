import { Box, Button, IconButton, Typography, useTheme } from '@mui/material'
import MoreVertIcon from '@mui/icons-material/MoreVert'
import type { DestinationHitRecord } from '@/api/destinationHits'
import { useAccessTranslation } from '../useAccessTranslation'
import type { hitTime } from './hitTime'
import { ToneBadge } from '@/components/ToneBadge'
import { accessTone } from '@/utils/accessControl'
const P = 'admin:access_control.records.'

export default function HitRow({ row, time, onUser, onSource, onMenu }: { row: DestinationHitRecord; time: ReturnType<typeof hitTime>; onUser: (id: number) => void; onSource: (source: string) => void; onMenu: (anchor: HTMLElement) => void }) {
  const { t, number } = useAccessTranslation(['admin']), theme = useTheme()
  const group = row.source.startsWith('g'), trial = group && row.action === 'observe'
  const action = trial ? 'trial' : group ? 'deny' : row.action
  return <Box sx={{ display: 'grid', gap: 1, alignItems: 'center', p: 1.5, borderBottom: 1, borderColor: 'divider', minWidth: 0,
    gridTemplateColumns: { xs: 'minmax(0,1fr) auto', lg: '130px minmax(100px,1fr) minmax(260px,2fr) minmax(90px,.7fr) 65px 44px' },
    gridTemplateAreas: { xs: '"user count" "destination menu" "time time"', lg: '"time user destination panel count menu"' } }}>
    <Box sx={{ gridArea: 'time', minWidth: 0 }} title={t(`${P}first_last`, { first: time.full(row.first_at), last: time.full(row.last_at) })}>
      <Typography variant="body2">{time.range(row.hour)}<Box component="span" sx={{ display: { xs: 'inline', lg: 'none' } }}> · {row.panel_name ?? `#${row.panel_id}`}</Box></Typography>
    </Box>
    <Box sx={{ gridArea: 'user', minWidth: 0 }}>{row.user_id > 0 ? <Button onClick={() => onUser(row.user_id)} sx={{ textTransform: 'none', justifyContent: 'flex-start', maxWidth: '100%', overflowWrap: 'anywhere', textAlign: 'left' }}>{row.user_upn ?? t(`${P}deleted_account`, { id: row.user_id })}</Button> : <Typography variant="body2">{t(`${P}group_level`)}</Typography>}</Box>
    <Box sx={{ gridArea: 'destination', minWidth: 0, display: 'grid', gap: 1, alignItems: 'center', gridTemplateColumns: { xs: 'minmax(0,.8fr) minmax(0,1.2fr)', lg: 'minmax(115px,.8fr) minmax(130px,1.2fr)' } }}>
      <Button onClick={() => onSource(row.source)} sx={{ textTransform: 'none', minWidth: 0, textAlign: 'left', justifyContent: 'flex-start' }}><Box sx={{ minWidth: 0, maxWidth: '100%' }}><ToneBadge tone={accessTone(theme, action)} label={t(`${P}${action}`)} wrap /><Typography component="span" variant="body2" title={row.source_name ?? undefined} sx={{ display: 'block', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontStyle: row.source_name === null ? 'italic' : undefined }}>{row.source_name ?? t(`${P}${group ? 'deleted_group' : 'deleted_policy'}`)}</Typography></Box></Button>
      <Typography variant="body2" title={row.dest} sx={{ fontFamily: 'monospace', minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{row.dest}{row.port > 0 ? `:${row.port}` : ''}</Typography>
    </Box>
    <Typography variant="body2" sx={{ gridArea: 'panel', display: { xs: 'none', lg: 'block' }, overflowWrap: 'anywhere' }}>{row.panel_name ?? `#${row.panel_id}`}</Typography>
    <Typography sx={{ gridArea: 'count', textAlign: 'right', fontVariantNumeric: 'tabular-nums', overflowWrap: 'anywhere', minWidth: 0 }}>{number(row.count)}</Typography>
    <IconButton sx={{ gridArea: 'menu', width: 44, height: 44 }} aria-label={t(`${P}row_menu`)} onClick={e => onMenu(e.currentTarget)}><MoreVertIcon /></IconButton>
  </Box>
}

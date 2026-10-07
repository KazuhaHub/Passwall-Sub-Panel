import { useState } from 'react'
import { Box, Button, Divider, IconButton, Menu, MenuItem, Stack, useMediaQuery, useTheme } from '@mui/material'
import MoreHorizIcon from '@mui/icons-material/MoreHoriz'
import SearchIcon from '@mui/icons-material/Search'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'
import PageHeader from '@/components/PageHeader'
import UserAutocomplete from '@/components/UserAutocomplete'

const P = 'admin:access_control.'
export default function AccessPageHeader({ userId, paused, busy, onTest, onOpenUser, onSettings, onPause }: {
  userId: number | null
  paused?: boolean
  busy: boolean
  onTest: () => void
  onOpenUser: (id: number) => void
  onSettings: () => void
  onPause: (paused: boolean) => Promise<void>
}) {
  const { t } = useAccessTranslation(['admin'])
  const mobile = useMediaQuery(useTheme().breakpoints.down('sm'))
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  return <>
    <PageHeader title={t(`${P}title`)} subtitle={t(`${P}subtitle`)} actionsSx={{ width: { xs: '100%', sm: 'auto' }, minWidth: 0 }} actions={
      <Stack direction="row" sx={{ width: '100%', flexWrap: 'wrap', alignItems: 'center', gap: 1 }}>
        {mobile ? <IconButton aria-label={t(`${P}test.title`)} onClick={onTest}><SearchIcon /></IconButton> : <Button startIcon={<SearchIcon />} onClick={onTest}>{t(`${P}test.title`)}</Button>}
        <Box sx={{ width: { xs: '100%', sm: 280 }, order: { xs: 1, sm: 0 }, minWidth: 0, '& > .MuiAutocomplete-root': { width: '100%' } }}>
          <UserAutocomplete key={userId ?? 'lookup'} label={t(`${P}view_account`)} value={null} width={280} onChange={id => { if (id) onOpenUser(id) }} />
        </Box>
        <IconButton disabled={busy} aria-label={t(`${P}more`)} aria-haspopup="menu" aria-expanded={!!anchor} onClick={event => setAnchor(event.currentTarget)}><MoreHorizIcon /></IconButton>
      </Stack>
    } />
    <Menu anchorEl={anchor} open={!!anchor} onClose={() => setAnchor(null)}>
      <MenuItem onClick={() => { setAnchor(null); onSettings() }}>{t(`${P}settings.title`)}</MenuItem>
      <Divider />
      <MenuItem disabled={busy || paused === undefined} sx={{ color: paused ? 'primary.main' : 'error.main' }} onClick={() => { setAnchor(null); void onPause(!paused) }}>{t(`${P}${paused ? 'resume' : 'pause'}`)}</MenuItem>
    </Menu>
  </>
}

import { Box, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'

import UserAutocomplete from '@/components/UserAutocomplete'
import UserLookupDetail from './UserLookupDetail'

/**
 * The user lookup: pick one account, see everything the panel knows about
 * it. The id lives in the URL (?tab=user&id=), owned by the page, so a
 * lookup is a link an admin can copy; this tab only hands a pick up.
 */
export default function UserLookupTab({ userId, onPick }: {
  userId: number | null
  onPick: (userId: number | null) => void
}) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <UserAutocomplete value={userId} onChange={onPick} label={t('admin:risk_center.lookup.pick')} width={320} />
      {userId
        // Keyed by id: switching accounts starts every section afresh, so
        // nothing of one account (an expanded row, a page) carries over.
        ? <UserLookupDetail key={userId} userId={userId} />
        : (
          <Typography sx={{ fontSize: 13, color: md.onSurfaceVariant }}>
            {t('admin:risk_center.lookup.pick_hint')}
          </Typography>
        )}
    </Box>
  )
}

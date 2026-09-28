import { Box, Button, Typography, useTheme } from '@mui/material'
import { Link as RouterLink } from 'react-router'
import { useTranslation } from 'react-i18next'

import { UserActivity } from '../../UserActivity'
import FlagRecordsTab from '../FlagRecordsTab'

/**
 * 时间线: the account's flag records, newest first, then its recent panel
 * sign-ins, and an exact link to the whole auth log for it (by id: a search
 * by name would also match other accounts whose names contain it).
 */
export default function TimelineTab({ userId, upn }: { userId: number; upn: string }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <FlagRecordsTab userId={userId} compact />
      <Box>
        <Typography component="h3" sx={{ fontSize: 14, fontWeight: 600, color: md.onSurface }}>
          {t('admin:risk_center.drawer.logins_title')}
        </Typography>
        <UserActivity userId={userId} showTitle={false} />
      </Box>
      <Button size="small" component={RouterLink}
        to={`/admin/logs?tab=auth&user_id=${userId}&upn=${encodeURIComponent(upn)}`}
        sx={{ alignSelf: 'flex-start', textTransform: 'none' }}>
        {t('admin:risk_center.drawer.open_auth_logs')}
      </Button>
    </Box>
  )
}

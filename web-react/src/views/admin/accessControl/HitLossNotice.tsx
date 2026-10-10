import { Alert, Stack, Typography } from '@mui/material'
import type { DestinationAuditLosses } from '@/api/accessControl'
import { useAccessTranslation } from './useAccessTranslation'

const P = 'admin:access_control.records.'
export default function HitLossNotice({ losses }: { losses: DestinationAuditLosses }) {
  const { t } = useAccessTranslation(['admin'])
  const observed = losses.rows > 0 || losses.events > 0 || losses.unmatched > 0
  return <Alert severity={observed ? 'warning' : 'info'}>
    <Typography variant="body2">{t(`${P}${observed ? 'loss_notice' : 'incomplete'}`)}</Typography>
    {observed && <Stack direction="row" sx={{ flexWrap: 'wrap', gap: 1 }}>
      {losses.rows > 0 && <Typography variant="caption">{t(`${P}loss_rows`, { count: losses.rows })}</Typography>}
      {losses.events > 0 && <Typography variant="caption">{t(`${P}loss_events`, { count: losses.events })}</Typography>}
      {losses.unmatched > 0 && <Typography variant="caption">{t(`${P}loss_unmatched`, { count: losses.unmatched })}</Typography>}
    </Stack>}
  </Alert>
}

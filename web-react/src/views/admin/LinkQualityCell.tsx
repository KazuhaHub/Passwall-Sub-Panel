import { Box, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import { pickLinkProbe, probeQualityOf } from '@/api/transitProbe'
import { useTransitProbes } from '@/query/transitProbe'
import { useQueryScope } from '@/query/useQueryScope'
import ProbeQualityChip from './ProbeQualityChip'

/**
 * The quality of one tunnel (entry to exit server): the end-to-end result when
 * there is one, else the transport result. A failed load or a link nobody has
 * probed yet shows an unobtrusive dash.
 */
export default function LinkQualityCell({ entry, exit, testId = 'link-quality' }: { entry: number; exit: number; testId?: string }) {
  const md = useTheme().palette.md
  const { t } = useTranslation('admin')
  const scope = useQueryScope()
  const q = useTransitProbes(scope, entry, { enabled: entry > 0 && exit > 0 })
  const item = q.data ? pickLinkProbe(q.data, entry, exit) : undefined
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
      <Typography component="span" sx={{ fontSize: 12, color: md.onSurfaceVariant }}>{t('nodes.probe.quality')}</Typography>
      {item
        ? <ProbeQualityChip probe={probeQualityOf(item)} testId={testId} />
        : <Typography component="span" data-testid={`${testId}-none`} aria-label={t('nodes.probe.no_link_data')} sx={{ fontSize: 12, color: md.onSurfaceVariant }}>—</Typography>}
    </Box>
  )
}

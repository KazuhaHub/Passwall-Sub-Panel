import { Box, Typography, useTheme } from '@mui/material'
import { useTranslation } from 'react-i18next'
import type { RouteLink } from '@/api/routes'
import ProbeQualityChip from '../ProbeQualityChip'

/**
 * The tunnel from the node's server to an exit, in words: the server is gone,
 * missing, not ready (which end has not applied it) or ready, plus the measured
 * quality. in_applied is the exit's end and out_applied the entry's.
 */
export default function RouteLinkStatus({ link, testId }: { link: RouteLink; testId: string }) {
  const { t } = useTranslation(['admin'])
  const md = useTheme().palette.md
  const K = 'admin:nodes.routes.link.'
  const state = link.gone ? 'gone' : !link.exists ? 'missing' : link.ready ? 'ready' : 'not_ready'
  const color = state === 'ready' ? md.primary : state === 'missing' || state === 'gone' ? md.error : md.tertiary
  return (
    <Box data-testid={testId} data-state={state} sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 1 }}>
      <Typography component="span" sx={{ fontSize: 12, fontWeight: 600, color }}>{t(`${K}${state}`)}</Typography>
      {link.exists && (
        <Typography component="span" data-testid={`${testId}-ends`} sx={{ fontSize: 12, color: md.onSurfaceVariant }}>
          {t(`${K}in_end`)}: {t(link.in_applied ? `${K}applied` : `${K}waiting`)} · {t(`${K}out_end`)}: {t(link.out_applied ? `${K}applied` : `${K}waiting`)}
        </Typography>
      )}
      {link.quality && <ProbeQualityChip probe={link.quality} testId={`${testId}-quality`} />}
    </Box>
  )
}

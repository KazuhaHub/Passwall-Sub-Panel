import type { ReactNode } from 'react'
import { Box, Typography, useTheme } from '@mui/material'
import { stateTone } from './ToneBadge'

export type StatusLineTone = 'failing' | 'attention' | 'measuring' | 'ok' | 'quiet'

export default function StatusLine({ tone, title, detail, actions, meta, testId, dataTone = tone }: {
  tone: StatusLineTone
  title: ReactNode
  detail?: ReactNode
  actions?: ReactNode
  meta?: ReactNode
  testId?: string
  dataTone?: string
}) {
  const colors = stateTone(useTheme(), tone)
  const { Icon } = colors
  return <Box data-testid={testId} data-tone={dataTone} role="status"
    sx={{ display: 'flex', gap: 1.5, alignItems: 'flex-start', p: 2, mb: 2, borderRadius: 3, bgcolor: colors.bg }}>
    <Icon aria-hidden sx={{ color: colors.iconColor ?? colors.fg, mt: 0.25 }} />
    <Box sx={{ minWidth: 0 }}>
      <Typography sx={{ fontWeight: 600, color: colors.fg }}>{title}</Typography>
      {detail != null && <Typography variant="body2" sx={{ mt: 0.5, color: colors.fg }}>{detail}</Typography>}
      {meta != null && <Typography variant="caption" sx={{ display: 'block', mt: 0.25, color: colors.fg, opacity: 0.8 }}>{meta}</Typography>}
    </Box>
    {actions != null && <Box sx={{ ml: 'auto', flexShrink: 0 }}>{actions}</Box>}
  </Box>
}

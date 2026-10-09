import type { ReactNode } from 'react'
import { Box, Typography, useTheme } from '@mui/material'
import { stateTone } from './ToneBadge'

export type StatusLineTone = 'failing' | 'attention' | 'measuring' | 'ok' | 'quiet'

export default function StatusLine({ tone, title, detail, actions, meta, announcement, stackActionsOnMobile = false, testId, dataTone = tone }: {
  tone: StatusLineTone
  title: ReactNode
  detail?: ReactNode
  actions?: ReactNode
  meta?: ReactNode
  /** A stable primary verdict; rapidly changing secondary values stay out of the live region. */
  announcement?: ReactNode
  stackActionsOnMobile?: boolean
  testId?: string
  dataTone?: string
}) {
  const colors = stateTone(useTheme(), tone)
  const { Icon } = colors
  return <Box data-testid={testId} data-tone={dataTone} role={announcement === undefined ? 'status' : undefined}
    sx={{ display: 'flex', flexWrap: stackActionsOnMobile ? { xs: 'wrap', sm: 'nowrap' } : undefined, gap: 1.5, alignItems: 'flex-start', p: 2, mb: 2, borderRadius: 3, bgcolor: colors.bg }}>
    <Icon aria-hidden sx={{ color: colors.iconColor ?? colors.fg, mt: 0.25 }} />
    {announcement !== undefined && <Box role="status" sx={{ position: 'absolute', width: '1px', height: '1px', overflow: 'hidden', clipPath: 'inset(50%)' }}>{announcement}</Box>}
    <Box sx={{ minWidth: 0, flex: stackActionsOnMobile ? 1 : undefined }}>
      <Typography aria-hidden={announcement !== undefined ? true : undefined} sx={{ fontWeight: 600, color: colors.fg }}>{title}</Typography>
      {detail != null && <Typography variant="body2" sx={{ mt: 0.5, color: colors.fg }}>{detail}</Typography>}
      {meta != null && <Typography variant="caption" sx={{ display: 'block', mt: 0.25, color: colors.fg, opacity: 0.8 }}>{meta}</Typography>}
    </Box>
    {actions != null && <Box sx={{ ml: stackActionsOnMobile ? { xs: 0, sm: 'auto' } : 'auto', flexBasis: stackActionsOnMobile ? { xs: '100%', sm: 'auto' } : undefined, flexShrink: 0 }}>{actions}</Box>}
  </Box>
}

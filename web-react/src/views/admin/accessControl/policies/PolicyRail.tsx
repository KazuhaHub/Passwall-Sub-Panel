import type { ReactNode } from 'react'
import { Box, Typography, useMediaQuery, useTheme } from '@mui/material'

/**
 * The match-order rail (S2): a 2px vertical line down the left, a numbered dot
 * per step. The same line and dots draw the test sheet's trace, so the two
 * read as one picture of the order. Numbers come from pipelineSteps(), never
 * from here.
 */
export function RailStep({ n, title, caption, actions, children, last = false }: {
  n: number
  title: ReactNode
  caption?: ReactNode
  actions?: ReactNode
  children?: ReactNode
  last?: boolean
}) {
  const theme = useTheme()
  const md = theme.palette.md
  const phone = useMediaQuery(theme.breakpoints.down('sm'))
  const dot = phone ? 20 : 24
  return (
    <Box sx={{ position: 'relative', pl: phone ? '28px' : '40px', pb: last ? 0 : 2 }}>
      {!last && <Box aria-hidden sx={{ position: 'absolute', left: phone ? 8 : 11, top: dot, bottom: 0, width: '2px', bgcolor: md.outlineVariant }} />}
      <Box aria-hidden sx={{
        position: 'absolute', left: 0, top: 0, width: dot, height: dot, borderRadius: '50%', bgcolor: md.surfaceContainerHighest,
        color: md.onSurface, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 12, fontWeight: 600,
      }}>{n}</Box>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 1, minHeight: dot, mb: children ? 1 : 0 }}>
        <Typography component="h2" sx={{ fontSize: 16, fontWeight: 600, minWidth: 0 }}>
          {title}
          {caption ? <Box component="span" sx={{ fontSize: 13, fontWeight: 400, color: md.onSurfaceVariant }}>{' · '}{caption}</Box> : null}
        </Typography>
        {actions}
      </Box>
      {children}
    </Box>
  )
}

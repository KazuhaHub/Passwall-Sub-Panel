import type { ReactNode } from 'react'
import { Box, Typography } from '@mui/material'

// PageHeader is the ONE canonical admin-page header — title (h4) + optional
// subtitle + optional right-aligned actions, with uniform spacing — so every
// nav page reads at the same size and height instead of each view rolling its
// own ad-hoc heading. Place it as the first child of the page's `<Box sx={{ p: 3 }}>`.
export default function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
}) {
  return (
    <Box sx={{ display: 'flex', alignItems: 'flex-end', justifyContent: 'space-between', flexWrap: 'wrap', gap: 2, mb: 2 }}>
      <Box>
        <Typography variant="h4">{title}</Typography>
        {subtitle ? (
          <Typography variant="body2" sx={{ mt: 0.5, color: 'text.secondary' }}>{subtitle}</Typography>
        ) : null}
      </Box>
      {actions ? <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>{actions}</Box> : null}
    </Box>
  )
}

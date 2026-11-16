import type { ReactNode } from 'react'
import { Box, Typography, useTheme } from '@mui/material'

/**
 * The bar that sticks to the bottom of a list while rows are selected: how many,
 * and what can be done with them. It is part of the page (sticky, not fixed), so
 * it never covers the last rows, and it wraps to two lines on a phone.
 */
export default function BulkBar({ count, label, children }: { count: number; label: string; children: ReactNode }) {
  const md = useTheme().palette.md
  if (count <= 0) return null
  return (
    <Box role="region" aria-label={label} data-testid="bulk-bar" sx={{
      position: 'sticky', bottom: 8, zIndex: 2, mt: 1.5, px: 2, py: 1, borderRadius: 3,
      bgcolor: md.secondaryContainer, color: md.onSecondaryContainer, boxShadow: 3,
      display: 'flex', alignItems: 'center', gap: 1.5, flexWrap: 'wrap',
    }}>
      <Typography sx={{ fontSize: 14, fontWeight: 500 }}>{label}</Typography>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexWrap: 'wrap', ml: { sm: 'auto' }, flex: { xs: '1 1 100%', sm: '0 1 auto' } }}>{children}</Box>
    </Box>
  )
}

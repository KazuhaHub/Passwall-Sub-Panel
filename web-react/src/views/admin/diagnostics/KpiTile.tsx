import type { ReactNode } from 'react'
import { Box, Typography, useTheme } from '@mui/material'

/** One figure on an area card: what it is, the number, and what it is out of. */
export default function KpiTile({ label, value, caption, color, title }: {
  label: string
  value: ReactNode
  caption?: ReactNode
  /** Only for a figure that is itself a failure; nothing else is coloured. */
  color?: string
  /** Raw values behind a folded figure, on hover. */
  title?: string
}) {
  const md = useTheme().palette.md
  return (
    <Box title={title} sx={{ p: 1.5, borderRadius: 2, bgcolor: md.surfaceContainer, minWidth: 0 }}>
      <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant }}>{label}</Typography>
      <Typography sx={{
        fontSize: 17, fontWeight: 600, lineHeight: 1.4, fontVariantNumeric: 'tabular-nums',
        color: color ?? md.onSurface, overflowWrap: 'anywhere',
      }}>{value}</Typography>
      {caption ? (
        <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant, fontVariantNumeric: 'tabular-nums' }}>
          {caption}
        </Typography>
      ) : null}
    </Box>
  )
}

/** Two tiles a row on a phone, as many as fit on a wider screen. */
export function KpiGrid({ children }: { children: ReactNode }) {
  return (
    <Box sx={{
      display: 'grid', gap: 1, my: 1.5,
      gridTemplateColumns: { xs: 'repeat(2, minmax(0, 1fr))', md: 'repeat(auto-fit, minmax(170px, 1fr))' },
    }}>
      {children}
    </Box>
  )
}

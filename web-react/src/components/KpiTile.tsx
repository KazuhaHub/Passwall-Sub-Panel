import type { ReactNode } from 'react'
import { Box, CardActionArea, Typography, useTheme } from '@mui/material'

export default function KpiTile({ label, value, caption, color, title, pressed = false, onToggle }: {
  label: string
  value: ReactNode
  caption?: ReactNode
  color?: string
  title?: string
  pressed?: boolean
  onToggle?: () => void
}) {
  const md = useTheme().palette.md
  const content = <>
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
  </>
  const surface = { p: 1.5, borderRadius: 2, bgcolor: md.surfaceContainer, minWidth: 0 }
  if (!onToggle) return <Box title={title} sx={surface}>{content}</Box>
  return <CardActionArea title={title} aria-pressed={pressed} onClick={onToggle}
    sx={{ ...surface, textAlign: 'left', bgcolor: pressed ? md.secondaryContainer : md.surfaceContainer,
      outline: pressed ? `1px solid ${md.primary}` : undefined, outlineOffset: -1 }}>
    {content}
  </CardActionArea>
}

export function KpiGrid({ children }: { children: ReactNode }) {
  return <Box sx={{
    display: 'grid', gap: 1, my: 1.5,
    gridTemplateColumns: { xs: 'repeat(2, minmax(0, 1fr))', md: 'repeat(auto-fit, minmax(170px, 1fr))' },
  }}>{children}</Box>
}

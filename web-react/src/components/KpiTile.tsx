import type { ReactNode } from 'react'
import { Box, CardActionArea, Typography, useTheme } from '@mui/material'

/**
 * One figure: what it is, the number, and what it is out of. Static by
 * default; with `onToggle` it is a filter chip in tile form (CardActionArea
 * plus aria-pressed, the same size, fill and type), pressed by the secondary
 * container with a 1px primary outline. A second click is the page's cue to
 * clear the filter.
 */
export default function KpiTile({ label, value, caption, color, title, pressed, onToggle }: {
  label: string
  value: ReactNode
  caption?: ReactNode
  /** Only for a figure that is itself a failure; nothing else is coloured. */
  color?: string
  /** Raw values behind a folded figure, on hover. */
  title?: string
  pressed?: boolean
  onToggle?: () => void
}) {
  const md = useTheme().palette.md
  const body = (
    <Box sx={{ p: 1.5, minWidth: 0, textAlign: 'left' }}>
      <Typography variant="caption" sx={{ display: 'block', color: md.onSurfaceVariant }}>{label}</Typography>
      <Typography component="div" sx={{
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
  if (!onToggle) {
    return <Box title={title} sx={{ borderRadius: 2, bgcolor: md.surfaceContainer, minWidth: 0 }}>{body}</Box>
  }
  return (
    <Box sx={{
      borderRadius: 2, minWidth: 0, overflow: 'hidden',
      bgcolor: pressed ? md.secondaryContainer : md.surfaceContainer,
      outline: pressed ? `1px solid ${md.primary}` : 'none', outlineOffset: -1,
    }}>
      <CardActionArea title={title} aria-pressed={pressed === true} onClick={onToggle} sx={{ minHeight: 44 }}>{body}</CardActionArea>
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

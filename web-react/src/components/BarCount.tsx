import { Box, Typography, alpha, useTheme } from '@mui/material'

/** The width of a bar as a percentage of the page's largest value; a non-zero value never rounds away to nothing. */
export function barPercent(value: number, max: number): number {
  if (!(max > 0) || !(value > 0)) return 0
  return Math.min(100, Math.max(2, Math.round((value / max) * 100)))
}

/**
 * A count with a bar behind it. The bar is relative to the largest count on the
 * page (the caller passes it) and is a hint, not a measure: the number is always
 * written. The fill is the primary token with an alpha, so both themes read.
 */
export default function BarCount({ value, max, formatter }: { value: number; max: number; formatter?: Intl.NumberFormat }) {
  const md = useTheme().palette.md
  const num = formatter ?? new Intl.NumberFormat()
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, minWidth: 0 }}>
      <Box aria-hidden sx={{ flex: 1, minWidth: 40, height: 8, borderRadius: 1, overflow: 'hidden', bgcolor: alpha(md.primary, 0.12) }}>
        <Box data-testid="bar-fill" style={{ width: `${barPercent(value, max)}%` }} sx={{ height: '100%', bgcolor: alpha(md.primary, 0.6) }} />
      </Box>
      <Typography component="span" sx={{ minWidth: 56, textAlign: 'right', fontVariantNumeric: 'tabular-nums', fontSize: 14 }}>{num.format(value)}</Typography>
    </Box>
  )
}

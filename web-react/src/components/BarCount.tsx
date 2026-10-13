import { Box, Typography, useTheme } from '@mui/material'
import { useAccessTranslation } from '@/views/admin/accessControl/useAccessTranslation'

export default function BarCount({ count, max }: { count: number; max: number }) {
  const theme = useTheme(), { number } = useAccessTranslation('admin')
  return <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', minWidth: 0 }}>
    <Box aria-hidden sx={{ width: 96, maxWidth: '30vw', height: 8, borderRadius: 1, bgcolor: theme.palette.md.surfaceContainerHigh, overflow: 'hidden' }}><Box sx={{ height: '100%', width: `${max > 0 ? Math.min(100, Math.max(0, count / max * 100)) : 0}%`, bgcolor: theme.palette.md.primary }} /></Box>
    <Typography variant="body2" sx={{ fontVariantNumeric: 'tabular-nums' }}>{number(count)}</Typography>
  </Box>
}
